package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kith/internal/audit"
	"kith/internal/config"
	"kith/internal/protocol"
	"kith/internal/task"
)

func TestPairingSuccess(t *testing.T) {
	mac := newNode(t, "mac", nil)
	win := newNode(t, "win", nil)
	pairNodes(t, mac, win)
	var ps []PeerView
	mac.mustCall("peers", nil, &ps)
	if len(ps) != 1 || ps[0].FP != win.d.FP() || !ps[0].Reachable || ps[0].Name != "win" {
		t.Fatalf("%+v", ps)
	}
	waitFor(t, 5*time.Second, "caps exchanged", func() bool {
		mac.mustCall("peers", nil, &ps)
		return ps[0].Caps != ""
	})
	// Pairing events are audited, the code never is.
	for _, n := range []*node{mac, win} {
		found := false
		for _, e := range auditEvents(t, n) {
			if e.Event == audit.EvPairSuccess {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s: no pair.success audit event", n.name)
		}
	}
}

func TestPairingWrongCodeAndRateLimit(t *testing.T) {
	mac := newNode(t, "mac", nil)
	win := newNode(t, "win", func(o *Options) {
		o.PairCodeHook = func(c string) string { return "ZZZZZZ" } // human on win typed/confirmed a different code
	})
	for i := 0; i < 5; i++ {
		errA, errB := tryPair(t, mac, win)
		if errA == nil || errB == nil {
			t.Fatalf("attempt %d: wrong code paired (%v / %v)", i, errA, errB)
		}
		if c := protocol.CodeOf(errA); c != protocol.CodeAuthFailed {
			t.Fatalf("attempt %d: want AUTH_FAILED, got %v", i, errA)
		}
	}
	// Sixth attempt from the same source IP is rate limited.
	_, err := runPair(mac, win.addr())
	if protocol.CodeOf(err) != protocol.CodeRateLimited {
		t.Fatalf("want RATE_LIMITED, got %v", err)
	}
	var ps []PeerView
	mac.mustCall("peers", nil, &ps)
	if len(ps) != 0 {
		t.Fatal("peer stored after failed pairing")
	}
	n := 0
	for _, e := range auditEvents(t, win) {
		if e.Event == audit.EvPairFail {
			n++
		}
	}
	if n < 5 {
		t.Fatalf("expected >=5 pair.fail audit events, got %d", n)
	}
}

func TestIdentityMismatchRejected(t *testing.T) {
	mac := newNode(t, "mac", nil)
	win := newNode(t, "win", nil)
	pairNodes(t, mac, win)
	oldFP := win.d.FP()
	// "Reinstall" win: new identity, same address.
	win.stop()
	os.Remove(filepath.Join(win.home, "identity.key"))
	os.Remove(filepath.Join(win.home, "state.db"))
	os.Remove(filepath.Join(win.home, "state.db-wal"))
	os.Remove(filepath.Join(win.home, "state.db-shm"))
	win.start()
	if win.d.FP() == oldFP {
		t.Fatal("identity not regenerated")
	}
	mac.d.wakeDialers()
	waitFor(t, 20*time.Second, "identity mismatch audited", func() bool {
		for _, e := range auditEvents(t, mac) {
			if e.Event == audit.EvIdentityMismatch && e.PeerFP == oldFP && strings.Contains(e.Detail, win.d.FP()) {
				return true
			}
		}
		return false
	})
	if mac.d.conn(oldFP) != nil || mac.d.conn(win.d.FP()) != nil {
		t.Fatal("impostor was trusted")
	}
}

func TestManualIPAndDiscoveryReprobe(t *testing.T) {
	mac := newNode(t, "mac", nil)
	win := newNode(t, "win", nil)
	pairNodes(t, mac, win) // manual-IP pairing over 127.0.0.1
	// win forgets how to reach mac, then moves to a new port, so mac's
	// last_addr is stale too. Only a (fake) mDNS answer advertising win's
	// fingerprint at the new address can heal the link.
	win.d.st.TouchPeer(context.Background(), mac.d.FP(), "127.0.0.1:1")
	win.stop()
	newPort := freePort(t)
	writeConfig(t, win.home, "win", newPort, "auto")
	win.port = newPort
	win.start()
	time.Sleep(1500 * time.Millisecond)
	if mac.d.conn(win.d.FP()) != nil {
		t.Fatal("connected without discovery")
	}
	mac.d.mu.Lock()
	mac.d.discovered[strings.ToLower(win.d.FP()[:16])] = win.addr()
	mac.d.mu.Unlock()
	mac.d.wakeDialers()
	waitConnected(t, mac, win)
	p, _ := mac.d.st.GetPeer(context.Background(), win.d.FP())
	if p.LastAddr != win.addr() {
		t.Fatalf("last_addr not updated: %s", p.LastAddr)
	}
}

func TestSuccessfulTaskBothDirections(t *testing.T) {
	py := pythonPath(t)
	mac := newNode(t, "mac", nil)
	win := newNode(t, "win", nil)
	pairNodes(t, mac, win)
	for _, dir := range []struct{ req, exe *node }{{mac, win}, {win, mac}} {
		p := newProject(t, 3<<20+517)
		spec := makeSpec(py, 3, 0.05, "", withEnv(map[string]any{"python": ">=3.8"}))
		r := submit(t, dir.req, dir.exe, p, spec)
		if r.State == task.Rejected {
			t.Fatalf("rejected: %+v", r)
		}
		id := r.TaskID
		et := waitState(t, dir.exe, id, task.Completed, task.Failed)
		if et.State != task.Completed {
			b, _ := os.ReadFile(dir.exe.d.logPath(id))
			t.Fatalf("%s → %s: executor %s %s\n%s", dir.req.name, dir.exe.name, et.State, et.Detail, b)
		}
		rt := waitState(t, dir.req, id, task.Completed)
		// Inputs arrived intact.
		if got := shaOf(t, filepath.Join(dir.exe.d.wsDir(id), "data", "dataset.bin")); got != p.dataSHA {
			t.Fatal("input hash mismatch")
		}
		// model.pt returned to the requester's project dir; hashes match.
		local := filepath.Join(p.dir, "out", "model.pt")
		remote := filepath.Join(dir.exe.d.wsDir(id), "out", "model.pt")
		if shaOf(t, local) != shaOf(t, remote) {
			t.Fatal("artifact hash mismatch")
		}
		var tv TaskView
		dir.req.mustCall("task", map[string]string{"id": id}, &tv)
		if len(tv.Artifacts) != 2 {
			t.Fatalf("artifacts %+v", tv.Artifacts)
		}
		for _, a := range tv.Artifacts {
			if a.Location != "local" || shaOf(t, filepath.Join(p.dir, filepath.FromSlash(a.Path))) != a.SHA256 {
				t.Fatalf("artifact %+v", a)
			}
		}
		// Logs streamed to the requester.
		waitFor(t, 10*time.Second, "log mirror", func() bool {
			b, _ := os.ReadFile(dir.req.d.logPath(id))
			return bytes.Contains(b, []byte("epoch 3/3")) && bytes.Contains(b, []byte("done"))
		})
		// Timeline mirrored.
		states := []string{}
		for _, tr := range tv.Timeline {
			states = append(states, tr.State)
		}
		if !strings.Contains(strings.Join(states, ","), "RUNNING") || rt.State != task.Completed {
			t.Fatalf("timeline %v", states)
		}
	}
}

func TestCapabilityMismatchRejectedBeforeTransfer(t *testing.T) {
	py := pythonPath(t)
	mac := newNode(t, "mac", nil)
	win := newNode(t, "win", nil)
	win.caps.set(func(m *caps_Manifest) { m.Packages["torch"] = "2.3.0+cpu" })
	pairNodes(t, mac, win)
	p := newProject(t, 1024)
	spec := makeSpec(py, 1, 0, "", withEnv(map[string]any{"packages": map[string]string{"torch": ">=2.3+cu121"},
		"gpu": map[string]any{"vram_gb": 4, "required": true}}))
	r := submit(t, mac, win, p, spec)
	if r.State != task.Rejected || r.Code != protocol.CodeNoMatch {
		t.Fatalf("%+v", r)
	}
	if !strings.Contains(r.Detail, "torch>=2.3+cu121 not found (found 2.3.0+cpu)") ||
		r.Remediation != "pip install torch --index-url https://download.pytorch.org/whl/cu121" {
		t.Fatalf("detail/remediation: %q / %q", r.Detail, r.Remediation)
	}
	et := win.task(r.TaskID)
	if et.State != task.Rejected {
		t.Fatal("executor state", et.State)
	}
	// Nothing was transferred.
	if _, err := os.Stat(win.d.wsDir(r.TaskID)); !os.IsNotExist(err) {
		t.Fatal("workspace exists: transfer happened before the gate")
	}
	trs, _ := win.d.st.ListTransfers(context.Background(), r.TaskID)
	if len(trs) != 0 {
		t.Fatal("transfer rows exist")
	}
	// The probe command is part of the gate too.
	spec = makeSpec(py, 1, 0, "", withEnv(map[string]any{"probe": []string{py, "-c", "import sys; print('no cuda'); sys.exit(1)"}}))
	r = submit(t, mac, win, p, spec)
	if r.State != task.Rejected || r.Code != protocol.CodeNoMatch || !strings.Contains(r.Detail, "no cuda") {
		t.Fatalf("probe gate: %+v", r)
	}
}

func TestDuplicateSubmitReturnsSameRecord(t *testing.T) {
	py := pythonPath(t)
	mac := newNode(t, "mac", nil)
	win := newNode(t, "win", nil)
	pairNodes(t, mac, win)
	p := newProject(t, 1000)
	spec := makeSpec(py, 1, 0, "")
	r1 := submit(t, mac, win, p, spec)
	r2 := submit(t, mac, win, p, spec)
	if r1.TaskID != r2.TaskID || !r2.Existing {
		t.Fatalf("%+v %+v", r1, r2)
	}
	waitState(t, win, r1.TaskID, task.Completed)
	// Re-sending task.submit to the executor directly is idempotent too.
	c := mac.d.conn(win.d.FP())
	mac.d.sendSubmit(context.Background(), c, mac.task(r1.TaskID))
	ts, _ := win.d.st.ListTasks(context.Background(), storeFilterAll())
	if len(ts) != 1 {
		t.Fatalf("executor has %d records", len(ts))
	}
	if waitState(t, mac, r1.TaskID, task.Completed).State != task.Completed {
		t.Fatal()
	}
	var tv TaskView
	win.mustCall("task", map[string]string{"id": r1.TaskID}, &tv)
	runs := 0
	for _, tr := range tv.Timeline {
		if tr.State == task.Running {
			runs++
		}
	}
	if runs != 1 {
		t.Fatalf("task ran %d times", runs)
	}
}

func TestBusy(t *testing.T) {
	py := pythonPath(t)
	mac := newNode(t, "mac", nil)
	win := newNode(t, "win", nil)
	pairNodes(t, mac, win)
	p := newProject(t, 1000)
	r1 := submit(t, mac, win, p, makeSpec(py, 40, 0.1, ""))
	waitState(t, win, r1.TaskID, task.Running)
	r2 := submit(t, mac, win, p, makeSpec(py, 1, 0, ""))
	if r2.State != task.Rejected || r2.Code != protocol.CodeBusy {
		t.Fatalf("%+v", r2)
	}
	et := win.task(r2.TaskID)
	if et.Code != protocol.CodeBusy {
		t.Fatal(et.Code)
	}
	var tv TaskView
	mac.mustCall("cancel", map[string]string{"id": r1.TaskID}, &tv)
	if tv.State != task.Cancelled {
		t.Fatal(tv.State)
	}
}

func TestPolicyAskApproveDenyExpiry(t *testing.T) {
	py := pythonPath(t)
	mac := newNode(t, "mac", nil)
	win := newNode(t, "win", func(o *Options) { o.ApprovalTTL = 1500 * time.Millisecond })
	pairNodes(t, mac, win)
	win.mustCall("set_policy", map[string]string{"peer": "mac", "policy": config.PolicyAsk}, nil)
	p := newProject(t, 1000)

	// pending → approve → runs
	r := submit(t, mac, win, p, makeSpec(py, 1, 0, ""))
	if !r.PendingApproval || r.State != task.Submitted {
		t.Fatalf("%+v", r)
	}
	if _, err := os.Stat(win.d.wsDir(r.TaskID)); !os.IsNotExist(err) {
		t.Fatal("transfer before approval")
	}
	var tv TaskView
	win.mustCall("approve", map[string]string{"id": r.TaskID}, &tv)
	waitState(t, mac, r.TaskID, task.Completed)

	// deny → REJECTED (POLICY_DENIED)
	r = submit(t, mac, win, p, makeSpec(py, 1, 0, ""))
	win.mustCall("deny", map[string]string{"id": r.TaskID}, &tv)
	mt := waitState(t, mac, r.TaskID, task.Rejected)
	if mt.Code != protocol.CodePolicyDenied {
		t.Fatal(mt.Code)
	}

	// unapproved → expires → REJECTED (POLICY_DENIED)
	r = submit(t, mac, win, p, makeSpec(py, 1, 0, ""))
	mt = waitState(t, mac, r.TaskID, task.Rejected)
	if mt.Code != protocol.CodePolicyDenied || !strings.Contains(mt.Detail, "not approved within") {
		t.Fatalf("%+v", mt)
	}

	// auto: accepted immediately.
	win.mustCall("set_policy", map[string]string{"peer": "mac", "policy": config.PolicyAuto}, nil)
	r = submit(t, mac, win, p, makeSpec(py, 1, 0, ""))
	if r.PendingApproval || r.State == task.Rejected {
		t.Fatalf("%+v", r)
	}
	waitState(t, mac, r.TaskID, task.Completed)
}

func TestApprovalReverifiesCapabilities(t *testing.T) {
	py := pythonPath(t)
	mac := newNode(t, "mac", nil)
	win := newNode(t, "win", nil)
	pairNodes(t, mac, win)
	win.mustCall("set_policy", map[string]string{"peer": "mac", "policy": config.PolicyAsk}, nil)
	p := newProject(t, 1000)
	r := submit(t, mac, win, p, makeSpec(py, 1, 0, "", withEnv(map[string]any{"python": ">=3.10"})))
	if !r.PendingApproval {
		t.Fatalf("%+v", r)
	}
	// The manifest changed between submit and approval ("manifests lie").
	win.caps.set(func(m *caps_Manifest) { m.Python = []string{"3.9.1"} })
	var tv TaskView
	win.mustCall("approve", map[string]string{"id": r.TaskID}, &tv)
	if tv.State != task.Rejected || tv.Code != protocol.CodeNoMatch {
		t.Fatalf("%+v", tv.Task)
	}
	waitState(t, mac, r.TaskID, task.Rejected)
}

// Regression: dialing a machine that is not running `kith pair` must fail
// immediately with the real reason, before any code is shown to the human.
func TestPairingTargetNotInPairingModeFailsFast(t *testing.T) {
	mac := newNode(t, "mac", nil)
	win := newNode(t, "win", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	start := time.Now()
	cn, err := mac.cl.Open(ctx, "pair", map[string]string{"ip": win.addr()})
	if err != nil {
		t.Fatal(err)
	}
	defer cn.Close()
	sawCode := false
	err = cn.Wait(func(ev string, _ json.RawMessage) error {
		if ev == "code" {
			sawCode = true
			return cn.Send(map[string]bool{"confirm": false})
		}
		return nil
	}, nil)
	if sawCode {
		t.Fatal("a pairing code was shown although the remote is not in pairing mode")
	}
	if protocol.CodeOf(err) != protocol.CodeAuthFailed || !strings.Contains(err.Error(), "win is not in pairing mode") {
		t.Fatalf("want AUTH_FAILED 'win is not in pairing mode', got %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("took %s; must fail fast", time.Since(start))
	}
	// Not a failed attempt: it must not count towards the rate limit.
	if !mac.d.lim.Allowed("127.0.0.1") || !win.d.lim.Allowed("127.0.0.1") {
		t.Fatal("rate limiter charged")
	}
	for i := 0; i < 6; i++ {
		mac.call("pair", map[string]string{"ip": win.addr()}, nil)
	}
	pairNodes(t, mac, win) // still pairs normally afterwards
}
