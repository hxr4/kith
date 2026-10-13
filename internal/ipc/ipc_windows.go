//go:build windows

package ipc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"path/filepath"
	"strings"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"

	"kith/internal/config"
)

// endpoint is \\.\pipe\kith-ipc for the default home and a home-specific
// pipe otherwise, so loopback tests can run two daemons (DECISIONS.md D-031).
func endpoint(home string) string {
	std, _ := filepath.Abs(config.StandardHome())
	h, _ := filepath.Abs(home)
	if strings.EqualFold(filepath.Clean(std), filepath.Clean(h)) {
		return `\\.\pipe\kith-ipc`
	}
	sum := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(h))))
	return `\\.\pipe\kith-ipc-` + hex.EncodeToString(sum[:6])
}

func listen(home string) (net.Listener, error) {
	sddl := "D:P(A;;GA;;;SY)(A;;GA;;;BA)"
	if tok := windows.GetCurrentProcessToken(); tok != 0 {
		if u, err := tok.GetTokenUser(); err == nil {
			sddl = "D:P(A;;GA;;;SY)(A;;GA;;;" + u.User.Sid.String() + ")"
		}
	}
	return winio.ListenPipe(endpoint(home), &winio.PipeConfig{SecurityDescriptor: sddl, InputBufferSize: 64 << 10, OutputBufferSize: 64 << 10})
}

func dial(ctx context.Context, home string) (net.Conn, error) {
	return winio.DialPipeContext(ctx, endpoint(home))
}
