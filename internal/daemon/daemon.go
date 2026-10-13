// Package daemon is the long-lived Kith service. It owns all state and
// networking; the CLI and MCP server are thin IPC clients.
package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"kith/internal/audit"
	"kith/internal/caps"
	"kith/internal/config"
	"kith/internal/discovery"
	"kith/internal/identity"
	"kith/internal/ipc"
	"kith/internal/pairing"
	"kith/internal/peer"
	"kith/internal/protocol"
	"kith/internal/store"
	"kith/internal/task"
	"kith/internal/transfer"

	"golang.org/x/sync/errgroup"
)

// Options configure a daemon. Fields other than Home exist for tests and
// embedding; there are no production override flags for capabilities.
type Options struct {
	Home        string
	Caps        caps.Source       // nil → caps.System{}
	Browser     discovery.Browser // nil → mDNS when enabled in config
	ApprovalTTL time.Duration     // default 15m (§6)
	CancelGrace time.Duration     // default 5s (§13)
	TimeoutUnit time.Duration     // length of one timeout_minutes unit (default 1m; tests shorten it)
	Logger      *slog.Logger
	// PairCodeHook lets tests alter the locally derived pairing code to
	// simulate a human typing/confirming a different code.
	PairCodeHook func(code string) string
	// OnFileChunk is a test hook called for every received FILE_CHUNK
	// before it is processed (used to simulate interrupted transfers).
	OnFileChunk func(c *peer.Conn)
}

type Daemon struct {
	opt   Options
	home  string
	cfg   *config.Config
	id    *identity.Identity
	st    *store.Store
	audit *audit.Log
	log   *slog.Logger
	caps  caps.Source
	xfer  *transfer.Manager
	lim   *pairing.Limiter

	ctx    context.Context
	cancel context.CancelFunc
	eg     errgroup.Group // owns every goroutine; shutdown waits on it

	mu         sync.Mutex
	conns      map[string]*peer.Conn // by peer fp
	remoteCaps map[string]caps.Manifest
	dialers    map[string]bool
	discovered map[string]string // display-fp prefix → addr
	running    map[string]*runningTask
	pumps      map[pumpKey]bool
	copying    map[string]bool
	pairSess   *pairSession
	changed    chan struct{} // closed and replaced on every state/log change
	connEv     chan struct{} // closed and replaced on every new connection
	wake       chan struct{} // closed and replaced to cut dial backoffs short
	localCaps  caps.Manifest

	gateMu   sync.Mutex // serialises accept-time gates (one active task)
	mirrorMu sync.Mutex // serialises requester mirror completion decisions

	tcp     net.Listener
	ipcL    net.Listener
	adv     *discovery.Advertiser
	advMode string
	port    int
}

type pumpKey struct {
	c  *peer.Conn
	id string
}

// New prepares a daemon: identity, config, database. Run starts it.
func New(opt Options) (*Daemon, error) {
	home := config.ResolveHome(opt.Home)
	if err := config.EnsureHome(home); err != nil {
		return nil, err
	}
	cfg, err := config.Load(home)
	if err != nil {
		return nil, err
	}
	if opt.Logger == nil {
		opt.Logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.SlogLevel()}))
	}
	if opt.Caps == nil {
		opt.Caps = caps.System{}
	}
	if opt.ApprovalTTL == 0 {
		opt.ApprovalTTL = 15 * time.Minute
	}
	if opt.CancelGrace == 0 {
		opt.CancelGrace = 5 * time.Second
	}
	if opt.TimeoutUnit == 0 {
		opt.TimeoutUnit = time.Minute
	}
	if opt.Browser == nil && cfg.MDNSEnabled() {
		opt.Browser = discovery.MDNS{}
	}
	d := &Daemon{
		opt: opt, home: home, cfg: cfg, log: opt.Logger, caps: opt.Caps,
		xfer: transfer.NewManager(), lim: pairing.NewLimiter(),
		conns: map[string]*peer.Conn{}, remoteCaps: map[string]caps.Manifest{}, dialers: map[string]bool{},
		discovered: map[string]string{}, running: map[string]*runningTask{}, pumps: map[pumpKey]bool{},
		copying: map[string]bool{}, changed: make(chan struct{}), connEv: make(chan struct{}), wake: make(chan struct{}),
		port: cfg.Port,
	}
	// Start sequence (§4): identity → DB.
	if d.id, err = identity.LoadOrCreate(home); err != nil {
		return nil, fmt.Errorf("identity: %w", err)
	}
	if d.st, err = store.Open(home); err != nil {
		return nil, fmt.Errorf("database: %w", err)
	}
	if d.audit, err = audit.Open(home); err != nil {
		d.st.Close()
		return nil, err
	}
	for _, dir := range []string{"work", "logs", "scratch"} {
		os.MkdirAll(filepath.Join(home, dir), 0o700)
	}
	return d, nil
}

// FP returns this daemon's fingerprint.
func (d *Daemon) FP() string { return d.id.FP }

// Home returns the resolved home directory.
func (d *Daemon) Home() string { return d.home }

// Run starts IPC, probing, recovery, discovery and the TCP listener and
// blocks until ctx is cancelled, then shuts down gracefully.
func (d *Daemon) Run(ctx context.Context) error {
	d.ctx, d.cancel = context.WithCancel(ctx)
	defer d.cancel()
	var err error
	// IPC listener.
	if d.ipcL, err = ipc.Listen(d.home); err != nil {
		d.closeStores()
		return fmt.Errorf("ipc: %w", err)
	}
	// Probe capabilities.
	d.refreshLocalCaps(d.ctx)
	d.audit.Write(audit.Event{Event: audit.EvDaemonStart, PeerFP: d.id.FP})
	// Recovery from the journal before accepting any work (§9.4).
	if err := d.recover(d.ctx); err != nil {
		d.log.Error("recovery", "err", err)
	}
	// mDNS advertise + browse, then the TLS listener (§4 order).
	d.port = d.cfg.Port
	d.startAdvertise("available")
	d.tcp, err = net.Listen("tcp", net.JoinHostPort(d.cfg.Bind, strconv.Itoa(d.cfg.Port)))
	if err != nil {
		d.adv.Close()
		d.ipcL.Close()
		d.closeStores()
		return protocol.Errf(protocol.CodeInternal, "listen on TCP %d: %v", d.cfg.Port, err).
			WithFix("is another kith daemon running? change `port` in kith.toml or stop the other process")
	}
	d.log.Info("kith daemon started", "fp", identity.Display(d.id.FP), "name", d.cfg.Name, "port", d.port, "home", d.home)

	d.goFn(func() { ipc.Serve(d.ctx, d.ipcL, d.handleIPC, d.log) })
	d.goFn(d.acceptLoop)
	d.goFn(d.discoveryLoop)
	d.goFn(d.wakeLoop)
	d.goFn(d.expiryLoop)
	peers, _ := d.st.ListPeers(d.ctx)
	for _, p := range peers {
		d.ensureDialer(p.FP)
	}

	<-d.ctx.Done()
	d.shutdown()
	return nil
}

func (d *Daemon) goFn(f func()) {
	d.eg.Go(func() error {
		f()
		return nil
	})
}

func (d *Daemon) closeStores() {
	d.st.Close()
	d.audit.Close()
}

// shutdown: mark running tasks INTERRUPTED, kill their trees, close everything.
func (d *Daemon) shutdown() {
	d.log.Info("shutting down")
	bg := context.Background()
	d.mu.Lock()
	rts := make([]*runningTask, 0, len(d.running))
	for _, rt := range d.running {
		rts = append(rts, rt)
	}
	d.mu.Unlock()
	for _, rt := range rts {
		rt.mu.Lock()
		p := rt.proc
		rt.mu.Unlock()
		if p != nil {
			if _, err := d.execTransition(bg, rt.id, task.Interrupted, "daemon shutdown", nil); err != nil {
				d.log.Debug("shutdown transition", "task", rt.id, "err", err)
			}
			p.Terminate(0)
		}
		rt.cancel()
	}
	if d.tcp != nil {
		d.tcp.Close()
	}
	if d.ipcL != nil {
		d.ipcL.Close()
	}
	d.adv.Close()
	d.mu.Lock()
	for _, c := range d.conns {
		c.Close(errors.New("daemon shutdown"))
	}
	d.mu.Unlock()
	done := make(chan struct{})
	go func() {
		d.eg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		d.log.Warn("shutdown: some goroutines did not stop in time")
	}
	d.audit.Write(audit.Event{Event: audit.EvDaemonStop, PeerFP: d.id.FP})
	d.closeStores()
}

// notify wakes everyone waiting on task/log changes.
func (d *Daemon) notify() {
	d.mu.Lock()
	close(d.changed)
	d.changed = make(chan struct{})
	d.mu.Unlock()
}

func (d *Daemon) changedCh() <-chan struct{} {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.changed
}

func (d *Daemon) wakeDialers() {
	d.mu.Lock()
	close(d.wake)
	d.wake = make(chan struct{})
	d.mu.Unlock()
}

func (d *Daemon) refreshLocalCaps(ctx context.Context) caps.Manifest {
	m, err := d.caps.Probe(ctx, caps.Request{Packages: caps.DefaultPackages, DiskPath: filepath.Join(d.home, "work")})
	if err != nil {
		d.log.Warn("capability probe failed", "err", err)
	}
	if m.KithVer == "" {
		m.KithVer = config.KithVersion
	}
	m.Zstd = true
	n, _ := d.activeCount(ctx, "")
	m.QueueLen = n
	if m.Mode == "" {
		m.Mode = "available"
	}
	if n > 0 {
		m.Mode = "busy"
	}
	d.mu.Lock()
	d.localCaps = m
	d.mu.Unlock()
	return m
}

func (d *Daemon) hello(unpaired bool) protocol.Hello {
	return protocol.Hello{Name: d.cfg.Name, FP: d.id.FP, KithVer: config.KithVersion, Unpaired: unpaired, Port: d.port, Zstd: true}
}

// ---- connections ----

func (d *Daemon) conn(fp string) *peer.Conn {
	d.mu.Lock()
	defer d.mu.Unlock()
	if c := d.conns[fp]; c != nil && c.Alive() {
		return c
	}
	return nil
}

// waitConn blocks until a live connection to fp exists.
func (d *Daemon) waitConn(ctx context.Context, fp string) (*peer.Conn, error) {
	for {
		d.mu.Lock()
		c := d.conns[fp]
		ev := d.connEv
		d.mu.Unlock()
		if c != nil && c.Alive() {
			return c, nil
		}
		select {
		case <-ev:
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// register installs c as the connection for its peer, resolving duplicate
// simultaneous connections deterministically: keep the connection dialed by
// the lexicographically smaller fingerprint.
func (d *Daemon) register(c *peer.Conn) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if old := d.conns[c.PeerFP]; old != nil && old.Alive() {
		dialer := func(x *peer.Conn) string {
			if x.Dialed {
				return d.id.FP
			}
			return x.PeerFP
		}
		keepOld := false
		if old.Dialed != c.Dialed {
			want := d.id.FP
			if c.PeerFP < want {
				want = c.PeerFP
			}
			keepOld = dialer(old) == want
		}
		if keepOld {
			go c.Close(errors.New("duplicate connection"))
			return false
		}
		go old.Close(errors.New("superseded by a newer connection"))
	}
	d.conns[c.PeerFP] = c
	close(d.connEv)
	d.connEv = make(chan struct{})
	return true
}

func (d *Daemon) acceptLoop() {
	for {
		nc, err := d.tcp.Accept()
		if err != nil {
			if d.ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return
			}
			d.log.Warn("accept", "err", err)
			time.Sleep(100 * time.Millisecond)
			continue
		}
		d.goFn(func() { d.handleIncoming(nc) })
	}
}

func remoteIP(addr string) string {
	h, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return h
}

func (d *Daemon) handleIncoming(nc net.Conn) {
	ip := remoteIP(nc.RemoteAddr().String())
	ctx, cancel := context.WithTimeout(d.ctx, 15*time.Second)
	c, err := peer.Accept(ctx, d.id, nc, func(fp string) protocol.Hello {
		// unpaired=true advertises "ready to pair": only while a pairing
		// window is open and this IP is not rate limited. A dialer that
		// wants to pair then fails fast instead of showing a dead code.
		return d.hello(d.pairingReady(ip))
	}, d.log)
	cancel()
	if err != nil {
		d.log.Debug("incoming handshake failed", "from", ip, "err", err)
		return
	}
	if c.Hello.Unpaired {
		d.acceptPairing(c, ip)
		return
	}
	p, err := d.st.GetPeer(d.ctx, c.PeerFP)
	if err != nil {
		d.audit.Write(audit.Event{Event: audit.EvAuthFailed, PeerFP: c.PeerFP, Addr: ip, Detail: "unknown fingerprint"})
		d.log.Warn("rejecting unpaired fingerprint", "from", ip, "fp", identity.Display(c.PeerFP))
		c.GoAway(protocol.Errf(protocol.CodeAuthFailed, "this machine is not paired with %s", identity.Display(c.PeerFP)))
		return
	}
	addr := net.JoinHostPort(ip, strconv.Itoa(c.Hello.Port))
	if c.Hello.Port == 0 {
		addr = p.LastAddr
	}
	d.serveConn(c, addr)
}

// serveConn runs an authenticated, pinned connection until it closes.
func (d *Daemon) serveConn(c *peer.Conn, addr string) {
	if !d.register(c) {
		return
	}
	d.st.TouchPeer(d.ctx, c.PeerFP, addr)
	d.log.Info("peer connected", "peer", identity.Display(c.PeerFP), "addr", addr, "dialed", c.Dialed)
	d.goFn(func() { d.onConnect(c) })
	err := c.Run(d)
	if reason := fmt.Sprint(err); strings.Contains(reason, "duplicate connection") || strings.Contains(reason, "superseded") {
		// Both sides dialing at once after a restart is normal (D-014).
		d.log.Debug("peer connection replaced", "peer", identity.Display(c.PeerFP), "reason", err)
	} else {
		d.log.Info("peer disconnected", "peer", identity.Display(c.PeerFP), "reason", err)
	}
	d.mu.Lock()
	if d.conns[c.PeerFP] == c {
		delete(d.conns, c.PeerFP)
	}
	for k := range d.pumps {
		if k.c == c {
			delete(d.pumps, k)
		}
	}
	d.mu.Unlock()
	d.st.TouchPeer(context.Background(), c.PeerFP, "")
	d.notify()
}

// ensureDialer starts the reconnect loop for a paired peer if not running.
func (d *Daemon) ensureDialer(fp string) {
	d.mu.Lock()
	if d.dialers[fp] {
		d.mu.Unlock()
		return
	}
	d.dialers[fp] = true
	d.mu.Unlock()
	d.goFn(func() {
		defer func() {
			d.mu.Lock()
			delete(d.dialers, fp)
			d.mu.Unlock()
		}()
		d.dialLoop(fp)
	})
}

func (d *Daemon) dialLoop(fp string) {
	attempt := 0
	for d.ctx.Err() == nil {
		p, err := d.st.GetPeer(d.ctx, fp)
		if err != nil {
			return // unpaired
		}
		if c := d.conn(fp); c != nil {
			attempt = 0
			select {
			case <-c.Done():
				continue
			case <-d.ctx.Done():
				return
			}
		}
		addrs := []string{}
		if p.LastAddr != "" {
			addrs = append(addrs, p.LastAddr)
		}
		d.mu.Lock()
		for k, a := range d.discovered {
			if len(k) >= 16 && fp[:16] == k[:16] && a != p.LastAddr {
				addrs = append(addrs, a)
			}
		}
		wake := d.wake
		d.mu.Unlock()
		for _, a := range addrs {
			if d.conn(fp) != nil {
				break
			}
			ctx, cancel := context.WithTimeout(d.ctx, 10*time.Second)
			c, err := peer.Dial(ctx, d.id, a, fp, d.hello(false), d.log)
			cancel()
			if err != nil {
				var im *peer.IdentityMismatchError
				if errors.As(err, &im) {
					d.audit.Write(audit.Event{Event: audit.EvIdentityMismatch, PeerFP: fp, Addr: a, Detail: "presented " + im.Got})
					d.log.Error(im.Error(), "addr", a)
				} else {
					d.log.Debug("dial failed", "peer", identity.Display(fp), "addr", a, "err", err)
				}
				continue
			}
			d.goFn(func() { d.serveConn(c, a) })
			attempt = 0
			// Give serveConn a moment to register before re-checking.
			time.Sleep(200 * time.Millisecond)
			break
		}
		if d.conn(fp) != nil {
			continue
		}
		select {
		case <-time.After(peer.Backoff(attempt)):
		case <-wake:
		case <-d.ctx.Done():
			return
		}
		attempt++
	}
}

// wakeLoop detects sleep/wake via wall-clock jumps and re-probes last_addrs.
func (d *Daemon) wakeLoop() {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	last := time.Now()
	for {
		select {
		case <-d.ctx.Done():
			return
		case now := <-t.C:
			if now.Sub(last) > 30*time.Second {
				d.log.Info("wake detected; re-probing peers")
				d.wakeDialers()
			}
			last = now
		}
	}
}

func (d *Daemon) startAdvertise(mode string) {
	if !d.cfg.MDNSEnabled() {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.advMode == mode && d.adv != nil {
		return
	}
	d.adv.Close()
	d.adv = nil
	a, err := discovery.Advertise(d.cfg.Name, d.id.FP, d.port, mode)
	if err != nil {
		d.log.Debug("mDNS advertise unavailable", "err", err)
		return
	}
	d.adv, d.advMode = a, mode
}

// discoveryLoop browses mDNS while any peer is disconnected (or pairing).
func (d *Daemon) discoveryLoop() {
	if d.opt.Browser == nil {
		return
	}
	for {
		interval := 30 * time.Second
		d.mu.Lock()
		pairing := d.pairSess != nil
		d.mu.Unlock()
		if pairing {
			interval = 3 * time.Second
		}
		need := pairing
		peers, _ := d.st.ListPeers(d.ctx)
		for _, p := range peers {
			if d.conn(p.FP) == nil {
				need = true
			}
		}
		if need {
			es, err := d.opt.Browser.Browse(d.ctx, 2*time.Second)
			if err == nil {
				found := false
				d.mu.Lock()
				for _, e := range es {
					if e.MatchesFP(d.id.FP) {
						continue
					}
					k := identity.NormalizeFP(e.FPDisplay)
					if d.discovered[k] != e.Addr {
						d.discovered[k] = e.Addr
						found = true
					}
				}
				sess := d.pairSess
				d.mu.Unlock()
				if found {
					d.wakeDialers()
				}
				if sess != nil {
					d.pairFromDiscovery(sess, es)
				}
			}
		}
		select {
		case <-d.ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

// activeCount counts executor tasks occupying this machine (excluding id).
func (d *Daemon) activeCount(ctx context.Context, exclude string) (int, error) {
	ts, err := d.st.ListTasks(ctx, store.TaskFilter{Role: store.RoleExecutor, States: task.Active})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, t := range ts {
		if t.ID != exclude {
			n++
		}
	}
	return n, nil
}

func (d *Daemon) logPath(id string) string { return filepath.Join(d.home, "logs", id+".log") }
func (d *Daemon) wsDir(id string) string   { return filepath.Join(d.home, "work", id) }

// auditTask writes a task audit event carrying the full argv (§6).
func (d *Daemon) auditTask(ev string, t store.Task, state, detail string) {
	e := audit.Event{Event: ev, TaskID: t.ID, PeerFP: t.PeerFP, Role: t.Role, State: state, Detail: detail}
	if spec, err := task.ParseJSON([]byte(t.SpecJSON)); err == nil {
		e.Argv = spec.Command
	}
	d.audit.Write(e)
}

// pairingReady reports whether an unpaired hello from ip would be served.
func (d *Daemon) pairingReady(ip string) bool {
	d.mu.Lock()
	open := d.pairSess != nil
	d.mu.Unlock()
	return open && d.lim.Allowed(ip)
}
