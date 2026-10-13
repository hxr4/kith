package peer

import (
	"context"
	"crypto/tls"
	"log/slog"
	"math/rand"
	"net"
	"time"

	"kith/internal/identity"
	"kith/internal/protocol"
)

// Dial connects to addr. expectFP pins the peer ("" = unpinned, pairing only).
func Dial(ctx context.Context, id *identity.Identity, addr, expectFP string, hello protocol.Hello, log *slog.Logger) (*Conn, error) {
	d := net.Dialer{Timeout: 5 * time.Second, KeepAlive: 15 * time.Second}
	nc, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	raw := tls.Client(nc, ClientTLS(id, expectFP))
	c, err := Handshake(ctx, raw, func(string) protocol.Hello { return hello }, log)
	if err != nil {
		nc.Close()
		return nil, err
	}
	c.Dialed = true
	c.RemoteAddr = addr
	return c, nil
}

// Accept performs the server side of a connection.
func Accept(ctx context.Context, id *identity.Identity, nc net.Conn, hello HelloFunc, log *slog.Logger) (*Conn, error) {
	raw := tls.Server(nc, ServerTLS(id))
	c, err := Handshake(ctx, raw, hello, log)
	if err != nil {
		nc.Close()
		return nil, err
	}
	return c, nil
}

// Backoff returns the reconnect delay for attempt n (0-based): 1s, 2s, 5s,
// 15s, then 60s ± 20% jitter (§7.2).
func Backoff(n int) time.Duration {
	steps := []time.Duration{time.Second, 2 * time.Second, 5 * time.Second, 15 * time.Second}
	if n < len(steps) {
		return steps[n]
	}
	j := time.Duration(rand.Int63n(int64(24 * time.Second)))
	return 48*time.Second + j
}
