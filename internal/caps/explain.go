package caps

import (
	"fmt"
	"sort"
	"strings"

	"kith/internal/task"
)

// Requirement is one preflight line: what the task asks for, what the
// machine reported, and whether it matches.
type Requirement struct {
	Req    string `json:"req"`
	Have   string `json:"have"`
	Status string `json:"status"` // pass | fail | unknown
}

// Explain lists each static requirement against a (possibly cached)
// manifest, for showing a preflight before anything is sent. Check stays
// the authority: the executor re-probes the packages a task names, so a
// package missing from the advertised manifest is "unknown", not "fail".
func Explain(m Manifest, env task.Env, inputsBytes int64) []Requirement {
	var out []Requirement
	add := func(req, have string, ok bool) {
		st := "fail"
		if ok {
			st = "pass"
		}
		out = append(out, Requirement{Req: req, Have: have, Status: st})
	}
	if env.OS != "" {
		add("os = "+env.OS, m.OS, strings.HasPrefix(strings.ToLower(m.OS), strings.ToLower(env.OS)))
	}
	if env.Arch != "" {
		add("arch = "+env.Arch, m.Arch, NormArch(env.Arch) == NormArch(m.Arch))
	}
	if env.Python != "" {
		have, ok := "none", false
		if len(m.Python) > 0 {
			have = strings.Join(m.Python, ", ")
		}
		for _, v := range m.Python {
			if good, _ := MatchVersion(env.Python, v); good {
				have, ok = v, true
				break
			}
		}
		add("python "+env.Python, have, ok)
	}
	names := make([]string, 0, len(env.Packages))
	for n := range env.Packages {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		c := env.Packages[name]
		req := strings.TrimSpace(name + " " + c)
		have, ok := lookupPkg(m.Packages, name)
		switch {
		case !ok && probedByDefault(name):
			add(req, "not installed", false)
		case !ok:
			out = append(out, Requirement{Req: req, Have: "checked when sent", Status: "unknown"})
		default:
			good, err := MatchVersion(c, have)
			add(req, have, err == nil && good)
		}
	}
	if env.GPU != nil && env.GPU.Required {
		have, ok := "no NVIDIA GPU", false
		for _, g := range m.GPUs {
			have = fmt.Sprintf("%s %.1f GB", g.Name, g.VRAMGB)
			if g.VRAMGB+0.05 >= env.GPU.VRAMGB {
				ok = true
				break
			}
		}
		add(fmt.Sprintf("gpu.vram >= %g GB", env.GPU.VRAMGB), have, ok)
	}
	need := float64(inputsBytes+GiB) / GiB
	add(fmt.Sprintf("disk >= %.1f GB", need), fmt.Sprintf("%.1f GB free", m.FreeDiskGB), m.FreeDiskGB+1e-9 >= need)
	return out
}

func probedByDefault(name string) bool {
	for _, n := range DefaultPackages {
		if strings.EqualFold(n, name) {
			return true
		}
	}
	return false
}
