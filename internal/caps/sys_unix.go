//go:build !windows

package caps

import (
	"os/exec"
	"runtime"

	"golang.org/x/sys/unix"
)

func freeDisk(path string) (uint64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, err
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}

func totalRAM() (uint64, error) {
	if runtime.GOOS == "darwin" {
		return unix.SysctlUint64("hw.memsize")
	}
	return linuxRAM()
}

func hideWindow(*exec.Cmd) {}

func windowsName() string { return "windows" }
