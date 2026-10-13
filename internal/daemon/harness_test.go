package daemon

// Loopback acceptance harness (§16): two real daemons with distinct homes
// talking real TLS over 127.0.0.1. Capability manifests are injected via
// caps.Source; processes, files and sockets are real.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"kith/internal/audit"
	"kith/internal/caps"
	"kith/internal/ipc"
	"kith/internal/peer"
	"kith/internal/store"
	"kith/internal/task"
	"kith/internal/transfer"
)

type fakeCaps struct {
	mu sync.Mutex
	m  caps.Manifest
}

func (f *fakeCaps) Probe(ctx context.Context, req caps.Request) (caps.Manifest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m := f.m
	pk := map[string]string{}
	for k, v := range f.m.Packages {
		pk[k] = v
	}
	m.Packages = pk
	return m, nil
}

func (f *fakeCaps) set(fn func(*caps.Manifest)) {
	f.mu.Lock()
	fn(&f.m)
	f.mu.Unlock()
}

func defaultFakeCaps() *fakeCaps {
	return &fakeCaps{m: caps.Manifest{OS: "testos-1", Arch: caps.NormArch(runtime.GOARCH), Python: []string{"3.11.9"},
		Packages: map[string]string{"numpy": "1.26.4"}, GPUs: []caps.GPU{}, FreeDiskGB: 500, RAMGB: 16, Mode: "available",
		Zstd: true, KithVer: "1.0.0"}}
}

type node struct {
	t      *testing.T
	name   string
	home   string
	port   int
	opt    Options
	d      *Daemon
	cancel context.CancelFunc
	done   chan error
	cl     ipc.Client
	caps   *fakeCaps
}

func freePort(t *testing.T) int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func shortTemp(t *testing.T, prefix string) string {
	d, err := os.MkdirTemp("", prefix)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for i := 0; i < 20; i++ {
			if os.RemoveAll(d) == nil {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	})
	return d
}

func writeConfig(t *testing.T, home, name string, port int, policy string) {
	cfg := fmt.Sprintf("port = %d\nname = %q\nbind = \"127.0.0.1\"\nmdns = false\ndefault_policy = %q\nlog_level = \"debug\"\n", port, name, policy)
	if err := os.WriteFile(filepath.Join(home, "kith.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
}

func testLogger(t *testing.T, name string) *slog.Logger {
	if os.Getenv("KITH_TEST_VERBOSE") != "" {
		return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})).With("node", name)
	}
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newNode(t *testing.T, name string, mod func(*Options)) *node {
	t.Helper()
	home := shortTemp(t, "k"+name[:1])
	port := freePort(t)
	writeConfig(t, home, name, port, "auto")
	fc := defaultFakeCaps()
	opt := Options{Home: home, Caps: fc, Logger: testLogger(t, name), CancelGrace: 2 * time.Second}
	if mod != nil {
		mod(&opt)
	}
	n := &node{t: t, name: name, home: home, port: port, opt: opt, cl: ipc.Client{Home: home}, caps: fc}
	n.start()
	t.Cleanup(n.stop)
	return n
}

func (n *node) start() {
	n.t.Helper()
	d, err := New(n.opt)
	if err != nil {
		n.t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	n.d, n.cancel, n.done = d, cancel, make(chan error, 1)
	go func() { n.done <- d.Run(ctx) }()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var st StatusView
		if n.cl.Call(context.Background(), "status", nil, &st) == nil {
			return
		}
		select {
		case err := <-n.done:
			n.t.Fatalf("%s: daemon exited: %v", n.name, err)
		case <-time.After(20 * time.Millisecond):
		}
	}
	n.t.Fatalf("%s: daemon did not start", n.name)
}

func (n *node) stop() {
	if n.cancel == nil {
		return
	}
	n.cancel()
	select {
	case <-n.done:
	case <-time.After(20 * time.Second):
		n.t.Errorf("%s: daemon did not stop", n.name)
	}
	n.cancel = nil
}

func (n *node) restart() {
	n.stop()
	n.start()
}

func (n *node) call(method string, params, out any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	return n.cl.Call(ctx, method, params, out)
}

func (n *node) mustCall(method string, params, out any) {
	n.t.Helper()
	if err := n.call(method, params, out); err != nil {
		n.t.Fatalf("%s %s: %v", n.name, method, err)
	}
}

// runPair drives one side's `kith pair`, auto-confirming the code.
func runPair(n *node, ip string) (PeerView, error) {
	var pv PeerView
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cn, err := n.cl.Open(ctx, "pair", map[string]string{"ip": ip})
	if err != nil {
		return pv, err
	}
	defer cn.Close()
	err = cn.Wait(func(ev string, data json.RawMessage) error {
		if ev == "code" {
			return cn.Send(map[string]bool{"confirm": true})
		}
		return nil
	}, &pv)
	return pv, err
}

func (n *node) addr() string { return fmt.Sprintf("127.0.0.1:%d", n.port) }

func (n *node) pairingOpen() bool {
	n.d.mu.Lock()
	defer n.d.mu.Unlock()
	return n.d.pairSess != nil
}

// tryPair runs the ceremony (b waits, a dials b) and returns both errors.
func tryPair(t *testing.T, a, b *node) (error, error) {
	bErr := make(chan error, 1)
	go func() {
		_, err := runPair(b, "")
		bErr <- err
	}()
	waitFor(t, 10*time.Second, "pairing window", b.pairingOpen)
	_, errA := runPair(a, b.addr())
	var errB error
	select {
	case errB = <-bErr:
	case <-time.After(30 * time.Second):
		t.Fatal("responder pairing did not finish")
	}
	waitFor(t, 10*time.Second, "pairing window closed", func() bool { return !b.pairingOpen() })
	return errA, errB
}

func pairNodes(t *testing.T, a, b *node) {
	t.Helper()
	errA, errB := tryPair(t, a, b)
	if errA != nil || errB != nil {
		t.Fatalf("pairing failed: %v / %v", errA, errB)
	}
	waitConnected(t, a, b)
}

func waitConnected(t *testing.T, a, b *node) {
	t.Helper()
	waitFor(t, 20*time.Second, "peers connected", func() bool {
		return a.d.conn(b.d.FP()) != nil && b.d.conn(a.d.FP()) != nil
	})
}

func waitFor(t *testing.T, d time.Duration, what string, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if f() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func (n *node) task(id string) store.Task {
	t, err := n.d.st.GetTask(context.Background(), id)
	if err != nil {
		return store.Task{}
	}
	return t
}

func waitState(t *testing.T, n *node, id string, states ...string) store.Task {
	t.Helper()
	var last store.Task
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		last = n.task(id)
		for _, s := range states {
			if last.State == s {
				return last
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	b, _ := os.ReadFile(n.d.logPath(id))
	t.Fatalf("%s: task %s never reached %v (last %s %q)\nlog:\n%s", n.name, id, states, last.State, last.Detail, b)
	return last
}

func pythonPath(t *testing.T) string {
	names := []string{"python3", "python"}
	if runtime.GOOS == "windows" {
		names = []string{"python", "python3", "py"}
	}
	for _, n := range names {
		if p, err := exec.LookPath(n); err == nil && exec.Command(p, "-c", "import sys").Run() == nil {
			return p // runs for real (rejects Windows Store alias stubs)
		}
	}
	t.Skip("python not found; the training-task tests need Python 3")
	return ""
}

// trainScript is the "training task": prints epoch lines, sleeps, writes
// out/model.pt and out/metrics.json. Supports checkpoints and grandchildren.
const trainScript = `import hashlib, json, os, subprocess, sys, time
epochs = int(sys.argv[1]); delay = float(sys.argv[2])
mode = sys.argv[3] if len(sys.argv) > 3 else ""
assert os.environ.get("KITH_TASK_ID", "").startswith("tsk_")
os.makedirs("out", exist_ok=True)
data = open(os.path.join("data", "dataset.bin"), "rb").read()
start = 0
if os.path.exists("out/checkpoint.pt"):
    start = int(open("out/checkpoint.pt").read())
    print("resumed from checkpoint at epoch %d" % start, flush=True)
if mode == "grandchild":
    child = subprocess.Popen([sys.executable, "-c", "import time\nwhile True: time.sleep(0.2)"])
    open("pids.txt", "w").write("%d %d" % (os.getpid(), child.pid))
open("pid.txt", "w").write(str(os.getpid()))
for e in range(start, epochs):
    print("epoch %d/%d loss=%.4f" % (e + 1, epochs, 1.0 / (e + 1)), flush=True)
    time.sleep(delay)
    if mode == "checkpoint":
        open("out/checkpoint.pt", "w").write(str(e + 1))
if mode == "fail":
    sys.exit(7)
open("out/model.pt", "wb").write(hashlib.sha256(data).digest() * 4096)
json.dump({"epochs": epochs, "loss": 1.0 / epochs}, open("out/metrics.json", "w"))
print("done", flush=True)
`

type project struct {
	dir     string
	dataSHA string
}

func newProject(t *testing.T, dataSize int) project {
	dir := shortTemp(t, "kp")
	os.MkdirAll(filepath.Join(dir, "data"), 0o755)
	os.WriteFile(filepath.Join(dir, "train.py"), []byte(trainScript), 0o644)
	b := make([]byte, dataSize)
	for i := range b {
		b[i] = byte(i*7 + i/1024)
	}
	os.WriteFile(filepath.Join(dir, "data", "dataset.bin"), b, 0o644)
	sha, _, _ := transfer.SHAFile(filepath.Join(dir, "data", "dataset.bin"))
	return project{dir: dir, dataSHA: sha}
}

type specOpt func(map[string]any)

func makeSpec(py string, epochs int, delay float64, mode string, opts ...specOpt) map[string]any {
	cmd := []string{py, "train.py", fmt.Sprint(epochs), fmt.Sprint(delay)}
	if mode != "" {
		cmd = append(cmd, mode)
	}
	s := map[string]any{
		"id":      task.NewID(),
		"project": "mnist-demo",
		"name":    "train",
		"command": cmd,
		"inputs":  map[string]any{"files": []string{"train.py", "data/*.bin"}},
		"outputs": map[string]any{"include": []string{"out/model.pt", "out/metrics.json", "logs/**/*.log"}},
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

func withEnv(env map[string]any) specOpt { return func(s map[string]any) { s["env"] = env } }
func withPolicy(p map[string]any) specOpt {
	return func(s map[string]any) { s["policy"] = p }
}
func withOutputs(o map[string]any) specOpt { return func(s map[string]any) { s["outputs"] = o } }

func submit(t *testing.T, req, exe *node, p project, spec map[string]any) SubmitResult {
	t.Helper()
	var r SubmitResult
	req.mustCall("submit", map[string]any{"peer": exe.name, "spec": spec, "project_dir": p.dir}, &r)
	return r
}

func shaOf(t *testing.T, path string) string {
	t.Helper()
	s, _, err := transfer.SHAFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func auditEvents(t *testing.T, n *node) []audit.Event {
	ev, err := audit.ReadAll(n.home)
	if err != nil {
		t.Fatal(err)
	}
	return ev
}

func closeConn(n *node, fp string) {
	if c := n.d.conn(fp); c != nil {
		c.Close(fmt.Errorf("test partition"))
	}
}

type caps_Manifest = caps.Manifest

func storeFilterAll() store.TaskFilter { return store.TaskFilter{} }

var _ = peer.Backoff
var _ = strings.Contains
