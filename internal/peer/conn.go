package peer

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"kith/internal/protocol"
)

// Heartbeat parameters (§7.2). Variables so tests can shorten them.
var (
	PingInterval = 5 * time.Second
	MissedPings  = 3
)

// Handler receives everything read from a connection. Calls are made
// sequentially from the reader goroutine, so implementations must return
// quickly and preserve ordering; slow work belongs in goroutines.
type Handler interface {
	HandleCtrl(c *Conn, env protocol.Envelope)
	HandleFileChunk(c *Conn, ch protocol.FileChunk)
	HandleLogChunk(c *Conn, ch protocol.LogChunk)
}

// Conn is one authenticated Kith/1 connection.
type Conn struct {
	raw net.Conn
	r   *protocol.Reader
	w   *protocol.Writer
	log *slog.Logger

	PeerFP     string
	RemoteAddr string
	Dialed     bool
	Hello      protocol.Hello // remote hello
	Zstd       bool           // both sides support zstd
	Created    time.Time

	mu      sync.Mutex
	pending map[string]chan protocol.Envelope

	closed    chan struct{}
	closeOnce sync.Once
	closeErr  atomic.Value // error
	lastRecv  atomic.Int64

	// PullMu / PushMu enforce one active transfer per direction (§7.2).
	PullMu sync.Mutex
	PushMu sync.Mutex
}

// NewMsgID returns a random message id.
func NewMsgID() string {
	var b [16]byte
	rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

// HelloFunc builds our hello once the peer's certificate fingerprint is known.
type HelloFunc func(peerFP string) protocol.Hello

// Handshake exchanges hello messages on an established TLS connection.
// It verifies hello.fp matches the certificate and enforces major version.
func Handshake(ctx context.Context, raw *tls.Conn, helloFn HelloFunc, log *slog.Logger) (*Conn, error) {
	if dl, ok := ctx.Deadline(); ok {
		raw.SetDeadline(dl)
	} else {
		raw.SetDeadline(time.Now().Add(30 * time.Second))
	}
	defer raw.SetDeadline(time.Time{})
	if err := raw.HandshakeContext(ctx); err != nil {
		var im *IdentityMismatchError
		if errors.As(err, &im) {
			return nil, im
		}
		return nil, err
	}
	cs := raw.ConnectionState()
	if cs.NegotiatedProtocol != protocol.ALPN {
		raw.Close()
		return nil, protocol.Errf(protocol.CodeProtocolError, "peer did not negotiate ALPN %s", protocol.ALPN)
	}
	fp, _, err := PeerFP(cs)
	if err != nil {
		raw.Close()
		return nil, err
	}
	c := &Conn{
		raw:        raw,
		r:          protocol.NewReader(raw),
		w:          protocol.NewWriter(raw),
		log:        log,
		PeerFP:     fp,
		RemoteAddr: raw.RemoteAddr().String(),
		pending:    map[string]chan protocol.Envelope{},
		closed:     make(chan struct{}),
		Created:    time.Now(),
	}
	mine := helloFn(fp)
	if _, err := c.Send(protocol.MsgHello, mine); err != nil {
		c.Close(err)
		return nil, err
	}
	env, err := c.ReadCtrl()
	if err != nil {
		c.Close(err)
		return nil, err
	}
	if env.Type != protocol.MsgHello {
		err := protocol.Errf(protocol.CodeProtocolError, "first message must be hello, got %q", env.Type)
		c.GoAway(err)
		return nil, err
	}
	var h protocol.Hello
	if err := json.Unmarshal(env.Payload, &h); err != nil {
		e := protocol.Errf(protocol.CodeProtocolError, "bad hello: %v", err)
		c.GoAway(e)
		return nil, e
	}
	if h.FP != fp {
		e := protocol.Errf(protocol.CodeAuthFailed, "hello fingerprint does not match certificate")
		c.GoAway(e)
		return nil, e
	}
	if major(h.KithVer) != major(mine.KithVer) {
		e := protocol.Errf(protocol.CodeVersionMismatch, "peer runs Kith %s, this machine runs Kith %s", h.KithVer, mine.KithVer).
			WithFix("install the same major Kith version on both machines")
		c.GoAway(e)
		return nil, e
	}
	c.Hello = h
	c.Zstd = h.Zstd && mine.Zstd
	c.lastRecv.Store(time.Now().UnixNano())
	return c, nil
}

func major(v string) string {
	for i := 0; i < len(v); i++ {
		if v[i] == '.' {
			return v[:i]
		}
	}
	return v
}

// ConnState exposes the TLS state (for pairing's exporter).
func (c *Conn) ConnState() tls.ConnectionState {
	return c.raw.(*tls.Conn).ConnectionState()
}

// SetDeadline sets an absolute deadline on the underlying conn (pairing).
func (c *Conn) SetDeadline(t time.Time) { c.raw.SetDeadline(t) }

// ReadCtrl synchronously reads the next CTRL message, skipping PINGs. Only
// valid before Run (handshake / pairing phases). GOAWAY becomes an error.
func (c *Conn) ReadCtrl() (protocol.Envelope, error) {
	for {
		f, err := c.r.ReadFrame()
		if err != nil {
			return protocol.Envelope{}, err
		}
		switch f.Type {
		case protocol.FramePing:
			continue
		case protocol.FrameGoAway:
			return protocol.Envelope{}, decodeGoAway(f.Payload)
		case protocol.FrameCtrl:
			return protocol.DecodeCtrl(f.Payload)
		default:
			return protocol.Envelope{}, protocol.Errf(protocol.CodeProtocolError, "unexpected frame 0x%02x", f.Type)
		}
	}
}

func decodeGoAway(b []byte) error {
	var g protocol.GoAway
	json.Unmarshal(b, &g)
	if g.Code == "" {
		g.Code = protocol.CodeProtocolError
	}
	return &protocol.Error{Code: g.Code, Message: "peer closed connection: " + g.Message}
}

// Send writes a CTRL message with a fresh id and returns that id.
func (c *Conn) Send(typ string, payload any) (string, error) {
	id := NewMsgID()
	return id, c.Reply(id, typ, payload)
}

// Reply writes a CTRL message echoing id.
func (c *Conn) Reply(id, typ string, payload any) error {
	b, err := protocol.EncodeCtrl(typ, id, payload)
	if err != nil {
		return err
	}
	return c.SendFrame(protocol.FrameCtrl, b)
}

// SendFrame writes a raw frame.
func (c *Conn) SendFrame(typ byte, parts ...[]byte) error {
	select {
	case <-c.closed:
		return c.Err()
	default:
	}
	if err := c.w.WriteFrame(typ, parts...); err != nil {
		c.Close(err)
		return err
	}
	return nil
}

// Request sends a CTRL message and waits for the reply with the same id.
func (c *Conn) Request(ctx context.Context, typ string, payload any) (protocol.Envelope, error) {
	id := NewMsgID()
	ch := make(chan protocol.Envelope, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()
	if err := c.Reply(id, typ, payload); err != nil {
		return protocol.Envelope{}, err
	}
	select {
	case env := <-ch:
		return env, nil
	case <-c.closed:
		return protocol.Envelope{}, protocol.Errf(protocol.CodePeerUnreachable, "connection lost: %v", c.Err())
	case <-ctx.Done():
		return protocol.Envelope{}, protocol.Errf(protocol.CodeTimeout, "no reply to %s: %v", typ, ctx.Err())
	}
}

// GoAway sends GOAWAY and closes.
func (c *Conn) GoAway(e *protocol.Error) {
	b, _ := json.Marshal(protocol.GoAway{Code: e.Code, Message: e.Message})
	c.w.WriteFrame(protocol.FrameGoAway, b)
	c.Close(e)
}

// Close closes the connection once, recording why.
func (c *Conn) Close(why error) {
	c.closeOnce.Do(func() {
		if why == nil {
			why = errors.New("closed")
		}
		c.closeErr.Store(why)
		close(c.closed)
		c.raw.Close()
	})
}

// Err returns the close reason.
func (c *Conn) Err() error {
	if v := c.closeErr.Load(); v != nil {
		return v.(error)
	}
	return nil
}

// Done is closed when the connection is closed.
func (c *Conn) Done() <-chan struct{} { return c.closed }

// Alive reports whether the connection is open.
func (c *Conn) Alive() bool {
	select {
	case <-c.closed:
		return false
	default:
		return true
	}
}

// Run reads frames and dispatches them until the connection closes. It also
// drives the heartbeat. Returns the close reason.
func (c *Conn) Run(h Handler) error {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		c.heartbeat()
	}()
	err := c.readLoop(h)
	c.Close(err)
	wg.Wait()
	return c.Err()
}

func (c *Conn) heartbeat() {
	t := time.NewTicker(PingInterval)
	defer t.Stop()
	for {
		select {
		case <-c.closed:
			return
		case <-t.C:
			last := time.Unix(0, c.lastRecv.Load())
			if time.Since(last) > time.Duration(MissedPings)*PingInterval {
				c.Close(protocol.Errf(protocol.CodePeerUnreachable, "heartbeat timeout (%d missed pings)", MissedPings))
				return
			}
			var ts [8]byte
			binary.BigEndian.PutUint64(ts[:], uint64(time.Now().UnixNano()))
			c.SendFrame(protocol.FramePing, ts[:])
		}
	}
}

func (c *Conn) readLoop(h Handler) (err error) {
	defer func() {
		if p := recover(); p != nil {
			c.log.Error("peer handler panic", "peer", c.PeerFP[:8], "panic", p)
			err = fmt.Errorf("internal error: %v", p)
		}
	}()
	for {
		f, err := c.r.ReadFrame()
		if err != nil {
			return err
		}
		c.lastRecv.Store(time.Now().UnixNano())
		switch f.Type {
		case protocol.FramePing:
		case protocol.FrameGoAway:
			return decodeGoAway(f.Payload)
		case protocol.FrameCtrl:
			env, err := protocol.DecodeCtrl(f.Payload)
			if err != nil {
				c.GoAway(protocol.AsError(err))
				return err
			}
			h.HandleCtrl(c, env)
			c.mu.Lock()
			ch := c.pending[env.ID]
			c.mu.Unlock()
			if ch != nil {
				select {
				case ch <- env:
				default:
				}
			}
		case protocol.FrameFileChunk:
			ch, err := protocol.DecodeFileChunk(f.Payload)
			if err != nil {
				c.GoAway(protocol.AsError(err))
				return err
			}
			h.HandleFileChunk(c, ch)
		case protocol.FrameLogChunk:
			ch, err := protocol.DecodeLogChunk(f.Payload)
			if err != nil {
				c.GoAway(protocol.AsError(err))
				return err
			}
			h.HandleLogChunk(c, ch)
		default:
			c.log.Debug("ignoring unknown frame type", "type", f.Type)
		}
	}
}
