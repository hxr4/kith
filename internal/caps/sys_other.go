//go:build !windows && !linux

package caps

import "errors"

func linuxRAM() (uint64, error) { return 0, errors.New("unsupported") }
