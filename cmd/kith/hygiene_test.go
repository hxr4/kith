package main

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func goList(t *testing.T, env []string, args ...string) string {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}
	c := exec.Command("go", append([]string{"list"}, args...)...)
	c.Dir = "../.."
	c.Env = append(os.Environ(), env...)
	// Only stdout is the listing; stderr carries "go: downloading …" noise
	// on machines whose module cache is cold.
	var stderr bytes.Buffer
	c.Stderr = &stderr
	out, err := c.Output()
	if err != nil {
		t.Fatalf("go list: %v\n%s%s", err, out, stderr.String())
	}
	return string(out)
}

// §17 #4: no CGO anywhere in the non-stdlib dependency graph, any target.
func TestNoCgoInDependencyGraph(t *testing.T) {
	for _, target := range [][]string{{"GOOS=darwin", "GOARCH=arm64"}, {"GOOS=darwin", "GOARCH=amd64"}, {"GOOS=windows", "GOARCH=amd64"}} {
		out := goList(t, append(target, "CGO_ENABLED=1"), "-deps", "-f", "{{if not .Standard}}{{if .CgoFiles}}{{.ImportPath}}{{end}}{{end}}", "./cmd/kith")
		if s := strings.TrimSpace(out); s != "" {
			t.Fatalf("%v: packages with cgo files:\n%s", target, s)
		}
	}
}

// §17 #6: none of the §2 non-goals are pulled in as dependencies.
func TestNoNonGoalDependencies(t *testing.T) {
	out := goList(t, nil, "-deps", "-f", "{{.ImportPath}}", "./cmd/kith")
	for _, bad := range []string{"golang.org/x/crypto/ssh", "tailscale", "docker", "k8s.io", "kubernetes", "containerd",
		"net/http/pprof", "html/template", "grpc"} {
		if strings.Contains(out, bad) {
			t.Errorf("forbidden dependency %q present", bad)
		}
	}
}

// Kith's own output must be plain ASCII: Windows PowerShell 5.1 decodes
// captured output with the OEM code page, so "—" became "â€”" (Windows
// report #6). Task logs are passed through untouched; this only checks the
// string literals kith itself prints.
func TestUserFacingStringsAreASCII(t *testing.T) {
	root := "../../internal"
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return err
		}
		f, err := parser.ParseFile(fset, p, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			for _, r := range lit.Value {
				if r > 0x7e {
					t.Errorf("%s: non-ASCII %q in string literal %s", fset.Position(lit.Pos()), r, lit.Value)
					break
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
