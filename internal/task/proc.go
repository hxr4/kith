package task

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Proc is a running task process tree.
type Proc struct {
	cmd     *exec.Cmd
	PID     int
	tree    *treeHandle // process group (unix) / Job Object (windows)
	waitErr error
	done    chan struct{}
	release func() // sleep prevention
}

// ResolveArgv0 resolves argv[0]: a relative path with a separator is taken
// relative to the workspace; a bare name is looked up on PATH only.
func ResolveArgv0(argv0, ws string) (string, error) {
	if strings.ContainsAny(argv0, `/\`) {
		if !filepath.IsAbs(argv0) {
			argv0 = filepath.Join(ws, argv0)
		}
		if _, err := os.Stat(argv0); err != nil {
			return "", err
		}
		return argv0, nil
	}
	return exec.LookPath(argv0)
}

// Start launches argv (never via a shell) in dir with stdout+stderr going
// to logFile. The child and all descendants are placed in one killable unit.
func Start(argv []string, dir string, env []string, logFile *os.File) (*Proc, error) {
	if len(argv) == 0 {
		return nil, errors.New("empty argv")
	}
	path, err := ResolveArgv0(argv[0], dir)
	if err != nil {
		return nil, err
	}
	cmd := &exec.Cmd{Path: path, Args: argv, Dir: dir, Env: env, Stdout: logFile, Stderr: logFile}
	tree, err := startInTree(cmd)
	if err != nil {
		return nil, err
	}
	p := &Proc{cmd: cmd, PID: cmd.Process.Pid, tree: tree, done: make(chan struct{})}
	p.release = PreventSleep(p.PID)
	go func() {
		p.waitErr = cmd.Wait()
		p.release()
		close(p.done)
	}()
	return p, nil
}

// Done is closed when the main process exits.
func (p *Proc) Done() <-chan struct{} { return p.done }

// Err is the exit error (valid after Done).
func (p *Proc) Err() error { return p.waitErr }

// ExitCode returns the exit code (valid after Done).
func (p *Proc) ExitCode() int {
	if p.cmd.ProcessState == nil {
		return -1
	}
	return p.cmd.ProcessState.ExitCode()
}

// Terminate stops the whole tree: unix SIGTERM to the group, grace, then
// SIGKILL; Windows TerminateJobObject immediately.
func (p *Proc) Terminate(grace time.Duration) {
	p.tree.terminate(p.done, grace)
}

// Cleanup kills any descendants left behind after the main process exited
// and releases OS handles.
func (p *Proc) Cleanup() { p.tree.cleanup() }
