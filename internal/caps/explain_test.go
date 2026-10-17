package caps

import (
	"testing"

	"kith/internal/task"
)

func TestExplain(t *testing.T) {
	env := task.Env{OS: "windows", Arch: "amd64", Python: ">=3.10",
		Packages: map[string]string{"torch": ">=2.0", "transformers": ">=4.40"},
		GPU:      &task.GPUReq{VRAMGB: 12, Required: true}}
	got := Explain(win(), env, 0)
	want := map[string]string{
		"os = windows":        "pass",
		"arch = amd64":        "pass",
		"python >=3.10":       "pass",
		"torch >=2.0":         "pass",
		"transformers >=4.40": "unknown", // not in the advertised manifest
		"gpu.vram >= 12 GB":   "fail",
		"disk >= 1.0 GB":      "pass",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d requirements, want %d: %+v", len(got), len(want), got)
	}
	for _, r := range got {
		if want[r.Req] != r.Status {
			t.Errorf("%s: status %q, want %q (have %q)", r.Req, r.Status, want[r.Req], r.Have)
		}
	}
	// The failing line agrees with Check, which stays the authority.
	if e := Check(win(), env, 0); e == nil {
		t.Fatal("Check passed a task Explain marks as failing")
	}
}

func TestExplainMissingDefaultPackage(t *testing.T) {
	m := win()
	delete(m.Packages, "torch")
	got := Explain(m, task.Env{Packages: map[string]string{"torch": ">=2"}}, 0)
	if got[0].Status != "fail" || got[0].Have != "not installed" {
		t.Fatalf("torch is probed by default, so missing means not installed: %+v", got[0])
	}
}
