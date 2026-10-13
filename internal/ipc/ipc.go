// Package ipc is the local control channel between the daemon and its thin
// clients (CLI, MCP). One request per connection, newline-delimited JSON.
// The server may push any number of event lines before the final
// result/error line; the client may send further lines (e.g. pairing
// confirmation) on the same connection.
package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"

	"kith/internal/protocol"
)

type Request struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

// Message is one server → client line.
type Message struct {
	Event  string          `json:"event,omitempty"`
	Data   json.RawMessage `json:"data,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *protocol.Error `json:"error,omitempty"`
	Final  bool            `json:"final,omitempty"`
}

// Stream lets a handler push events and read client lines.
type Stream struct {
	mu  sync.Mutex
	enc *json.Encoder
	dec *json.Decoder
}

// Send pushes an event line to the client.
func (s *Stream) Send(event string, data any) error {
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.enc.Encode(Message{Event: event, Data: b})
}

// Recv reads the next client line into v.
func (s *Stream) Recv(v any) error { return s.dec.Decode(v) }

// Handler serves one request. Returning an error sends it as the final line.
type Handler func(ctx context.Context, req Request, s *Stream) (any, error)

// Serve accepts connections until l is closed. Each connection runs in its
// own goroutine tracked by wg. Panics are converted into INTERNAL errors.
func Serve(ctx context.Context, l net.Listener, h Handler, log *slog.Logger) error {
	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		c, err := l.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			serveConn(ctx, c, h, log)
		}()
	}
}

func serveConn(ctx context.Context, c net.Conn, h Handler, log *slog.Logger) {
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer c.Close()
	// Close the conn when the daemon shuts down so blocked reads end.
	go func() {
		<-cctx.Done()
		c.SetDeadline(time.Now())
	}()
	r := bufio.NewReaderSize(c, 64<<10)
	s := &Stream{enc: json.NewEncoder(c), dec: json.NewDecoder(r)}
	var req Request
	if err := s.dec.Decode(&req); err != nil {
		return
	}
	var res any
	var herr error
	func() {
		defer func() {
			if p := recover(); p != nil {
				log.Error("ipc handler panic", "method", req.Method, "panic", p)
				herr = protocol.Errf(protocol.CodeInternal, "internal error in %s", req.Method)
			}
		}()
		res, herr = h(cctx, req, s)
	}()
	msg := Message{Final: true}
	if herr != nil {
		msg.Error = protocol.AsError(herr)
	} else {
		b, err := json.Marshal(res)
		if err != nil {
			msg.Error = protocol.AsError(err)
		} else {
			msg.Result = b
		}
	}
	s.mu.Lock()
	s.enc.Encode(msg)
	s.mu.Unlock()
}

// ErrDaemonNotRunning is returned when the daemon socket is not reachable.
var ErrDaemonNotRunning = &protocol.Error{Code: protocol.CodePeerUnreachable,
	Message:     "the kith daemon is not running",
	Remediation: "start it with `kith daemon` (or `kith daemon install` for autostart)"}

// Client talks to a daemon at a given home.
type Client struct {
	Home string
}

// Conn is an open streaming call.
type Conn struct {
	c   net.Conn
	enc *json.Encoder
	dec *json.Decoder
}

// Open starts a call and returns the connection for streaming use.
func (cl Client) Open(ctx context.Context, method string, params any) (*Conn, error) {
	c, err := dial(ctx, cl.Home)
	if err != nil {
		return nil, ErrDaemonNotRunning
	}
	var raw json.RawMessage
	if params != nil {
		if raw, err = json.Marshal(params); err != nil {
			c.Close()
			return nil, err
		}
	}
	cn := &Conn{c: c, enc: json.NewEncoder(c), dec: json.NewDecoder(bufio.NewReader(c))}
	if err := cn.enc.Encode(Request{Method: method, Params: raw}); err != nil {
		c.Close()
		return nil, err
	}
	go func() {
		<-ctx.Done()
		c.SetDeadline(time.Now())
	}()
	return cn, nil
}

// Send writes a client line (e.g. a confirmation).
func (cn *Conn) Send(v any) error { return cn.enc.Encode(v) }

// Next reads the next server line.
func (cn *Conn) Next() (Message, error) {
	var m Message
	err := cn.dec.Decode(&m)
	return m, err
}

func (cn *Conn) Close() error { return cn.c.Close() }

// Wait reads lines until the final one, invoking onEvent for events, and
// decodes the result into out.
func (cn *Conn) Wait(onEvent func(event string, data json.RawMessage) error, out any) error {
	for {
		m, err := cn.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return protocol.Errf(protocol.CodeInternal, "daemon closed the connection")
			}
			return err
		}
		if m.Final {
			if m.Error != nil {
				return m.Error
			}
			if out != nil && len(m.Result) > 0 {
				return json.Unmarshal(m.Result, out)
			}
			return nil
		}
		if onEvent != nil {
			if err := onEvent(m.Event, m.Data); err != nil {
				return err
			}
		}
	}
}

// Call performs a simple request/response.
func (cl Client) Call(ctx context.Context, method string, params, out any) error {
	cn, err := cl.Open(ctx, method, params)
	if err != nil {
		return err
	}
	defer cn.Close()
	return cn.Wait(nil, out)
}

// Stream performs a call that emits events.
func (cl Client) Stream(ctx context.Context, method string, params any, onEvent func(string, json.RawMessage) error, out any) error {
	cn, err := cl.Open(ctx, method, params)
	if err != nil {
		return err
	}
	defer cn.Close()
	return cn.Wait(onEvent, out)
}

// Listen opens the platform IPC listener for home.
func Listen(home string) (net.Listener, error) { return listen(home) }

// Endpoint describes the IPC address for home (for diagnostics).
func Endpoint(home string) string { return endpoint(home) }
