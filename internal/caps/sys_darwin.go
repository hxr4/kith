//go:build darwin

package caps

import "golang.org/x/sys/unix"

func totalRAM() (uint64, error) { return unix.SysctlUint64("hw.memsize") }
