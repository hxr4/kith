package main

import (
	"os"
	"runtime"
	"strings"
	"time"
)

// isLite reports whether to run in "lite" mode: slower polling and no
// animations, for Raspberry Pi and other low-power boards.
// KITH_GUI_LITE=1/0 overrides the detection.
func isLite() bool {
	switch os.Getenv("KITH_GUI_LITE") {
	case "1", "true":
		return true
	case "0", "false":
		return false
	}
	if runtime.GOOS != "linux" {
		return false
	}
	b, err := os.ReadFile("/proc/device-tree/model")
	if err != nil {
		return false
	}
	return strings.Contains(string(b), "Raspberry Pi")
}

func pollInterval(lite bool) time.Duration {
	if lite {
		return 3 * time.Second
	}
	return 1500 * time.Millisecond
}
