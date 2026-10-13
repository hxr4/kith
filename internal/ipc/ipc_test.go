package ipc

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"testing"
	"time"

	"kith/internal/protocol"
)

func shortHome(t *testing.T) string {
	d, err := os.MkdirTemp("", "kipc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	return d
}

func TestCallStreamAndErrors(t *testing.T) {
	home := shortHome(t)
	l, err := Listen(home)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		Serve(ctx, l, func(ctx context.Context, req Request, s *Stream) (any, error) {
			switch req.Method {
			case "echo":
				var v map[string]any
				json.Unmarshal(req.Params, &v)
				return v, nil
			case "stream":
				for i := 0; i < 3; i++ {
					s.Send("tick", i)
				}
				var c struct{ OK bool }
				s.Recv(&c)
				return c.OK, nil
			case "panic":
				panic("boom")
			default:
				return nil, protocol.Errf(protocol.CodeNotFound, "no method %s", req.Method)
			}
		}, slog.Default())
		close(done)
	}()
	cl := Client{Home: home}
	var out map[string]any
	if err := cl.Call(ctx, "echo", map[string]any{"a": "b"}, &out); err != nil || out["a"] != "b" {
		t.Fatal(err, out)
	}
	cn, err := cl.Open(ctx, "stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	var ok bool
	cn.Send(map[string]bool{"OK": true})
	if err := cn.Wait(func(ev string, d json.RawMessage) error { n++; return nil }, &ok); err != nil || n != 3 || !ok {
		t.Fatal(err, n, ok)
	}
	cn.Close()
	if err := cl.Call(ctx, "nope", nil, nil); protocol.CodeOf(err) != protocol.CodeNotFound {
		t.Fatal(err)
	}
	if err := cl.Call(ctx, "panic", nil, nil); protocol.CodeOf(err) != protocol.CodeInternal {
		t.Fatal("panic must become INTERNAL, got", err)
	}
	// A second daemon on the same home must refuse to start.
	if _, err := Listen(home); err == nil {
		t.Fatal("second listener allowed")
	}
	cancel()
	l.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not stop")
	}
	if err := cl.Call(context.Background(), "echo", nil, nil); err != ErrDaemonNotRunning {
		t.Fatal(err)
	}
}
