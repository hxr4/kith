package main

import (
	"bytes"
	"os"
	"os/exec"
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
