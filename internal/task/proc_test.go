package task

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

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

const grandchildScript = `
import subprocess, sys, time, os
child = subprocess.Popen([sys.executable, "-c", "import time\nwhile True: time.sleep(0.2)"])
open("pids.txt", "w").write("%d %d" % (os.getpid(), child.pid))
print("started", flush=True)
while True:
    time.sleep(0.2)
`

func readPids(t *testing.T, ws string) (int, int) {
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		b, err := os.ReadFile(filepath.Join(ws, "pids.txt"))
		if err == nil {
			f := strings.Fields(string(b))
			if len(f) == 2 {
				a, _ := strconv.Atoi(f[0])
				c, _ := strconv.Atoi(f[1])
				return a, c
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("pids never written")
	return 0, 0
}

func waitDead(t *testing.T, pids ...int) {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		alive := false
		for _, p := range pids {
			if Alive(p) {
				alive = true
			}
		}
		if !alive {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("processes still alive: %v", pids)
}

func TestTerminateKillsWholeTree(t *testing.T) {
	py := python(t)
	ws := t.TempDir()
	os.WriteFile(filepath.Join(ws, "s.py"), []byte(grandchildScript), 0o644)
	log, _ := os.Create(filepath.Join(ws, "log.txt"))
	defer log.Close()
	p, err := Start([]string{py, "s.py"}, ws, os.Environ(), log)
	if err != nil {
		t.Fatal(err)
	}
	parent, child := readPids(t, ws)
	if !Alive(parent) || !Alive(child) {
		t.Fatal("tree not running")
	}
	p.Terminate(2 * time.Second)
	<-p.Done()
	p.Cleanup()
	waitDead(t, parent, child)
}

func TestExitCodeAndLog(t *testing.T) {
	py := python(t)
	ws := t.TempDir()
	lp := filepath.Join(ws, "log.txt")
	log, _ := os.Create(lp)
	p, err := Start([]string{py, "-c", "import sys; print('epoch 1'); sys.stderr.write('warn\\n'); sys.exit(3)"}, ws, os.Environ(), log)
	if err != nil {
		t.Fatal(err)
	}
	<-p.Done()
	p.Cleanup()
	log.Close()
	if p.ExitCode() != 3 {
		t.Fatal(p.ExitCode())
	}
	b, _ := os.ReadFile(lp)
	if !strings.Contains(string(b), "epoch 1") || !strings.Contains(string(b), "warn") {
		t.Fatalf("log %q", b)
	}
}

func TestResolveArgv0(t *testing.T) {
	if _, err := ResolveArgv0("definitely-not-on-path-kith", t.TempDir()); err == nil {
		t.Fatal("expected error")
	}
	ws := t.TempDir()
	os.WriteFile(filepath.Join(ws, "run.sh"), []byte("x"), 0o755)
	p, err := ResolveArgv0("./run.sh", ws)
	if err != nil || p != filepath.Join(ws, "run.sh") {
		t.Fatal(p, err)
	}
}
