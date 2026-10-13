package daemon

import (
	"context"
	"net"
	"strconv"
	"strings"
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
	confirm chan bool
	busy    atomic.Bool
	dialing atomic.Bool
}

type pairEvent struct {
	Kind string          `json:"kind"` // waiting | code | done | error
	Code string          `json:"code,omitempty"`
	Peer *PeerView       `json:"peer,omitempty"`
	Addr string          `json:"addr,omitempty"`
	Err  *protocol.Error `json:"error,omitempty"`
	Msg  string          `json:"message,omitempty"`
}

// connChannel adapts a pre-Run peer.Conn to pairing.Channel.
type connChannel struct{ c *peer.Conn }

func (cc connChannel) Send(typ string, v any) error {
	_, err := cc.c.Send(typ, v)
	return err
}
func (cc connChannel) Recv() (protocol.Envelope, error) { return cc.c.ReadCtrl() }

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
	s := &pairSession{events: make(chan pairEvent, 8), confirm: make(chan bool, 1)}
	d.pairSess = s
	return s, nil
}

func (d *Daemon) closePairing(s *pairSession) {
	d.mu.Lock()
	if d.pairSess == s {
		d.pairSess = nil
	}
	d.mu.Unlock()
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
	ip := remoteIP(addr)
	if !d.lim.Allowed(ip) {
		d.audit.Write(audit.Event{Event: audit.EvPairRateLimited, Addr: ip})
		s.emit(pairEvent{Kind: "error", Err: pairing.RateLimitedError(ip)})
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
		s.emit(pairEvent{Kind: "error", Err: e})
		return
	}
	if !c.Hello.Unpaired {
		// The remote is not ready to pair (no pairing window, or we are
		// rate limited there). It follows its hello with a GOAWAY carrying
		// the exact reason; report that before any code is shown.
		s.emit(pairEvent{Kind: "error", Err: notReadyReason(c)})
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

func (d *Daemon) runPairing(s *pairSession, c *peer.Conn, ip string, initiator bool) {
	if !s.busy.CompareAndSwap(false, true) {
		c.GoAway(protocol.Errf(protocol.CodeBusy, "another pairing ceremony is in progress"))
		return
	}
	defer s.busy.Store(false)
	d.audit.Write(audit.Event{Event: audit.EvPairStart, PeerFP: c.PeerFP, Addr: ip})
	fail := func(e *protocol.Error, countFailure bool) {
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
	pv := &PeerView{FP: c.PeerFP, Display: identity.Display(c.PeerFP), Name: c.Hello.Name}
	s.emit(pairEvent{Kind: "code", Code: code, Peer: pv, Addr: ip})
	select {
	case ok := <-s.confirm:
		if !ok {
			fail(protocol.Errf(protocol.CodeAuthFailed, "pairing rejected by the user"), true)
			return
		}
	case <-time.After(pairing.CodeValidity):
		fail(protocol.Errf(protocol.CodeTimeout, "pairing code expired after %s", pairing.CodeValidity), true)
		return
	case <-d.ctx.Done():
		c.Close(nil)
		return
	}
	me := protocol.PairIdentities{Name: d.cfg.Name, PubKey: d.id.Pub, FP: d.id.FP, KithVer: config.KithVersion, Port: d.port}
	them, err := pairing.Run(connChannel{c}, code, initiator, d.id.FP, c.PeerFP, me)
	if err != nil {
		e := protocol.AsError(err)
		if e.Code == protocol.CodeInternal {
			e = protocol.Errf(protocol.CodeAuthFailed, "pairing failed: %v", err)
		}
		fail(e, true)
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
		if identity.NormalizeFP(identity.Display(d.id.FP)) < identity.NormalizeFP(e.FPDisplay) && !s.busy.Load() {
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
