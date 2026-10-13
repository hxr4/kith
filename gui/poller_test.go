package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
)

func TestDiffNotices(t *testing.T) {
	prev := []taskLite{
		{ID: "a", Role: "requester", State: "RUNNING", Name: "train", PeerName: "HARI-PC"},
		{ID: "b", Role: "executor", State: "SUBMITTED", PendingApproval: true, PeerName: "mac"},
	}
	cur := []taskLite{
		{ID: "a", Role: "requester", State: "COMPLETED", Name: "train", PeerName: "HARI-PC", Detail: "artifacts copied back"},
		{ID: "b", Role: "executor", State: "SUBMITTED", PendingApproval: true, PeerName: "mac"}, // already notified
		{ID: "c", Role: "executor", State: "SUBMITTED", PendingApproval: true, PeerName: "mac", Name: "report"},
		{ID: "d", Role: "requester", State: "FAILED"}, // new but already terminal: not "just finished"
	}
	ns := diffNotices(prev, cur)
	if len(ns) != 2 {
		t.Fatalf("%+v", ns)
	}
	if ns[0].Kind != "finished" || ns[0].TaskID != "a" || !strings.Contains(ns[0].Title, "COMPLETED") || !strings.Contains(ns[0].Body, "copied back") {
		t.Fatalf("%+v", ns[0])
	}
	if ns[1].Kind != "approval" || ns[1].TaskID != "c" || !strings.Contains(ns[1].Title, "mac") {
		t.Fatalf("%+v", ns[1])
	}
	if diffNotices(nil, cur) != nil {
		t.Fatal("first poll must only set a baseline")
	}
}

type fakeCaller struct {
	mu    sync.Mutex
	down  bool
	tasks []taskLite
}

func (f *fakeCaller) Call(_ context.Context, method string, _, out any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		return errors.New("the kith daemon is not running")
	}
	var v any
	switch method {
	case "status":
		v = map[string]any{"name": "mac", "port": 7420}
	case "peers":
		v = []map[string]any{{"name": "HARI-PC", "reachable": true}, {"name": "pi", "reachable": false}}
	case "tasks":
		v = f.tasks
	}
	b, _ := json.Marshal(v)
	*(out.(*json.RawMessage)) = b
	return nil
}

type fakeSink struct {
	states  []Snapshot
	trays   int
	notices []Notice
}

func (s *fakeSink) State(x Snapshot)          { s.states = append(s.states, x) }
func (s *fakeSink) Tray(Snapshot, []peerLite) { s.trays++ }
func (s *fakeSink) Notify(n Notice)           { s.notices = append(s.notices, n) }

func TestPollerCountsAndNotifies(t *testing.T) {
	fc := &fakeCaller{tasks: []taskLite{{ID: "a", Role: "requester", State: "RUNNING"}}}
	sink := &fakeSink{}
	p := NewPoller(fc, sink, false)
	p.svcSt = "installed, running" // avoid touching the real service manager
	p.svcAt = p.svcAt.AddDate(100, 0, 0)

	s := p.Refresh()
	if !s.Running || s.Active != 1 || s.Pending != 0 || len(sink.notices) != 0 {
		t.Fatalf("%+v %+v", s, sink.notices)
	}
	fc.tasks = []taskLite{{ID: "a", Role: "requester", State: "COMPLETED"},
		{ID: "x", Role: "executor", State: "SUBMITTED", PendingApproval: true}}
	s = p.Refresh()
	if s.Pending != 1 || s.Active != 1 || len(sink.notices) != 2 {
		t.Fatalf("%+v %+v", s, sink.notices)
	}
	fc.down = true
	s = p.Refresh()
	if s.Running || s.Error == "" || sink.trays != 3 || len(sink.states) != 3 {
		t.Fatalf("daemon down: %+v", s)
	}
}

func TestLiteOverride(t *testing.T) {
	t.Setenv("KITH_GUI_LITE", "1")
	if !isLite() || pollInterval(true) <= pollInterval(false) {
		t.Fatal("lite mode")
	}
	t.Setenv("KITH_GUI_LITE", "0")
	if isLite() {
		t.Fatal("override off")
	}
}
