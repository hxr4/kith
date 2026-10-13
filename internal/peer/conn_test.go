package peer

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"

	"kith/internal/identity"
	"kith/internal/protocol"
)

type echoHandler struct {
	mu   sync.Mutex
	logs []protocol.LogChunk
}

func (h *echoHandler) HandleCtrl(c *Conn, env protocol.Envelope) {
	if env.Type == "ping.req" {
		c.Reply(env.ID, "ping.resp", map[string]string{"ok": "yes"})
	}
}
func (h *echoHandler) HandleFileChunk(c *Conn, ch protocol.FileChunk) {}
func (h *echoHandler) HandleLogChunk(c *Conn, ch protocol.LogChunk) {
	h.mu.Lock()
	h.logs = append(h.logs, ch)
	h.mu.Unlock()
}

func hello(id *identity.Identity) protocol.Hello {
	return protocol.Hello{Name: "x", FP: id.FP, KithVer: "1.0.0", Zstd: true}
}

func serve(t *testing.T, srv *identity.Identity, h Handler) (string, chan *Conn) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	conns := make(chan *Conn, 4)
	go func() {
		for {
			nc, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				c, err := Accept(context.Background(), srv, nc, func(string) protocol.Hello { return hello(srv) }, slog.Default())
				if err != nil {
					return
				}
				conns <- c
				c.Run(h)
			}()
		}
	}()
	return l.Addr().String(), conns
}

func TestPinnedRequestResponse(t *testing.T) {
	a, _ := identity.LoadOrCreate(t.TempDir())
	b, _ := identity.LoadOrCreate(t.TempDir())
	addr, conns := serve(t, b, &echoHandler{})
	c, err := Dial(context.Background(), a, addr, b.FP, hello(a), slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(nil)
	srvSide := <-conns
	if srvSide.PeerFP != a.FP || c.PeerFP != b.FP || !c.Zstd {
		t.Fatal("fingerprints")
	}
	go c.Run(&echoHandler{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	env, err := c.Request(ctx, "ping.req", nil)
	if err != nil || env.Type != "ping.resp" {
		t.Fatal(err, env)
	}
	var m map[string]string
	json.Unmarshal(env.Payload, &m)
	if m["ok"] != "yes" {
		t.Fatal(m)
	}
	// Unknown types are ignored, the connection stays up.
	c.Send("future.unknown", map[string]int{"x": 1})
	if _, err := c.Request(ctx, "ping.req", nil); err != nil {
		t.Fatal(err)
	}
}

func TestIdentityMismatch(t *testing.T) {
	a, _ := identity.LoadOrCreate(t.TempDir())
	b, _ := identity.LoadOrCreate(t.TempDir())
	imposter, _ := identity.LoadOrCreate(t.TempDir())
	addr, _ := serve(t, imposter, &echoHandler{})
	_, err := Dial(context.Background(), a, addr, b.FP, hello(a), slog.Default())
	var im *IdentityMismatchError
	if !errors.As(err, &im) || im.Got != imposter.FP {
		t.Fatalf("expected identity mismatch, got %v", err)
	}
}

func TestHeartbeatTimeout(t *testing.T) {
	old := PingInterval
	PingInterval = 50 * time.Millisecond
	defer func() { PingInterval = old }()
	a, _ := identity.LoadOrCreate(t.TempDir())
	b, _ := identity.LoadOrCreate(t.TempDir())
	// Server that completes the handshake and then goes silent.
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	defer l.Close()
	go func() {
		nc, err := l.Accept()
		if err != nil {
			return
		}
		Accept(context.Background(), b, nc, func(string) protocol.Hello { return hello(b) }, slog.Default())
		time.Sleep(2 * time.Second)
		nc.Close()
	}()
	c, err := Dial(context.Background(), a, l.Addr().String(), b.FP, hello(a), slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- c.Run(&echoHandler{}) }()
	select {
	case err := <-done:
		if protocol.CodeOf(err) != protocol.CodePeerUnreachable {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("heartbeat did not close the connection")
	}
}

func TestVersionMismatchGoAway(t *testing.T) {
	a, _ := identity.LoadOrCreate(t.TempDir())
	b, _ := identity.LoadOrCreate(t.TempDir())
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	defer l.Close()
	go func() {
		nc, _ := l.Accept()
		h := hello(b)
		h.KithVer = "2.0.0"
		Accept(context.Background(), b, nc, func(string) protocol.Hello { return h }, slog.Default())
	}()
	_, err := Dial(context.Background(), a, l.Addr().String(), b.FP, hello(a), slog.Default())
	if protocol.CodeOf(err) != protocol.CodeVersionMismatch {
		t.Fatal(err)
	}
}

func TestBackoff(t *testing.T) {
	want := []time.Duration{time.Second, 2 * time.Second, 5 * time.Second, 15 * time.Second}
	for i, w := range want {
		if Backoff(i) != w {
			t.Fatal(i)
		}
	}
	for i := 0; i < 50; i++ {
		d := Backoff(10)
		if d < 48*time.Second || d > 72*time.Second {
			t.Fatal(d)
		}
	}
}
