//go:build !windows

package cli

import (
	"os/exec"
	"syscall"
)

// detach starts the child in its own session so it outlives the terminal.
func detach(c *exec.Cmd) { c.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
