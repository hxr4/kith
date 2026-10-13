//go:build !windows

package task

import (
	"os/exec"
	"runtime"
	"syscall"
	"time"
)

type treeHandle struct{ pgid int }

func startInTree(cmd *exec.Cmd) (*treeHandle, error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &treeHandle{pgid: cmd.Process.Pid}, nil
}

func (t *treeHandle) terminate(done <-chan struct{}, grace time.Duration) {
	syscall.Kill(-t.pgid, syscall.SIGTERM)
	select {
	case <-done:
	case <-time.After(grace):
	}
	syscall.Kill(-t.pgid, syscall.SIGKILL)
}

func (t *treeHandle) cleanup() {
	syscall.Kill(-t.pgid, syscall.SIGKILL)
}

// KillOrphan kills the process group of a task left over by a crashed
// daemon (recovery path). Windows needs nothing: the Job Object handle
// closed with the daemon and KILL_ON_JOB_CLOSE took the tree down.
func KillOrphan(pid int) {
	if pid > 1 {
		syscall.Kill(-pid, syscall.SIGKILL)
	}
}

// PreventSleep keeps the machine awake while pid lives: macOS caffeinate;
// Linux systemd-inhibit holding a sleep/idle lock until pid exits.
// Best effort: if the tool is missing or not permitted, nothing happens.
func PreventSleep(pid int) func() {
	var argv []string
	switch runtime.GOOS {
	case "darwin":
		argv = []string{"caffeinate", "-i", "-w", itoa(pid)}
	case "linux":
		argv = []string{"systemd-inhibit", "--what=sleep:idle", "--who=kith", "--why=kith task running", "--mode=block",
			"tail", "--pid=" + itoa(pid), "-f", "/dev/null"}
	default:
		return func() {}
	}
	path, err := exec.LookPath(argv[0])
	if err != nil {
		return func() {}
	}
	c := exec.Command(path, argv[1:]...)
	if err := c.Start(); err != nil {
		return func() {}
	}
	go c.Wait()
	return func() { c.Process.Kill() }
}

// Alive reports whether pid exists (for tests and diagnostics).
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}
