//go:build !windows

package ipc

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"time"
)

func endpoint(home string) string { return filepath.Join(home, "ipc.sock") }

func listen(home string) (net.Listener, error) {
	if err := os.MkdirAll(home, 0o700); err != nil {
		return nil, err
	}
	os.Chmod(home, 0o700)
	p := endpoint(home)
	// A stale socket from a crashed daemon is removed only if nobody answers.
	if c, err := net.DialTimeout("unix", p, 300*time.Millisecond); err == nil {
		c.Close()
		return nil, errors.New("another kith daemon is already running for " + home)
	}
	os.Remove(p)
	l, err := net.Listen("unix", p)
	if err != nil {
		return nil, err
	}
	os.Chmod(p, 0o600)
	return l, nil
}

func dial(ctx context.Context, home string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "unix", endpoint(home))
}
