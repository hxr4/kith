//go:build !windows

package caps

import (
	"os/exec"

	"golang.org/x/sys/unix"
)

func freeDisk(path string) (uint64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, err
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}

func hideWindow(*exec.Cmd) {}

func windowsName() string { return "windows" }
