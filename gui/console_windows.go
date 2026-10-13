//go:build windows

package main

import (
	"os/exec"
	"syscall"
)

// hideConsole keeps child CLI processes from flashing a console window.
func hideConsole(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}
