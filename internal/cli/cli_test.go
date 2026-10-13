package cli

import (
	"bytes"
	"strings"
	"testing"

	"kith/internal/daemon"
	"kith/internal/store"
	"kith/internal/task"
)

func TestVPNNames(t *testing.T) {
	for _, n := range []string{"utun3", "wg0", "tun0", "ppp0", "Tailscale", "WireGuard Tunnel"} {
		if !isVPNName(n) {
			t.Error(n)
		}
	}
	for _, n := range []string{"en0", "eth0", "Wi-Fi", "Ethernet"} {
		if isVPNName(n) {
			t.Error(n)
		}
	}
}

func TestCommandSurface(t *testing.T) {
	a := &app{out: &bytes.Buffer{}, errOut: &bytes.Buffer{}}
	root := a.root()
	want := []string{"daemon", "pair", "peers", "unpair", "id", "probe", "submit", "tasks", "task", "logs", "cancel",
		"artifacts", "fetch", "approve", "deny", "doctor", "mcp"}
	have := map[string]bool{}
	for _, c := range root.Commands() {
		have[strings.Fields(c.Use)[0]] = true
	}
	for _, w := range want {
		if !have[w] {
			t.Errorf("missing command %s", w)
		}
	}
	d, _, _ := root.Find([]string{"daemon", "install"})
	if d == nil || d.Name() != "install" {
		t.Error("daemon install missing")
	}
}

func TestDaemonNotRunningIsReported(t *testing.T) {
	var out, errOut bytes.Buffer
	home := t.TempDir() // never the real ~/.kith, which may have a live daemon
	t.Setenv("KITH_HOME", home)
	a := &app{out: &out, errOut: &errOut}
	root := a.root()
	root.SetArgs([]string{"--home", home, "tasks"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "not running") {
		t.Fatal(err)
	}
}

func TestSplitPkg(t *testing.T) {
	cases := map[string][2]string{
		"torch>=2.3+cu121": {"torch", ">=2.3+cu121"},
		"numpy==1.26":      {"numpy", "==1.26"},
		"torch=>=2.3":      {"torch", ">=2.3"},
		"jax":              {"jax", ""},
		" scipy ~= 1.11 ":  {"scipy", "~= 1.11"},
	}
	for in, want := range cases {
		n, c := splitPkg(in)
		if n != want[0] || c != want[1] {
			t.Errorf("%q → %q %q", in, n, c)
		}
	}
}

func TestBuildSpecFromFlags(t *testing.T) {
	o := runOpts{inputs: []string{"train.py", "data/*.zip"}, outputs: []string{"out/*"}, python: ">=3.10",
		pkgs: []string{"torch>=2.3+cu121"}, gpu: 4, onInterrupt: "restart", timeout: 30, noCopyBack: true}
	raw, err := o.buildSpec([]string{"python", "-u", "train.py"})
	if err != nil {
		t.Fatal(err)
	}
	s, err := task.ParseJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if s.ID == "" || s.Command[2] != "train.py" || s.Inputs.Files[1] != "data/*.zip" || s.Env.Packages["torch"] != ">=2.3+cu121" ||
		!s.Env.GPU.Required || s.Env.GPU.VRAMGB != 4 || s.Policy.OnInterrupt != "restart" || s.Policy.TimeoutMinutes != 30 ||
		*s.Outputs.CopyBack || s.Env.Python != ">=3.10" {
		t.Fatalf("%+v", s)
	}
	if _, err := o.buildSpec(nil); err == nil {
		t.Fatal("empty argv accepted")
	}
	bad := runOpts{inputs: []string{"../secret"}}
	if _, err := bad.buildSpec([]string{"ls"}); err == nil {
		t.Fatal("path escape accepted")
	}
}

func TestFilterTasksAndExitCodes(t *testing.T) {
	ts := []daemon.TaskView{
		{Task: store.Task{ID: "a", Role: "requester", State: "RUNNING", PeerFP: "88ee"}, PeerName: "HARI-PC"},
		{Task: store.Task{ID: "b", Role: "executor", State: "SUBMITTED", PendingApproval: true, PeerFP: "86ae"}, PeerName: "mac"},
		{Task: store.Task{ID: "c", Role: "requester", State: "COMPLETED", PeerFP: "88ee"}, PeerName: "HARI-PC"},
	}
	ids := func(v []daemon.TaskView) string {
		var s []string
		for _, t := range v {
			s = append(s, t.ID)
		}
		return strings.Join(s, ",")
	}
	if got := ids(filterTasks(ts, taskFilter{active: true})); got != "a,b" {
		t.Error("active", got)
	}
	if got := ids(filterTasks(ts, taskFilter{pending: true})); got != "b" {
		t.Error("pending", got)
	}
	if got := ids(filterTasks(ts, taskFilter{peer: "hari-pc", role: "out"})); got != "a,c" {
		t.Error("peer/role", got)
	}
	if got := ids(filterTasks(ts, taskFilter{state: "completed"})); got != "c" {
		t.Error("state", got)
	}
	if exitForState("COMPLETED") != nil || exitForState("FAILED").(exitError).code != ExitFailed ||
		exitForState("REJECTED").(exitError).code != ExitRejected || exitForState("CANCELLED").(exitError).code != ExitCancelled {
		t.Error("exit codes")
	}
}

func TestTableAlignsColoredCells(t *testing.T) {
	var out bytes.Buffer
	a := &app{out: &out, p: painter{on: true}}
	a.table([]string{"A", "B"}, [][]string{{a.p.green("ok"), "x"}, {"longer-cell", "y"}})
	lines := strings.Split(strings.TrimSpace(stripANSI(out.String())), "\n")
	if len(lines) != 3 || strings.Index(lines[1], "x") != strings.Index(lines[2], "y") {
		t.Fatalf("misaligned:\n%s", stripANSI(out.String()))
	}
}

func TestConfigSetGetUnset(t *testing.T) {
	home := t.TempDir()
	t.Setenv("KITH_HOME", home)
	run := func(args ...string) (string, error) {
		var out bytes.Buffer
		a := &app{out: &out, errOut: &out}
		r := a.root()
		r.SetArgs(append([]string{"--home", home}, args...))
		err := r.Execute()
		return out.String(), err
	}
	if _, err := run("config", "set", "default_policy", "AUTO"); err != nil {
		t.Fatal(err)
	}
	if out, _ := run("config", "get", "default_policy"); strings.TrimSpace(out) != "auto" {
		t.Fatal(out)
	}
	if _, err := run("config", "set", "port", "99999"); err == nil {
		t.Fatal("bad port accepted")
	}
	if _, err := run("config", "set", "mdns", "false"); err != nil {
		t.Fatal(err)
	}
	if _, err := run("config", "set", "nonsense", "1"); err == nil {
		t.Fatal("unknown key accepted")
	}
	if out, _ := run("config", "list"); !strings.Contains(out, "kith.toml") || !strings.Contains(out, "mdns") {
		t.Fatal(out)
	}
	run("config", "unset", "default_policy")
	if out, _ := run("config", "get", "default_policy"); strings.TrimSpace(out) != "ask" {
		t.Fatal(out)
	}
}

func TestVersionFlag(t *testing.T) {
	var out bytes.Buffer
	a := &app{out: &out, errOut: &out}
	r := a.root()
	r.SetOut(&out)
	r.SetArgs([]string{"--version"})
	if err := r.Execute(); err != nil || !strings.HasPrefix(out.String(), "kith 1.0.0 (Kith/1)") {
		t.Fatalf("%v %q", err, out.String())
	}
}
