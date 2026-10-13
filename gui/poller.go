package main

import (
	"context"
	"encoding/json"
	"runtime"
	"sync"
	"time"

	"kith/internal/config"
	"kith/internal/ipc"
	"kith/internal/svc"
)

// Caller is the subset of ipc.Client the poller needs (fakes in tests).
type Caller interface {
	Call(ctx context.Context, method string, params, out any) error
}

// Sink receives poll results: the UI event, the tray and notifications.
type Sink interface {
	State(Snapshot)
	Tray(Snapshot, []peerLite)
	Notify(Notice)
}

// Poller periodically reads daemon state over IPC.
type Poller struct {
	cl       Caller
	sink     Sink
	lite     bool
	interval time.Duration
	home     string

	mu    sync.Mutex
	last  Snapshot
	prev  []taskLite // nil until the first successful poll (baseline)
	kick  chan struct{}
	svcSt string
	svcAt time.Time
}

func NewPoller(cl Caller, sink Sink, lite bool) *Poller {
	return &Poller{cl: cl, sink: sink, lite: lite, interval: pollInterval(lite), kick: make(chan struct{}, 1),
		home: config.ResolveHome("")}
}

// Kick schedules an immediate poll (after user actions).
func (p *Poller) Kick() {
	select {
	case p.kick <- struct{}{}:
	default:
	}
}

func (p *Poller) Run(ctx context.Context) {
	for {
		p.Refresh()
		select {
		case <-ctx.Done():
			return
		case <-p.kick:
		case <-time.After(p.interval):
		}
	}
}

// serviceStatus is slow-ish (launchctl/SCM/systemctl), so cache it briefly.
func (p *Poller) serviceStatus() string {
	if time.Since(p.svcAt) < 15*time.Second && p.svcSt != "" {
		return p.svcSt
	}
	p.svcSt, _ = svc.Status(p.home)
	p.svcAt = time.Now()
	return p.svcSt
}

// Refresh polls once, publishes the snapshot and returns it.
func (p *Poller) Refresh() Snapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s := Snapshot{Lite: p.lite, Platform: runtime.GOOS + "/" + runtime.GOARCH, GUIVer: config.KithVersion,
		Service: p.serviceStatus()}
	var status, peers, tasks json.RawMessage
	if err := p.cl.Call(ctx, "status", nil, &status); err != nil {
		s.Error = friendly(err).Error()
		p.last = s
		p.publish(s, nil)
		return s
	}
	s.Running, s.Status = true, status
	if err := p.cl.Call(ctx, "peers", nil, &peers); err == nil {
		s.Peers = peers
	}
	if err := p.cl.Call(ctx, "tasks", map[string]int{"limit": 200}, &tasks); err == nil {
		s.Tasks = tasks
	}
	var ts []taskLite
	json.Unmarshal(s.Tasks, &ts)
	for _, t := range ts {
		if needsApproval(t) {
			s.Pending++
		}
		if !isTerminal(t.State) {
			s.Active++
		}
	}
	var ps []peerLite
	json.Unmarshal(s.Peers, &ps)
	for _, n := range diffNotices(p.prev, ts) {
		p.sink.Notify(n)
	}
	if ts == nil {
		ts = []taskLite{}
	}
	p.prev = ts
	p.last = s
	p.publish(s, ps)
	return s
}

func (p *Poller) publish(s Snapshot, ps []peerLite) {
	p.sink.State(s)
	p.sink.Tray(s, ps)
}

// Last returns the most recent snapshot without polling.
func (p *Poller) Last() Snapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.last
}

var _ Caller = ipc.Client{}
