package task

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"kith/internal/protocol"
)

const exampleSpec = `
[task]
id = "tsk_01J9Z3M4N5P6Q7R8S9T0V1W2X3"
project = "mnist-demo"
name = "train"
command = ["python", "train.py", "--epochs", "100", "--out", "out/"]

[task.inputs]
files = ["train.py", "dataset.zip"]
max_total_gb = 25

[task.env]
python = ">=3.10"
packages = { torch = ">=2.3+cu121" }
gpu = { vram_gb = 4, required = true }
probe = "python -c \"import torch; assert torch.cuda.is_available()\""

[task.outputs]
include = ["out/model.pt", "out/metrics.json", "logs/**/*.log"]
copy_back = true
retain_cap_gb = 10

[task.policy]
on_interrupt = "restart"
checkpoint = { pattern = "out/checkpoint*.pt" }
timeout_minutes = 720
`

func TestParseExampleSpec(t *testing.T) {
	s, err := ParseTOML([]byte(exampleSpec))
	if err != nil {
		t.Fatal(err)
	}
	if s.Project != "mnist-demo" || s.Command[2] != "--epochs" || s.Env.Packages["torch"] != ">=2.3+cu121" ||
		!s.Env.GPU.Required || s.Env.GPU.VRAMGB != 4 || s.Policy.OnInterrupt != "restart" ||
		s.Policy.Checkpoint.Pattern != "out/checkpoint*.pt" || !*s.Outputs.CopyBack {
		t.Fatalf("%+v", s)
	}
	want := []string{"python", "-c", "import torch; assert torch.cuda.is_available()"}
	if !reflect.DeepEqual([]string(s.Env.Probe), want) {
		t.Fatalf("probe %q", s.Env.Probe)
	}
	// JSON roundtrip is lossless.
	s2, err := ParseJSON(s.JSON())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(s, s2) {
		t.Fatalf("roundtrip\n%+v\n%+v", s, s2)
	}
}

func TestRejectShell(t *testing.T) {
	for _, body := range []string{
		"[task]\ncommand = \"python train.py\"\n",
		"[task]\ncommand=[\"x\"]\nshell = \"bash\"\n",
		"[task]\ncommand=[\"x\"]\n[task.env]\nscript = \"rm -rf /\"\n",
	} {
		_, err := ParseTOML([]byte(body))
		if protocol.CodeOf(err) != protocol.CodeProtocolError {
			t.Fatalf("accepted shell spec %q: %v", body, err)
		}
	}
	if _, err := ParseJSON([]byte(`{"command":"ls -la"}`)); err == nil {
		t.Fatal("json string command accepted")
	}
	if _, err := ParseJSON([]byte(`{"task":{"command":["ls"],"shell":true}}`)); err == nil {
		t.Fatal("json shell accepted")
	}
}

func TestValidation(t *testing.T) {
	bad := []string{
		`{"command":[]}`,
		`{"command":["x"],"id":"nope"}`,
		`{"command":["x"],"inputs":{"files":["../etc/passwd"]}}`,
		`{"command":["x"],"inputs":{"files":["/abs"]}}`,
		`{"command":["x"],"outputs":{"include":["C:/x"]}}`,
		`{"command":["x"],"policy":{"on_interrupt":"maybe"}}`,
	}
	for _, b := range bad {
		if _, err := ParseJSON([]byte(b)); err == nil {
			t.Fatalf("accepted %s", b)
		}
	}
	s, err := ParseJSON([]byte(`{"command":["python","x.py"]}`))
	if err != nil || s.Policy.OnInterrupt != "fail" || s.Outputs.RetainCapGB != 10 || s.Name != "python" {
		t.Fatalf("%+v %v", s, err)
	}
}

func TestSplitArgs(t *testing.T) {
	cases := map[string][]string{
		`python -c "print('a b')"`: {"python", "-c", "print('a b')"},
		`a  'b c'  d\x`:            {"a", "b c", `d\x`},
		`x "a\"b"`:                 {"x", `a"b`},
		`""`:                       {""},
	}
	for in, want := range cases {
		got, err := SplitArgs(in)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: %q %v", in, got, err)
		}
	}
	if _, err := SplitArgs(`"open`); err == nil {
		t.Fatal("unterminated accepted")
	}
}

func TestStateMachine(t *testing.T) {
	legal := [][2]string{{Submitted, Accepted}, {Submitted, Rejected}, {Accepted, Transferring}, {Transferring, Running},
		{Running, Finalizing}, {Running, Interrupted}, {Interrupted, Running}, {Cancelling, Cancelled}, {Finalizing, Completed}}
	for _, e := range legal {
		if !CanTransition(e[0], e[1]) {
			t.Fatal(e)
		}
	}
	illegal := [][2]string{{Submitted, Running}, {Completed, Running}, {Failed, Running}, {Accepted, Running},
		{Transferring, Cancelled}, {Interrupted, Transferring}, {Running, Completed}, {Rejected, Accepted}}
	for _, e := range illegal {
		if CanTransition(e[0], e[1]) {
			t.Fatal("illegal edge allowed", e)
		}
	}
	for _, s := range []string{Completed, Failed, Cancelled, Rejected} {
		if !IsTerminal(s) || len(edges[s]) != 0 {
			t.Fatal(s)
		}
	}
}

func TestGlob(t *testing.T) {
	if !MatchGlob("logs/**/*.log", "logs/a/b/x.log") || !MatchGlob("logs/**/*.log", "logs/x.log") ||
		MatchGlob("logs/**/*.log", "logs/x.txt") || !MatchGlob("out/checkpoint*.pt", "out/checkpoint3.pt") ||
		MatchGlob("out/*.pt", "out/sub/a.pt") || !MatchGlob("**", "a/b/c") {
		t.Fatal("glob semantics")
	}
	root := t.TempDir()
	for _, f := range []string{"train.py", "data/a.csv", "data/b.csv", "data/sub/c.csv", "x.kithpart"} {
		p := filepath.Join(root, filepath.FromSlash(f))
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(f), 0o644)
	}
	got, err := ExpandGlobs(root, []string{"train.py", "data/*.csv", "data"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"data/a.csv", "data/b.csv", "data/sub/c.csv", "train.py"}
	if !reflect.DeepEqual(got, want) {
		t.Fatal(got)
	}
	if _, err := ExpandGlobs(root, []string{"missing.py"}); err == nil {
		t.Fatal("missing literal accepted")
	}
}
