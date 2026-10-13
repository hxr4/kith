package main

// Black-box end-to-end tests: two real `kith daemon` processes (this test
// binary re-executed as the kith CLI), distinct --home dirs, real TCP over
// 127.0.0.1, driven through the CLI and IPC. Includes kill -9 crash tests.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"kith/internal/cli"
	"kith/internal/daemon"
	"kith/internal/ipc"
	"kith/internal/task"
	"kith/internal/transfer"
)

func TestMain(m *testing.M) {
	if os.Getenv("KITH_E2E_EXEC") == "1" {
		os.Exit(cli.Main(os.Args[1:]))
	}
	os.Exit(m.Run())
}

type proc struct {
	t    *testing.T
	name string
	home string
	port int
	cmd  *exec.Cmd
	cl   ipc.Client
}

func freePort(t *testing.T) int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func tempDir(t *testing.T, prefix string) string {
	d, err := os.MkdirTemp("", prefix)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for i := 0; i < 30; i++ {
			if os.RemoveAll(d) == nil {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	})
	return d
}

func kithCmd(args ...string) *exec.Cmd {
	c := exec.Command(os.Args[0], args...)
	c.Env = append(os.Environ(), "KITH_E2E_EXEC=1")
	return c
}

func newProc(t *testing.T, name string) *proc {
	home := tempDir(t, "ke"+name[:1])
	port := freePort(t)
	cfg := fmt.Sprintf("port = %d\nname = %q\nbind = \"127.0.0.1\"\nmdns = false\ndefault_policy = \"auto\"\nlog_level = \"debug\"\n", port, name)
	os.WriteFile(filepath.Join(home, "kith.toml"), []byte(cfg), 0o600)
	p := &proc{t: t, name: name, home: home, port: port, cl: ipc.Client{Home: home}}
	p.start()
	t.Cleanup(p.stop)
	return p
}

func (p *proc) start() {
	p.t.Helper()
	p.cmd = kithCmd("daemon", "--home", p.home)
	f, _ := os.OpenFile(filepath.Join(p.home, "daemon.out"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	p.cmd.Stdout, p.cmd.Stderr = f, f
	if err := p.cmd.Start(); err != nil {
		p.t.Fatal(err)
	}
	go func(c *exec.Cmd, f *os.File) { c.Wait(); f.Close() }(p.cmd, f)
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		var st daemon.StatusView
		if p.cl.Call(context.Background(), "status", nil, &st) == nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	b, _ := os.ReadFile(filepath.Join(p.home, "daemon.out"))
	p.t.Fatalf("%s daemon did not start:\n%s", p.name, b)
}

// kill9 terminates the daemon without any chance to clean up
// (SIGKILL on Unix, TerminateProcess == taskkill /F on Windows).
func (p *proc) kill9() {
	p.cmd.Process.Kill()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if p.cl.Call(context.Background(), "status", nil, nil) != nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (p *proc) stop() {
	if p.cmd == nil || p.cmd.Process == nil {
		return
	}
	if runtime.GOOS == "windows" {
		p.cmd.Process.Kill()
	} else {
		p.cmd.Process.Signal(os.Interrupt)
	}
	done := make(chan struct{})
	go func() {
		for p.cl.Call(context.Background(), "status", nil, nil) == nil {
			time.Sleep(50 * time.Millisecond)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		p.cmd.Process.Kill()
	}
}

func (p *proc) cli(args ...string) (string, error) {
	c := kithCmd(append([]string{"--home", p.home}, args...)...)
	var out bytes.Buffer
	c.Stdout, c.Stderr = &out, &out
	err := c.Run()
	return out.String(), err
}

func (p *proc) mustCLI(args ...string) string {
	p.t.Helper()
	out, err := p.cli(args...)
	if err != nil {
		b, _ := os.ReadFile(filepath.Join(p.home, "daemon.out"))
		p.t.Fatalf("%s kith %v: %v\n%s\n--- daemon log tail ---\n%s", p.name, args, err, out, tail(b, 3000))
	}
	return out
}

func tail(b []byte, n int) string {
	if len(b) > n {
		b = b[len(b)-n:]
	}
	return string(b)
}

func (p *proc) taskView(id string) daemon.TaskView {
	var tv daemon.TaskView
	p.cl.Call(context.Background(), "task", map[string]string{"id": id}, &tv)
	return tv
}

func (p *proc) waitState(id string, states ...string) daemon.TaskView {
	p.t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	var tv daemon.TaskView
	for time.Now().Before(deadline) {
		tv = p.taskView(id)
		for _, s := range states {
			if tv.State == s {
				return tv
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	b, _ := os.ReadFile(filepath.Join(p.home, "daemon.out"))
	p.t.Fatalf("%s: %s never reached %v (last %s %q)\n%s", p.name, id, states, tv.State, tv.Detail, tail(b, 4000))
	return tv
}

func pairProcs(t *testing.T, a, b *proc) {
	t.Helper()
	bOut := make(chan string, 1)
	go func() {
		out, err := b.cli("pair", "--yes")
		if err != nil {
			out = "ERR " + err.Error() + "\n" + out
		}
		bOut <- out
	}()
	// Wait until b's pairing window is open (its `kith pair` is running).
	time.Sleep(700 * time.Millisecond)
	outA := a.mustCLI("pair", "--yes", "--ip", fmt.Sprintf("127.0.0.1:%d", b.port))
	ob := <-bOut
	if !strings.Contains(outA, "Paired with "+b.name) || !strings.Contains(ob, "Paired with "+a.name) {
		t.Fatalf("pairing output:\n%s\n---\n%s", outA, ob)
	}
	// Both sides showed the same code.
	codeA, codeB := grabCode(outA), grabCode(ob)
	if codeA == "" || codeA != codeB {
		t.Fatalf("codes differ: %q vs %q", codeA, codeB)
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		out := a.mustCLI("peers")
		if strings.Contains(out, "online") {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("peers never connected")
}

func grabCode(out string) string {
	for _, l := range strings.Split(out, "\n") {
		if i := strings.Index(l, "Pairing code:"); i >= 0 {
			return strings.TrimSpace(l[i+len("Pairing code:"):])
		}
	}
	return ""
}

func python(t *testing.T) string {
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

const trainPy = `import hashlib, json, os, sys, time
epochs = int(sys.argv[1]); delay = float(sys.argv[2])
os.makedirs("out", exist_ok=True)
data = open("dataset.zip", "rb").read()
open("pid.txt", "w").write(str(os.getpid()))
print("attempt start", flush=True)
for e in range(epochs):
    print("epoch %d/%d" % (e + 1, epochs), flush=True)
    time.sleep(delay)
open("out/model.pt", "wb").write(hashlib.sha256(data).digest() * 1000)
json.dump({"epochs": epochs}, open("out/metrics.json", "w"))
`

func writeProject(t *testing.T, py string, epochs int, delay float64, policy string) (dir, id string) {
	dir = tempDir(t, "kprj")
	os.WriteFile(filepath.Join(dir, "train.py"), []byte(trainPy), 0o644)
	data := bytes.Repeat([]byte("mnist!"), 300_000)
	os.WriteFile(filepath.Join(dir, "dataset.zip"), data, 0o644)
	id = task.NewID()
	pyTOML, _ := json.Marshal(py) // JSON string quoting is valid TOML basic-string quoting here
	toml := fmt.Sprintf(`[task]
id = %q
project = "mnist-demo"
name = "train"
command = [%s, "train.py", "%d", "%g"]

[task.inputs]
files = ["train.py", "dataset.zip"]

[task.env]
python = ">=3.6"

[task.outputs]
include = ["out/model.pt", "out/metrics.json"]

[task.policy]
on_interrupt = %q
`, id, string(pyTOML), epochs, delay, policy)
	os.WriteFile(filepath.Join(dir, "task.toml"), []byte(toml), 0o644)
	return dir, id
}

func TestE2ECLIFullFlowBothDirections(t *testing.T) {
	py := python(t)
	mac := newProc(t, "mac")
	win := newProc(t, "win")
	pairProcs(t, mac, win)
	for _, d := range []struct{ req, exe *proc }{{mac, win}, {win, mac}} {
		dir, id := writeProject(t, py, 3, 0.05, "fail")
		out := d.req.mustCLI("submit", "-f", filepath.Join(dir, "task.toml"), "--peer", d.exe.name)
		if !strings.Contains(out, id) {
			t.Fatal(out)
		}
		logs := d.req.mustCLI("logs", id, "-f")
		if !strings.Contains(logs, "epoch 3/3") {
			t.Fatalf("logs -f:\n%s", logs)
		}
		tv := d.req.waitState(id, task.Completed)
		if len(tv.Artifacts) != 2 {
			t.Fatalf("%+v", tv.Artifacts)
		}
		local, _, _ := transfer.SHAFile(filepath.Join(dir, "out", "model.pt"))
		remote := d.exe.taskView(id)
		for _, a := range remote.Artifacts {
			if a.Path == "out/model.pt" && a.SHA256 != local {
				t.Fatal("model.pt hash mismatch")
			}
		}
		// Resubmitting the same file returns the existing record.
		out = d.req.mustCLI("submit", "-f", filepath.Join(dir, "task.toml"), "--peer", d.exe.name)
		if !strings.Contains(out, "already existed") {
			t.Fatal(out)
		}
		for _, args := range [][]string{{"tasks"}, {"task", id}, {"artifacts", id}, {"peers"}, {"id"}, {"probe"}, {"daemon", "status"}} {
			d.req.mustCLI(args...)
		}
	}
	// doctor never fails silently; it always prints a report.
	out, _ := mac.cli("doctor", "--json")
	var checks []map[string]any
	if err := json.Unmarshal([]byte(out), &checks); err != nil || len(checks) < 4 {
		t.Fatalf("doctor output: %v\n%s", err, out)
	}
}

func TestE2EExecutorKill9(t *testing.T) {
	py := python(t)
	for _, pol := range []string{"restart", "fail"} {
		t.Run(pol, func(t *testing.T) {
			mac := newProc(t, "mac")
			win := newProc(t, "win")
			pairProcs(t, mac, win)
			dir, id := writeProject(t, py, 30, 0.1, pol)
			mac.mustCLI("submit", "-f", filepath.Join(dir, "task.toml"), "--peer", "win")
			win.waitState(id, task.Running)
			// Let it make progress, then crash the executor daemon hard.
			deadline := time.Now().Add(20 * time.Second)
			for time.Now().Before(deadline) {
				if b, _ := os.ReadFile(filepath.Join(win.home, "logs", id+".log")); bytes.Contains(b, []byte("epoch 2/")) {
					break
				}
				time.Sleep(50 * time.Millisecond)
			}
			pidB, _ := os.ReadFile(filepath.Join(win.home, "work", id, "pid.txt"))
			var pid int
			fmt.Sscan(string(pidB), &pid)
			win.kill9()
			win.start()
			want := task.Completed
			if pol == "fail" {
				want = task.Failed
			}
			tv := win.waitState(id, want)
			states := []string{}
			for _, tr := range tv.Timeline {
				states = append(states, tr.State)
			}
			joined := strings.Join(states, ",")
			if !strings.Contains(joined, "RUNNING,INTERRUPTED,") {
				t.Fatalf("timeline lacks INTERRUPTED: %s", joined)
			}
			if pol == "restart" && (tv.Attempt != 2 || !strings.HasSuffix(joined, "INTERRUPTED,RUNNING,FINALIZING,COMPLETED")) {
				t.Fatalf("restart: attempt %d timeline %s", tv.Attempt, joined)
			}
			if pol == "fail" && !strings.HasSuffix(joined, "INTERRUPTED,FAILED") {
				t.Fatalf("fail: %s", joined)
			}
			// The first attempt's process did not survive the crash.
			deadline = time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) && task.Alive(pid) {
				time.Sleep(50 * time.Millisecond)
			}
			if task.Alive(pid) {
				t.Fatalf("orphaned task process %d still alive after crash recovery", pid)
			}
			mt := mac.waitState(id, want)
			if pol == "restart" {
				local, _, _ := transfer.SHAFile(filepath.Join(dir, "out", "model.pt"))
				for _, a := range mt.Artifacts {
					if a.Path == "out/model.pt" && a.SHA256 != local {
						t.Fatal("artifact mismatch after crash")
					}
				}
			}
		})
	}
}

// Requester crash: the executor is unaffected and the mirror catches up.
func TestE2ERequesterKill9(t *testing.T) {
	py := python(t)
	mac := newProc(t, "mac")
	win := newProc(t, "win")
	pairProcs(t, mac, win)
	dir, id := writeProject(t, py, 10, 0.1, "fail")
	mac.mustCLI("submit", "-f", filepath.Join(dir, "task.toml"), "--peer", "win")
	win.waitState(id, task.Running)
	mac.kill9()
	win.waitState(id, task.Completed)
	mac.start()
	mac.waitState(id, task.Completed)
	if _, err := os.Stat(filepath.Join(dir, "out", "model.pt")); err != nil {
		t.Fatal("artifact not copied back after requester crash")
	}
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode()
	}
	return -1
}

// `kith run` end to end: inline spec, log following, copy-back, exit codes,
// detach + wait, and rejection before transfer.
func TestE2ECLIRunAndWait(t *testing.T) {
	py := python(t)
	mac := newProc(t, "mac")
	win := newProc(t, "win")
	pairProcs(t, mac, win)
	dir, _ := writeProject(t, py, 2, 0.05, "fail")

	out, err := mac.cli("run", "--peer", "win", "--dir", dir, "-i", "train.py", "-i", "dataset.zip",
		"-o", "out/*", "--python", ">=3.6", "--", py, "-u", "train.py", "2", "0.05")
	if err != nil || !strings.Contains(out, "epoch 2/2") || !strings.Contains(out, "COMPLETED") || !strings.Contains(out, "out/model.pt") {
		t.Fatalf("run: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(dir, "out", "model.pt")); err != nil {
		t.Fatal("output not copied back")
	}

	// Failing command → exit 2.
	out, err = mac.cli("run", "--peer", "win", "--dir", dir, "--", py, "-c", "import sys; print('boom'); sys.exit(9)")
	if exitCode(err) != 2 || !strings.Contains(out, "boom") || !strings.Contains(out, "status 9") {
		t.Fatalf("failing run: exit %d\n%s", exitCode(err), out)
	}

	// Unsatisfiable requirement → rejected before transfer, exit 4 with a fix.
	out, err = mac.cli("run", "--peer", "win", "--dir", dir, "--pkg", "definitely-not-installed>=1.0", "--", py, "-c", "pass")
	if exitCode(err) != 4 || !strings.Contains(out, "NO_MATCH") || !strings.Contains(out, "pip install") {
		t.Fatalf("rejected run: exit %d\n%s", exitCode(err), out)
	}

	// Detach, then wait.
	out, err = mac.cli("run", "-d", "--peer", "win", "--dir", dir, "--", py, "-c", "import time; time.sleep(1); print('later')")
	id := strings.TrimSpace(out)
	if err != nil || !strings.HasPrefix(id, "tsk_") {
		t.Fatalf("detach: %v %q", err, out)
	}
	out, err = mac.cli("wait", id, "--timeout", "60s")
	if err != nil || !strings.Contains(out, "COMPLETED") {
		t.Fatalf("wait: %v\n%s", err, out)
	}
	// wait --timeout on a long task → exit 5; then cancel → wait exits 3.
	out, _ = mac.cli("run", "-d", "--peer", "win", "--dir", dir, "--", py, "-c", "import time; time.sleep(60)")
	long := strings.TrimSpace(out)
	if _, err := mac.cli("wait", long, "--timeout", "1s"); exitCode(err) != 5 {
		t.Fatalf("wait timeout exit %d", exitCode(err))
	}
	mac.mustCLI("cancel", long)
	if _, err := mac.cli("wait", long); exitCode(err) != 3 {
		t.Fatalf("wait after cancel exit %d", exitCode(err))
	}

	// Filters and status.
	if out := mac.mustCLI("tasks", "--state", "FAILED"); !strings.Contains(out, "FAILED") || strings.Contains(out, "COMPLETED") {
		t.Fatalf("tasks --state:\n%s", out)
	}
	if out := mac.mustCLI("status"); !strings.Contains(out, "running") || !strings.Contains(out, "win") {
		t.Fatalf("status:\n%s", out)
	}
}

// Background lifecycle without a service: start, status, stop, logs.
func TestE2EDaemonStartStop(t *testing.T) {
	home := tempDir(t, "kds")
	port := freePort(t)
	os.WriteFile(filepath.Join(home, "kith.toml"), []byte(fmt.Sprintf("port = %d\nname = \"bg\"\nbind = \"127.0.0.1\"\nmdns = false\n", port)), 0o600)
	p := &proc{t: t, name: "bg", home: home}
	t.Cleanup(func() { p.cli("daemon", "stop") })
	out := p.mustCLI("daemon", "start")
	if !strings.Contains(out, "daemon started") {
		t.Fatal(out)
	}
	if out := p.mustCLI("daemon", "start"); !strings.Contains(out, "already running") {
		t.Fatal(out)
	}
	if out := p.mustCLI("daemon", "status"); !strings.Contains(out, "daemon:  running") {
		t.Fatal(out)
	}
	if out := p.mustCLI("daemon", "logs", "-n", "5"); !strings.Contains(out, "kith daemon started") {
		t.Fatalf("logs:\n%s", out)
	}
	if out := p.mustCLI("daemon", "stop"); !strings.Contains(out, "daemon stopped") {
		t.Fatal(out)
	}
	if out := p.mustCLI("status"); !strings.Contains(out, "not running") {
		t.Fatal(out)
	}
}
