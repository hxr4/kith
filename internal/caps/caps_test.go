package caps

import (
	"context"
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"kith/internal/protocol"
	"kith/internal/task"
)

func win() Manifest {
	return Manifest{OS: "windows-11", Arch: "x86_64", Python: []string{"3.11.9"},
		Packages: map[string]string{"torch": "2.3.1+cu121", "numpy": "1.26.4"},
		GPUs:     []GPU{{Name: "RTX 3050", VRAMGB: 8, FreeVRAMGB: 7.6}}, FreeDiskGB: 112, RAMGB: 16, Zstd: true}
}

func TestMatchVersion(t *testing.T) {
	cases := []struct {
		c, v string
		ok   bool
	}{
		{">=2.3+cu121", "2.3.1+cu121", true},
		{">=2.3+cu121", "2.3.0+cpu", false},
		{">=2.3+cu121", "2.2.0+cu121", false},
		{">=2.3+cu121", "2.3.1", false},
		{">=2.3", "2.3.1+cu118", true},
		{">=3.10", "3.11.9", true},
		{">=3.10", "3.9.18", false},
		{"==1.26.4", "1.26.4", true},
		{"~=1.26", "1.26.9", true},
		{">=2.0,<3", "2.5.0", true},
		{">=2.0,<3", "3.0.0", false},
		{">=2", "2.1.0rc1", true},
		{"", "anything", true},
	}
	for _, c := range cases {
		got, err := MatchVersion(c.c, c.v)
		if err != nil || got != c.ok {
			t.Errorf("%s vs %s: got %v err %v", c.c, c.v, got, err)
		}
	}
}

func TestCheckNoMatchWithRemediation(t *testing.T) {
	m := win()
	m.Packages["torch"] = "2.3.0+cpu"
	err := Check(m, task.Env{Packages: map[string]string{"torch": ">=2.3+cu121"}}, 0)
	if err == nil || err.Code != protocol.CodeNoMatch {
		t.Fatal(err)
	}
	if !strings.Contains(err.Message, "torch>=2.3+cu121 not found (found 2.3.0+cpu)") {
		t.Fatal(err.Message)
	}
	if err.Remediation != "pip install torch --index-url https://download.pytorch.org/whl/cu121" {
		t.Fatal(err.Remediation)
	}
}

func TestCheckMatrix(t *testing.T) {
	m := win()
	ok := task.Env{OS: "windows", Arch: "amd64", Python: ">=3.10", Packages: map[string]string{"torch": ">=2.3+cu121"},
		GPU: &task.GPUReq{VRAMGB: 4, Required: true}}
	if err := Check(m, ok, 10*GiB); err != nil {
		t.Fatal(err)
	}
	fails := map[string]task.Env{
		"os":     {OS: "macos"},
		"arch":   {Arch: "arm64"},
		"python": {Python: ">=3.12"},
		"pkg":    {Packages: map[string]string{"jax": ">=0.4"}},
		"gpu":    {GPU: &task.GPUReq{VRAMGB: 24, Required: true}},
	}
	for name, env := range fails {
		if err := Check(m, env, 0); err == nil || err.Code != protocol.CodeNoMatch || err.Remediation == "" {
			t.Errorf("%s: %v", name, err)
		}
	}
	// Optional GPU is not checked.
	if err := Check(Manifest{FreeDiskGB: 5}, task.Env{GPU: &task.GPUReq{VRAMGB: 4}}, 0); err != nil {
		t.Fatal(err)
	}
	if err := Check(m, task.Env{}, 112*GiB); err == nil || err.Code != protocol.CodeDiskFull {
		t.Fatal("disk precheck", err)
	}
}

func TestParseNvidiaSMI(t *testing.T) {
	g := ParseNvidiaSMI("NVIDIA GeForce RTX 3050, 8192 MiB, 7782 MiB\nbad line\n")
	if len(g) != 1 || g[0].Name != "RTX 3050" || g[0].VRAMGB != 8 || g[0].FreeVRAMGB != 7.6 {
		t.Fatalf("%+v", g)
	}
}

func TestSystemProbe(t *testing.T) {
	m, err := System{}.Probe(context.Background(), Request{Packages: []string{"pip"}, DiskPath: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if m.Arch == "" || m.OS == "" || m.FreeDiskGB <= 0 || m.RAMGB <= 0 || m.KithVer == "" {
		t.Fatalf("%+v", m)
	}
	t.Logf("manifest: %+v", m)
}

func TestRunProbe(t *testing.T) {
	py := findPython(t)
	if err := RunProbe(context.Background(), []string{py, "-c", "import sys; sys.exit(0)"}, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	err := RunProbe(context.Background(), []string{py, "-c", "import sys; print('no cuda'); sys.exit(3)"}, t.TempDir())
	if err == nil || err.Code != protocol.CodeNoMatch || !strings.Contains(err.Message, "no cuda") {
		t.Fatal(err)
	}
	if err := RunProbe(context.Background(), []string{"definitely-not-a-binary-kith"}, t.TempDir()); err == nil {
		t.Fatal("missing binary accepted")
	}
}

func findPython(t *testing.T) string {
	names := []string{"python3", "python"}
	if runtime.GOOS == "windows" {
		names = []string{"python", "python3"}
	}
	for _, n := range names {
		if p, err := exec.LookPath(n); err == nil && exec.Command(p, "-c", "import sys").Run() == nil {
			return p // runs for real (rejects Windows Store alias stubs)
		}
	}
	t.Skip("python not found")
	return ""
}
