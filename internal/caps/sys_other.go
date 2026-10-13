//go:build !windows && !linux && !darwin

package caps

import "errors"

func totalRAM() (uint64, error) { return 0, errors.New("unsupported") }
