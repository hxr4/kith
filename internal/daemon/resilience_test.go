package daemon

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"kith/internal/audit"
	"kith/internal/peer"
	"kith/internal/protocol"
	"kith/internal/store"
	"kith/internal/task"
)

func TestInterruptedTransferResumes(t *testing.T) {
	py := pythonPath(t)
	var chunks atomic.Int32
	var cut atomic.Bool
	mac := newNode(t, "mac", nil)
	win := newNode(t, "win", func(o *Options) {
		o.OnFileChunk = func(c *peer.Conn) {
			// Drop the link once, a few MiB into the dataset transfer.
			if chunks.Add(1) == 4 && cut.CompareAndSwap(false, true) {
				c.Close(protocolErr("test: cable pulled"))
			}
		}
	})
	pairNodes(t, mac, win)
	p := newProject(t, 12<<20+333)
	r := submit(t, mac, win, p, makeSpec(py, 1, 0, ""))
	et := waitState(t, win, r.TaskID, task.Completed, task.Failed)
	if et.State != task.Completed {
		t.Fatalf("%s %s", et.State, et.Detail)
	}
	if !cut.Load() {
		t.Fatal("transfer was never interrupted")
	}
	if got := shaOf(t, filepath.Join(win.d.wsDir(r.TaskID), "data", "dataset.bin")); got != p.dataSHA {
		t.Fatal("resumed input hash mismatch")
	}
	// Resumed, not restarted: total chunks received ≈ file size / 1MiB (+ small files), not 2x.
	if n := chunks.Load(); n > 16 {
		t.Fatalf("received %d chunks; transfer restarted instead of resuming", n)
	}
	trs, _ := win.d.st.ListTransfers(context.Background(), r.TaskID)
	for _, tr := range trs {
		if tr.BytesDone != tr.Size {
			t.Fatalf("transfer row %+v", tr)
		}
	}
	waitState(t, mac, r.TaskID, task.Completed)
}

func protocolErr(msg string) error { return protocol.Errf(protocol.CodePeerUnreachable, "%s", msg) }

func TestPartitionDuringRunning(t *testing.T) {
	py := pythonPath(t)
	mac := newNode(t, "mac", nil)
	win := newNode(t, "win", nil)
	pairNodes(t, mac, win)
	p := newProject(t, 2000)
	r := submit(t, mac, win, p, makeSpec(py, 12, 0.1, ""))
	waitState(t, win, r.TaskID, task.Running)
	waitFor(t, 10*time.Second, "some logs mirrored", func() bool {
		b, _ := os.ReadFile(mac.d.logPath(r.TaskID))
		return bytes.Contains(b, []byte("epoch 1/"))
	})
	// Partition: drop the TCP connection. The executor keeps running and
	// its state is unchanged by the link loss.
	closeConn(mac, win.d.FP())
	time.Sleep(50 * time.Millisecond)
	if s := win.task(r.TaskID).State; s != task.Running {
		t.Fatalf("partition changed executor state to %s", s)
	}
	waitState(t, win, r.TaskID, task.Completed)
	mt := waitState(t, mac, r.TaskID, task.Completed)
	if mt.State != task.Completed {
		t.Fatal(mt.State)
	}
	assertLogsIdentical(t, mac, win, r.TaskID)
}

// The requester is down for the whole run: the task finishes during the
// partition and is reconciled (state, artifacts, full log) on reconnect.
func TestRequesterOfflineSyncAndLogReplay(t *testing.T) {
	py := pythonPath(t)
	mac := newNode(t, "mac", nil)
	win := newNode(t, "win", nil)
	pairNodes(t, mac, win)
	p := newProject(t, 2000)
	r := submit(t, mac, win, p, makeSpec(py, 6, 0.1, ""))
	waitState(t, win, r.TaskID, task.Running)
	mac.stop()
	if s := win.task(r.TaskID).State; s != task.Running {
		t.Fatalf("executor state changed on partition: %s", s)
	}
	waitState(t, win, r.TaskID, task.Completed)
	os.Remove(filepath.Join(p.dir, "out", "model.pt"))
	mac.start()
	mt := waitState(t, mac, r.TaskID, task.Completed)
	if mt.LastSynced == "" {
		t.Fatal("last_synced not recorded")
	}
	if shaOf(t, filepath.Join(p.dir, "out", "model.pt")) != shaOf(t, filepath.Join(win.d.wsDir(r.TaskID), "out", "model.pt")) {
		t.Fatal("artifact not copied back after reconnect")
	}
	assertLogsIdentical(t, mac, win, r.TaskID)
}

func assertLogsIdentical(t *testing.T, req, exe *node, id string) {
	t.Helper()
	waitFor(t, 10*time.Second, "log replay complete", func() bool {
		a, _ := os.ReadFile(req.d.logPath(id))
		b, _ := os.ReadFile(exe.d.logPath(id))
		return len(b) > 0 && bytes.Equal(a, b)
	})
	// Replaying again from seq 0 is deduplicated.
	closeConn(req, exe.d.FP())
	waitConnected(t, req, exe)
	time.Sleep(500 * time.Millisecond)
	a, _ := os.ReadFile(req.d.logPath(id))
	b, _ := os.ReadFile(exe.d.logPath(id))
	if !bytes.Equal(a, b) {
		t.Fatalf("log replay duplicated data:\nreq %d bytes, exe %d bytes", len(a), len(b))
	}
	var lr LogsResult
	req.mustCall("logs", map[string]any{"id": id, "follow": true}, &lr)
	if !lr.Done || lr.Offset != int64(len(b)) {
		t.Fatalf("logs -f did not terminate cleanly: %+v", lr)
	}
}

func readPids(t *testing.T, ws string) []int {
	var out []int
	waitFor(t, 15*time.Second, "pids.txt", func() bool {
		b, err := os.ReadFile(filepath.Join(ws, "pids.txt"))
		if err != nil {
			return false
		}
		f := strings.Fields(string(b))
		if len(f) != 2 {
			return false
		}
		out = nil
		for _, s := range f {
			n, _ := strconv.Atoi(s)
			out = append(out, n)
		}
		return true
	})
	return out
}

func TestCancellationKillsProcessTree(t *testing.T) {
	py := pythonPath(t)
	mac := newNode(t, "mac", nil)
	win := newNode(t, "win", nil)
	pairNodes(t, mac, win)
	p := newProject(t, 2000)
	r := submit(t, mac, win, p, makeSpec(py, 1000, 0.1, "grandchild"))
	waitState(t, win, r.TaskID, task.Running)
	pids := readPids(t, win.d.wsDir(r.TaskID))
	for _, pid := range pids {
		if !task.Alive(pid) {
			t.Fatal("process not running", pid)
		}
	}
	var tv TaskView
	mac.mustCall("cancel", map[string]string{"id": r.TaskID}, &tv)
	if tv.State != task.Cancelled {
		t.Fatalf("cancel returned %s", tv.State)
	}
	waitState(t, win, r.TaskID, task.Cancelled)
	waitFor(t, 10*time.Second, "child and grandchild dead", func() bool {
		return !task.Alive(pids[0]) && !task.Alive(pids[1])
	})
	states := []string{}
	trs, _ := win.d.st.Transitions(context.Background(), r.TaskID)
	for _, tr := range trs {
		states = append(states, tr.State)
	}
	if strings.Join(states, ",") != "SUBMITTED,ACCEPTED,TRANSFERRING,RUNNING,CANCELLING,CANCELLED" {
		t.Fatalf("timeline %v", states)
	}
}

func TestTimeout(t *testing.T) {
	py := pythonPath(t)
	mac := newNode(t, "mac", nil)
	win := newNode(t, "win", func(o *Options) { o.TimeoutUnit = 700 * time.Millisecond })
	pairNodes(t, mac, win)
	p := newProject(t, 100)
	r := submit(t, mac, win, p, makeSpec(py, 1000, 0.1, "", withPolicy(map[string]any{"timeout_minutes": 1})))
	et := waitState(t, win, r.TaskID, task.Failed)
	if et.Detail != protocol.CodeTimeout || et.Code != protocol.CodeTimeout {
		t.Fatalf("%+v", et)
	}
	trs, _ := win.d.st.Transitions(context.Background(), r.TaskID)
	if trs[len(trs)-2].State != task.Cancelling {
		t.Fatalf("timeout must pass through CANCELLING: %+v", trs)
	}
	waitState(t, mac, r.TaskID, task.Failed)
}

func TestNonZeroExitFails(t *testing.T) {
	py := pythonPath(t)
	mac := newNode(t, "mac", nil)
	win := newNode(t, "win", nil)
	pairNodes(t, mac, win)
	p := newProject(t, 100)
	r := submit(t, mac, win, p, makeSpec(py, 1, 0, "fail"))
	mt := waitState(t, mac, r.TaskID, task.Failed)
	if !strings.Contains(mt.Detail, "status 7") {
		t.Fatal(mt.Detail)
	}
}

func TestArtifactsRetainAndFetch(t *testing.T) {
	py := pythonPath(t)
	mac := newNode(t, "mac", nil)
	win := newNode(t, "win", nil)
	pairNodes(t, mac, win)
	p := newProject(t, 1000)
	// model.pt is ~128 KiB; a 64 KiB cap keeps all outputs on the executor.
	spec := makeSpec(py, 1, 0, "", withOutputs(map[string]any{"include": []string{"out/*"}, "retain_cap_gb": 64.0 / (1 << 20)}))
	r := submit(t, mac, win, p, spec)
	waitState(t, mac, r.TaskID, task.Completed)
	if _, err := os.Stat(filepath.Join(p.dir, "out", "model.pt")); !os.IsNotExist(err) {
		t.Fatal("retained artifact was copied back")
	}
	var as []ArtifactView
	mac.mustCall("artifacts", map[string]string{"id": r.TaskID}, &as)
	if len(as) != 2 {
		t.Fatalf("%+v", as)
	}
	for _, a := range as {
		if a.Location != store.LocRemote || a.ProducerOffline || a.ProducerFP != win.d.FP() {
			t.Fatalf("%+v", a)
		}
	}
	var fr FetchResult
	mac.mustCall("fetch", map[string]string{"id": r.TaskID, "path": "out/model.pt"}, &fr)
	remote := filepath.Join(win.d.wsDir(r.TaskID), "out", "model.pt")
	if fr.SHA256 != shaOf(t, remote) || shaOf(t, fr.Local) != fr.SHA256 || fr.Local != filepath.Join(p.dir, "out", "model.pt") {
		t.Fatalf("%+v", fr)
	}
	mac.mustCall("artifacts", map[string]string{"id": r.TaskID}, &as)
	for _, a := range as {
		if a.Path == "out/model.pt" && (a.Location != store.LocLocal || !a.Fetched) {
			t.Fatalf("%+v", a)
		}
	}
	// Producer offline: queryable as remote + producer_offline; fetch fails cleanly.
	win.stop()
	waitFor(t, 10*time.Second, "producer offline", func() bool {
		mac.mustCall("artifacts", map[string]string{"id": r.TaskID}, &as)
		for _, a := range as {
			if a.Path == "out/metrics.json" {
				return a.ProducerOffline && a.Status == "remote (producer offline)"
			}
		}
		return false
	})
	err := mac.call("fetch", map[string]string{"id": r.TaskID, "path": "out/metrics.json"}, &fr)
	if protocol.CodeOf(err) != protocol.CodePeerUnreachable {
		t.Fatal(err)
	}
}

// Graceful executor restart mid-run: INTERRUPTED → policy.
func TestDaemonRestartRecoveryPolicies(t *testing.T) {
	py := pythonPath(t)
	mac := newNode(t, "mac", nil)
	win := newNode(t, "win", nil)
	pairNodes(t, mac, win)
	cases := []struct {
		policy  map[string]any
		mode    string
		want    string
		attempt int
		logHas  string
	}{
		{map[string]any{"on_interrupt": "fail"}, "", task.Failed, 1, ""},
		{map[string]any{"on_interrupt": "restart"}, "", task.Completed, 2, "attempt 2"},
		{map[string]any{"on_interrupt": "resume-if-checkpoint", "checkpoint": map[string]any{"pattern": "out/checkpoint*.pt"}}, "checkpoint", task.Completed, 2, "resumed from checkpoint"},
	}
	for _, c := range cases {
		p := newProject(t, 500)
		r := submit(t, mac, win, p, makeSpec(py, 8, 0.15, c.mode, withPolicy(c.policy)))
		waitState(t, win, r.TaskID, task.Running)
		waitFor(t, 10*time.Second, "progress", func() bool {
			b, _ := os.ReadFile(win.d.logPath(r.TaskID))
			return bytes.Contains(b, []byte("epoch 3/"))
		})
		pid := win.task(r.TaskID).PID
		win.restart()
		if task.Alive(pid) && !waitDeadQuiet(pid) {
			t.Fatal("task process survived graceful shutdown")
		}
		et := waitState(t, win, r.TaskID, c.want)
		if et.Attempt != c.attempt {
			t.Fatalf("%v: attempt %d", c.policy, et.Attempt)
		}
		trs, _ := win.d.st.Transitions(context.Background(), r.TaskID)
		sawInterrupted := false
		for _, tr := range trs {
			if tr.State == task.Interrupted {
				sawInterrupted = true
			}
		}
		if !sawInterrupted {
			t.Fatalf("%v: never INTERRUPTED", c.policy)
		}
		if c.logHas != "" {
			b, _ := os.ReadFile(win.d.logPath(r.TaskID))
			if !bytes.Contains(b, []byte(c.logHas)) {
				t.Fatalf("%v: log lacks %q:\n%s", c.policy, c.logHas, b)
			}
		}
		waitState(t, mac, r.TaskID, c.want)
	}
	// Restart beyond the attempt cap fails.
	p := newProject(t, 100)
	r := submit(t, mac, win, p, makeSpec(py, 1000, 0.1, "", withPolicy(map[string]any{"on_interrupt": "restart"})))
	for i := 0; i < 3; i++ {
		waitState(t, win, r.TaskID, task.Running, task.Failed)
		if win.task(r.TaskID).State == task.Failed {
			break
		}
		win.restart()
	}
	et := waitState(t, win, r.TaskID, task.Failed)
	if !strings.Contains(et.Detail, "attempt cap") {
		t.Fatal(et.Detail)
	}
}

func waitDeadQuiet(pid int) bool {
	for i := 0; i < 100; i++ {
		if !task.Alive(pid) {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

func TestPendingApprovalSurvivesRestart(t *testing.T) {
	py := pythonPath(t)
	mac := newNode(t, "mac", nil)
	win := newNode(t, "win", nil)
	pairNodes(t, mac, win)
	win.mustCall("set_policy", map[string]string{"peer": "mac", "policy": "ask"}, nil)
	p := newProject(t, 100)
	r := submit(t, mac, win, p, makeSpec(py, 1, 0, ""))
	win.restart()
	waitConnected(t, mac, win)
	et := win.task(r.TaskID)
	if et.State != task.Submitted || !et.PendingApproval {
		t.Fatalf("%+v", et)
	}
	var tv TaskView
	win.mustCall("approve", map[string]string{"id": r.TaskID}, &tv)
	waitState(t, mac, r.TaskID, task.Completed)
}

func TestAuditAccountsForEveryEvent(t *testing.T) {
	py := pythonPath(t)
	var code string
	mac := newNode(t, "mac", nil)
	win := newNode(t, "win", func(o *Options) {
		o.PairCodeHook = func(c string) string { code = c; return c }
	})
	pairNodes(t, mac, win)
	p := newProject(t, 1000)
	spec := makeSpec(py, 2, 0, "")
	r := submit(t, mac, win, p, spec)
	waitState(t, mac, r.TaskID, task.Completed)
	r2 := submit(t, mac, win, p, makeSpec(py, 1000, 0.1, ""))
	waitState(t, win, r2.TaskID, task.Running)
	var tv TaskView
	mac.mustCall("cancel", map[string]string{"id": r2.TaskID}, &tv)

	for _, n := range []*node{mac, win} {
		raw, _ := os.ReadFile(filepath.Join(n.home, audit.FileName))
		if code != "" && bytes.Contains(raw, []byte(code)) {
			t.Fatalf("%s: pairing code leaked into audit log", n.name)
		}
		if bytes.Contains(raw, []byte("PRIVATE KEY")) {
			t.Fatalf("%s: key material in audit log", n.name)
		}
	}
	exe := auditEvents(t, win)
	need := map[string]bool{audit.EvPairSuccess: false, audit.EvSubmit: false, audit.EvAccept: false, audit.EvCancel: false}
	states := map[string]bool{}
	for _, e := range exe {
		if _, ok := need[e.Event]; ok {
			need[e.Event] = true
		}
		if e.Event == audit.EvSubmit && e.TaskID == r.TaskID {
			if len(e.Argv) == 0 || e.Argv[1] != "train.py" || e.PeerFP != mac.d.FP() || e.TS == "" {
				t.Fatalf("submit event lacks argv/peer/ts: %+v", e)
			}
		}
		if e.Event == audit.EvState && e.TaskID == r.TaskID {
			states[e.State] = true
		}
	}
	for ev, ok := range need {
		if !ok {
			t.Errorf("executor audit lacks %s", ev)
		}
	}
	for _, s := range []string{task.Transferring, task.Running, task.Finalizing, task.Completed} {
		if !states[s] {
			t.Errorf("executor audit lacks state %s", s)
		}
	}
	// Every journaled executor transition has an audit record.
	trs, _ := win.d.st.Transitions(context.Background(), r.TaskID)
	if len(trs) != len(states)+2 { // + SUBMITTED (submit event) and ACCEPTED (accept event)
		t.Errorf("transitions %d vs audited states %d", len(trs), len(states))
	}
	req := auditEvents(t, mac)
	got := map[string]bool{}
	for _, e := range req {
		if e.TaskID == r.TaskID || e.TaskID == r2.TaskID {
			got[e.Event+"/"+e.State] = true
		}
	}
	for _, e := range req {
		if e.TaskID == r.TaskID && len(e.Argv) == 0 {
			t.Errorf("requester audit event without argv: %+v", e)
		}
	}
	for _, k := range []string{audit.EvSubmit + "/SUBMITTED", audit.EvAccept + "/ACCEPTED", audit.EvState + "/RUNNING", audit.EvState + "/COMPLETED",
		audit.EvCancel + "/", audit.EvArtifactFetch + "/"} {
		if !got[k] {
			t.Errorf("requester audit lacks %s (have %v)", k, got)
		}
	}
}

// Regression (Windows report #4): the requester timeline only records state
// changes — no SUBMITTED/SUBMITTED (pending approval) or
// FINALIZING/FINALIZING (copy-back) duplicates.
func assertNoDuplicateStates(t *testing.T, n *node, id string) {
	t.Helper()
	trs, _ := n.d.st.Transitions(context.Background(), id)
	for i := 1; i < len(trs); i++ {
		if trs[i].State == trs[i-1].State {
			t.Fatalf("%s: duplicate consecutive state %s in timeline %+v", n.name, trs[i].State, trs)
		}
	}
}

func TestMirrorTimelineHasNoDuplicates(t *testing.T) {
	py := pythonPath(t)
	mac := newNode(t, "mac", nil)
	win := newNode(t, "win", nil)
	pairNodes(t, mac, win)
	win.mustCall("set_policy", map[string]string{"peer": "mac", "policy": "ask"}, nil)
	p := newProject(t, 1000)
	r := submit(t, mac, win, p, makeSpec(py, 1, 0, ""))
	waitFor(t, 5*time.Second, "pending mirrored", func() bool { return mac.task(r.TaskID).PendingApproval })
	var tv TaskView
	win.mustCall("approve", map[string]string{"id": r.TaskID}, &tv)
	waitState(t, mac, r.TaskID, task.Completed)
	assertNoDuplicateStates(t, mac, r.TaskID)
	assertNoDuplicateStates(t, win, r.TaskID)
	if d := mac.task(r.TaskID).Detail; d != "artifacts copied back" {
		t.Fatalf("detail %q", d)
	}
}

// Regression (Windows report #2): a cancel issued on the executor is
// attributed to the executor on both sides.
func TestCancelAttribution(t *testing.T) {
	py := pythonPath(t)
	mac := newNode(t, "mac", nil)
	win := newNode(t, "win", nil)
	pairNodes(t, mac, win)
	p := newProject(t, 1000)
	for _, c := range []struct {
		who  *node
		want string
	}{{win, "cancelled on the executor"}, {mac, "cancelled by the requester"}} {
		r := submit(t, mac, win, p, makeSpec(py, 1000, 0.1, ""))
		waitState(t, win, r.TaskID, task.Running)
		if pid := win.task(r.TaskID).PID; pid <= 0 {
			t.Fatal("executor has no pid for a running task")
		}
		var tv TaskView
		c.who.mustCall("cancel", map[string]string{"id": r.TaskID}, &tv)
		et := waitState(t, win, r.TaskID, task.Cancelled)
		mt := waitState(t, mac, r.TaskID, task.Cancelled)
		if et.Detail != c.want || mt.Detail != c.want {
			t.Fatalf("cancel via %s: executor %q requester %q, want %q", c.who.name, et.Detail, mt.Detail, c.want)
		}
	}
}
