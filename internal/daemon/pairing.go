package daemon

import (
	"context"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"kith/internal/audit"
	"kith/internal/config"
	"kith/internal/discovery"
	"kith/internal/identity"
	"kith/internal/pairing"
	"kith/internal/peer"
	"kith/internal/protocol"
	"kith/internal/store"
)

// pairSession is an open pairing window driven by one `kith pair` client.
type pairSession struct {
	events  chan pairEvent
	confirm chan confirmMsg
	done    chan struct{} // closed when the client's session ends
	dialing atomic.Bool

	mu  sync.Mutex
	cur *ceremony
}

// confirmMsg is the human's answer. Code is the code they compared; a
// confirmation for a code that is no longer on screen is ignored.
type confirmMsg struct {
	ok   bool
	code string
}

// ceremony is one connection being paired over.
type ceremony struct {
	c      *peer.Conn
	dialer string        // fingerprint of the side that dialed c
	stop   chan struct{} // closed when a crossed connection supersedes c
}

func (cr *ceremony) superseded() bool {
	select {
	case <-cr.stop:
		return true
	default:
		return false
	}
}

func (s *pairSession) active() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cur != nil
}

type pairEvent struct {
	Kind string          `json:"kind"` // waiting | code | peer-confirmed | done | error
	Code string          `json:"code,omitempty"`
	Peer *PeerView       `json:"peer,omitempty"`
	Addr string          `json:"addr,omitempty"`
	Err  *protocol.Error `json:"error,omitempty"`
	Msg  string          `json:"message,omitempty"`
}

// ctrlRead is one ReadCtrl result.
type ctrlRead struct {
	env protocol.Envelope
	err error
}

// pairChannel adapts a pre-Run peer.Conn to pairing.Channel. While the human
// compares codes a single read is in flight (so a cancel from the other
// side shows at once); Run's first Recv picks up that read.
type pairChannel struct {
	c       *peer.Conn
	pending chan ctrlRead // the in-flight read, or nil
	stash   *ctrlRead     // a read that completed during the wait
}

func (pc *pairChannel) Send(typ string, v any) error {
	_, err := pc.c.Send(typ, v)
	return err
}

func (pc *pairChannel) Recv() (protocol.Envelope, error) {
	if r := pc.stash; r != nil {
		pc.stash = nil
		return r.env, r.err
	}
	if pc.pending != nil {
		r := <-pc.pending
		pc.pending = nil
		return r.env, r.err
	}
	return pc.c.ReadCtrl()
}

func (pc *pairChannel) readAhead() <-chan ctrlRead {
	ch := make(chan ctrlRead, 1)
	pc.pending = ch
	go func() {
		env, err := pc.c.ReadCtrl()
		ch <- ctrlRead{env, err}
	}()
	return ch
}

func withPort(addr string, def int) string {
	if _, _, err := net.SplitHostPort(addr); err == nil {
		return addr
	}
	return net.JoinHostPort(addr, strconv.Itoa(def))
}

// openPairing creates the session (one at a time).
func (d *Daemon) openPairing() (*pairSession, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.pairSess != nil {
		return nil, protocol.Errf(protocol.CodeBusy, "a pairing ceremony is already in progress")
	}
	s := &pairSession{events: make(chan pairEvent, 8), confirm: make(chan confirmMsg, 1), done: make(chan struct{})}
	d.pairSess = s
	return s, nil
}

// closePairing ends the session; a ceremony still waiting for its human
// tells the other machine that pairing was cancelled here.
func (d *Daemon) closePairing(s *pairSession) {
	d.mu.Lock()
	if d.pairSess == s {
		d.pairSess = nil
	}
	d.mu.Unlock()
	close(s.done)
	d.startAdvertise("available")
}

func (s *pairSession) emit(e pairEvent) {
	select {
	case s.events <- e:
	default:
	}
}

// pairDial initiates pairing to addr (manual IP or discovered).
func (d *Daemon) pairDial(s *pairSession, addr string) {
	if !s.dialing.CompareAndSwap(false, true) {
		return
	}
	defer s.dialing.Store(false)
	// A failed dial ends the session only when nothing else is pairing:
	// when both people choose each other, the other machine's connection
	// may already be carrying the ceremony.
	report := func(e *protocol.Error) {
		if s.active() {
			d.log.Info("pairing dial failed; continuing with the incoming connection", "addr", addr, "err", e.Message)
			return
		}
		s.emit(pairEvent{Kind: "error", Err: e})
	}
	ip := remoteIP(addr)
	if !d.lim.Allowed(ip) {
		d.audit.Write(audit.Event{Event: audit.EvPairRateLimited, Addr: ip})
		report(pairing.RateLimitedError(ip))
		return
	}
	ctx, cancel := context.WithTimeout(d.ctx, 15*time.Second)
	c, err := peer.Dial(ctx, d.id, addr, "", d.hello(true), d.log)
	cancel()
	if err != nil {
		e := protocol.AsError(err)
		if e.Code == protocol.CodeInternal {
			e = protocol.Errf(protocol.CodePeerUnreachable, "cannot reach %s: %v", addr, err).
				WithFix("check the address, that `kith pair` is running there, and run `kith doctor` (firewall / AP isolation)")
		}
		report(e)
		return
	}
	if !c.Hello.Unpaired {
		// The remote is not ready to pair (no pairing window, or we are
		// rate limited there). It follows its hello with a GOAWAY carrying
		// the exact reason; report that before any code is shown.
		report(notReadyReason(c))
		c.Close(nil)
		return
	}
	d.runPairing(s, c, ip, true)
}

// acceptPairing handles an incoming unpaired hello.
func (d *Daemon) acceptPairing(c *peer.Conn, ip string) {
	d.mu.Lock()
	s := d.pairSess
	d.mu.Unlock()
	if !d.lim.Allowed(ip) {
		d.audit.Write(audit.Event{Event: audit.EvPairRateLimited, Addr: ip, PeerFP: c.PeerFP})
		c.GoAway(pairing.RateLimitedError(ip))
		return
	}
	if s == nil {
		d.audit.Write(audit.Event{Event: audit.EvAuthFailed, PeerFP: c.PeerFP, Addr: ip, Detail: "pairing attempt while not in pairing mode"})
		c.GoAway(protocol.Errf(protocol.CodeAuthFailed, "%s is not in pairing mode; run `kith pair` on it", d.cfg.Name))
		return
	}
	d.runPairing(s, c, ip, false)
}

// crossedMsg marks the GOAWAY for the losing one of two crossed pairing
// connections: the other end drops it quietly and keeps waiting for the
// winner, which both ends hold.
const crossedMsg = "superseded by a crossed pairing connection"

func crossedErr() *protocol.Error { return protocol.Errf(protocol.CodeBusy, "%s", crossedMsg) }

func isCrossed(err error) bool {
	e, ok := err.(*protocol.Error)
	return ok && e.Code == protocol.CodeBusy && strings.HasSuffix(e.Message, crossedMsg)
}

// claim makes c the session's ceremony. When both people choose each other,
// two connections between the same machines cross; both ends keep the one
// dialed by the smaller fingerprint, so both show the same code. Any other
// second connection is turned away.
func (d *Daemon) claim(s *pairSession, c *peer.Conn, initiator bool) (cr *ceremony, crossed bool) {
	dialer := c.PeerFP
	if initiator {
		dialer = d.id.FP
	}
	cr = &ceremony{c: c, dialer: dialer, stop: make(chan struct{})}
	s.mu.Lock()
	defer s.mu.Unlock()
	if old := s.cur; old != nil {
		crossed = old.c.PeerFP == c.PeerFP && old.dialer != dialer
		if !crossed || old.dialer < dialer {
			return nil, crossed
		}
		close(old.stop)
		old.c.GoAway(crossedErr())
	}
	s.cur = cr
	// A confirmation queued for the previous code must not approve this one.
	select {
	case <-s.confirm:
	default:
	}
	return cr, false
}

func (s *pairSession) release(cr *ceremony) {
	s.mu.Lock()
	if s.cur == cr {
		s.cur = nil
	}
	s.mu.Unlock()
}

// remoteEnded turns the other machine's GOAWAY (or a dropped connection)
// during the code comparison into the error to show here.
func remoteEnded(err error, name string) *protocol.Error {
	if e, ok := err.(*protocol.Error); ok && strings.HasPrefix(e.Message, "peer closed connection: ") {
		return protocol.Errf(e.Code, "%s", strings.TrimPrefix(e.Message, "peer closed connection: "))
	}
	return protocol.Errf(protocol.CodePeerUnreachable, "%s disconnected during pairing", name).
		WithFix("check that Kith is still running there, then start over on both machines")
}

func (d *Daemon) runPairing(s *pairSession, c *peer.Conn, ip string, initiator bool) {
	cr, crossed := d.claim(s, c, initiator)
	if cr == nil {
		if crossed {
			c.GoAway(crossedErr())
		} else {
			c.GoAway(protocol.Errf(protocol.CodeBusy, "another pairing ceremony is in progress"))
		}
		return
	}
	defer s.release(cr)
	d.audit.Write(audit.Event{Event: audit.EvPairStart, PeerFP: c.PeerFP, Addr: ip})
	// fail ends this ceremony. A superseded ceremony ends quietly: the
	// crossed connection that replaced it carries on.
	fail := func(e *protocol.Error, countFailure bool) {
		if cr.superseded() {
			c.Close(nil)
			return
		}
		if countFailure {
			d.lim.Fail(ip)
		}
		d.audit.Write(audit.Event{Event: audit.EvPairFail, PeerFP: c.PeerFP, Addr: ip, Detail: e.Code + ": " + e.Message})
		c.GoAway(e)
		s.emit(pairEvent{Kind: "error", Err: e})
	}
	c.SetDeadline(time.Now().Add(pairing.CodeValidity + 30*time.Second))
	code, err := pairing.CodeFromState(c.ConnState())
	if err != nil {
		fail(protocol.Errf(protocol.CodeInternal, "derive code: %v", err), false)
		return
	}
	if d.opt.PairCodeHook != nil {
		code = d.opt.PairCodeHook(code)
	}
	name := c.Hello.Name
	if name == "" {
		name = "the other machine"
	}
	pv := &PeerView{FP: c.PeerFP, Display: identity.Display(c.PeerFP), Name: c.Hello.Name}
	s.emit(pairEvent{Kind: "code", Code: code, Peer: pv, Addr: ip})
	ch := &pairChannel{c: c}
	remote := ch.readAhead()
	expire := time.NewTimer(pairing.CodeValidity)
	defer expire.Stop()
wait:
	for {
		select {
		case m := <-s.confirm:
			if m.code != "" && !strings.EqualFold(strings.ReplaceAll(m.code, " ", ""), code) {
				continue // an answer about a code that is no longer shown
			}
			if !m.ok {
				fail(protocol.Errf(protocol.CodeAuthFailed, "pairing rejected by the user on %s: the codes did not match", d.cfg.Name), true)
				return
			}
			break wait
		case r := <-remote:
			remote = nil
			ch.pending = nil
			if r.err != nil {
				if cr.superseded() || isCrossed(r.err) {
					c.Close(nil) // the crossed connection carries on
					return
				}
				e := remoteEnded(r.err, name)
				d.audit.Write(audit.Event{Event: audit.EvPairFail, PeerFP: c.PeerFP, Addr: ip, Detail: e.Code + ": " + e.Message})
				c.Close(nil)
				s.emit(pairEvent{Kind: "error", Err: e})
				return
			}
			// The other side confirmed first; its first message waits for Run.
			ch.stash = &r
			if r.env.Type == protocol.MsgPairBegin {
				s.emit(pairEvent{Kind: "peer-confirmed", Peer: pv})
			}
		case <-cr.stop:
			return
		case <-expire.C:
			fail(protocol.Errf(protocol.CodeTimeout, "the pairing code expired after %s without an answer on %s", pairing.CodeValidity, d.cfg.Name), false)
			return
		case <-s.done:
			fail(protocol.Errf(protocol.CodeCancelledByPeer, "pairing was cancelled on %s", d.cfg.Name), false)
			return
		case <-d.ctx.Done():
			c.Close(nil)
			return
		}
	}
	me := protocol.PairIdentities{Name: d.cfg.Name, PubKey: d.id.Pub, FP: d.id.FP, KithVer: config.KithVersion, Port: d.port}
	them, err := pairing.Run(ch, code, initiator, d.id.FP, c.PeerFP, me)
	if err != nil && isCrossed(err) {
		c.Close(nil)
		return
	}
	if err != nil {
		e := protocol.AsError(err)
		if pe, ok := err.(*protocol.Error); ok && strings.HasPrefix(pe.Message, "peer closed connection: ") {
			e = remoteEnded(err, name)
		} else if e.Code == protocol.CodeInternal {
			e = protocol.Errf(protocol.CodeAuthFailed, "pairing failed: %v", err)
		}
		fail(e, e.Code == protocol.CodeAuthFailed)
		return
	}
	port := them.Port
	if port == 0 {
		port = config.DefaultPort
	}
	p := store.Peer{FP: them.FP, Name: them.Name, Policy: d.cfg.DefaultPolicy, LastAddr: net.JoinHostPort(ip, strconv.Itoa(port)), PubKey: them.PubKey}
	if old, err := d.st.GetPeer(d.ctx, them.FP); err == nil {
		p.Policy = old.Policy
	}
	if err := d.st.UpsertPeer(d.ctx, p); err != nil {
		fail(protocol.AsError(err), false)
		return
	}
	d.audit.Write(audit.Event{Event: audit.EvPairSuccess, PeerFP: them.FP, Addr: p.LastAddr, Detail: "name=" + them.Name})
	d.log.Info("paired", "peer", them.Name, "fp", identity.Display(them.FP), "addr", p.LastAddr)
	// Never upgrade in place: close and reconnect with pinning (§6 step 5).
	time.Sleep(100 * time.Millisecond)
	c.Close(nil)
	d.ensureDialer(them.FP)
	d.wakeDialers()
	pv.Name = them.Name
	pv.Policy = p.Policy
	pv.LastAddr = p.LastAddr
	s.emit(pairEvent{Kind: "done", Peer: pv})
}

// pairFromDiscovery initiates pairing with a peer that is also in pairing
// mode; only the side with the smaller fingerprint dials.
func (d *Daemon) pairFromDiscovery(s *pairSession, es []discovery.Entry) {
	for _, e := range es {
		if e.Mode != "pairing" || e.MatchesFP(d.id.FP) {
			continue
		}
		if identity.NormalizeFP(identity.Display(d.id.FP)) < identity.NormalizeFP(e.FPDisplay) && !s.active() {
			addr := e.Addr
			d.goFn(func() { d.pairDial(s, addr) })
			return
		}
	}
}

// notReadyReason reads the GOAWAY a non-ready responder sends after its
// hello, falling back to a generic explanation.
func notReadyReason(c *peer.Conn) *protocol.Error {
	fallback := protocol.Errf(protocol.CodeAuthFailed, "%s is not in pairing mode", c.Hello.Name).
		WithFix("run `kith pair` on the other machine at the same time")
	c.SetDeadline(time.Now().Add(3 * time.Second))
	_, err := c.ReadCtrl()
	e, ok := err.(*protocol.Error)
	if !ok {
		return fallback
	}
	msg := strings.TrimPrefix(e.Message, "peer closed connection: ")
	switch e.Code {
	case protocol.CodeRateLimited:
		return protocol.Errf(protocol.CodeRateLimited, "%s", msg).WithFix("wait up to an hour before trying again")
	case protocol.CodeAuthFailed:
		return protocol.Errf(protocol.CodeAuthFailed, "%s", msg).WithFix("run `kith pair` on the other machine at the same time")
	}
	return fallback
}
