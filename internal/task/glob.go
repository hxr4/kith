package task

import (
	"errors"
	"io/fs"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// CleanRel validates a slash-separated relative path: no absolute paths, no
// "..", no drive letters. Returns the cleaned form.
func CleanRel(p string) (string, error) {
	p = strings.ReplaceAll(p, "\\", "/")
	if p == "" {
		return "", errors.New("empty path")
	}
	if strings.HasPrefix(p, "/") || (len(p) >= 2 && p[1] == ':') {
		return "", errors.New("path must be relative: " + p)
	}
	c := path.Clean(p)
	if c == "." || c == ".." || strings.HasPrefix(c, "../") {
		return "", errors.New("path escapes project dir: " + p)
	}
	for _, seg := range strings.Split(c, "/") {
		if seg == ".." {
			return "", errors.New("path escapes project dir: " + p)
		}
	}
	return c, nil
}

// MatchGlob matches a slash path against a pattern supporting *, ?, [..] per
// segment and ** for zero or more whole segments.
func MatchGlob(pattern, name string) bool {
	return matchSegs(strings.Split(pattern, "/"), strings.Split(name, "/"))
}

func matchSegs(pat, name []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			rest := pat[1:]
			for i := 0; i <= len(name); i++ {
				if matchSegs(rest, name[i:]) {
					return true
				}
			}
			return false
		}
		if len(name) == 0 {
			return false
		}
		ok, err := path.Match(pat[0], name[0])
		if err != nil || !ok {
			return false
		}
		pat, name = pat[1:], name[1:]
	}
	return len(name) == 0
}

func hasMeta(s string) bool { return strings.ContainsAny(s, "*?[") }

// ExpandGlobs expands patterns (relative to root) into a sorted, de-duplicated
// list of relative slash paths of regular files. A literal path that does not
// exist is an error; a pattern with no matches is not.
func ExpandGlobs(root string, patterns []string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	var all []string
	walked := false
	for _, raw := range patterns {
		p, err := CleanRel(raw)
		if err != nil {
			return nil, err
		}
		if !hasMeta(p) {
			st, err := filepathStat(root, p)
			if err != nil {
				return nil, err
			}
			if st.IsDir() {
				// A directory means "everything under it".
				p = p + "/**"
			} else {
				if !seen[p] {
					seen[p] = true
					out = append(out, p)
				}
				continue
			}
		}
		if !walked {
			all, err = walkFiles(root)
			if err != nil {
				return nil, err
			}
			walked = true
		}
		for _, f := range all {
			if !seen[f] && MatchGlob(p, f) {
				seen[f] = true
				out = append(out, f)
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

func filepathStat(root, rel string) (fs.FileInfo, error) {
	return statFn(filepath.Join(root, filepath.FromSlash(rel)))
}

func walkFiles(root string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			rel, err := filepath.Rel(root, p)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			if strings.HasSuffix(rel, ".kithpart") {
				return nil
			}
			out = append(out, rel)
		}
		return nil
	})
	return out, err
}
