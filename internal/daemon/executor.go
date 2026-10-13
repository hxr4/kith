package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"kith/internal/artifacts"
	"kith/internal/audit"
	"kith/internal/caps"
	"kith/internal/config"
	"kith/internal/peer"
	"kith/internal/protocol"
	"kith/internal/store"
	"kith/internal/task"
	"kith/internal/transfer"
)

type runningTask struct {
	id     string
	ctx    context.Context
	cancel context.CancelFunc

	mu      sync.Mutex
	proc    *task.Proc
	timeout bool
}

func boolp(b bool) *bool { return &b }

// stateMsg builds the task.state message for an executor task.
func (d *Daemon) stateMsg(t store.Task, seq int) protocol.TaskState {
	return protocol.TaskState{TaskID: t.ID, State: t.State, Detail: t.Detail, Attempt: t.Attempt, TS: t.UpdatedAt, Seq: seq,
		PendingApproval: t.PendingApproval, Code: t.Code, Remediation: t.Remediation, LogSize: t.LogSize}
}

// execTransition journals an executor state change (edge-checked), audits
// it, then (and only then) announces it to the requester.
func (d *Daemon) execTransition(ctx context.Context, id, to, detail string, mod func(*store.Change)) (store.Task, error) {
	ch := store.Change{State: to, Detail: detail}
	if mod != nil {
		mod(&ch)
	}
	t, seq, err := d.st.Transition(ctx, id, ch, func(cur store.Task) error {
		if cur.Role != store.RoleExecutor {
			return fmt.Errorf("task %s is not executed here", id)
		}
		return task.CheckTransition(cur.State, to)
	})
	if err != nil {
		return t, err
	}
	d.auditTask(audit.EvState, t, to, detail)
	d.log.Info("task state", "task", id, "state", to, "detail", detail)
	d.notify()
	if c := d.conn(t.PeerFP); c != nil {
		c.Send(protocol.MsgTaskState, d.stateMsg(t, seq))
	}
	return t, nil
}

func rejectMsg(id string, e *protocol.Error, seq int) protocol.TaskReject {
	return protocol.TaskReject{TaskID: id, Code: e.Code, Message: e.Message, Remediation: e.Remediation, QueueLen: e.QueueLen, Seq: seq}
}

// onTaskSubmit handles task.submit (executor side): validate, record,
// verify capabilities BEFORE any transfer, then accept/reject/pend.
func (d *Daemon) onTaskSubmit(c *peer.Conn, env protocol.Envelope) {
	ctx := d.ctx
	var sub protocol.TaskSubmit
	if err := json.Unmarshal(env.Payload, &sub); err != nil {
		c.Reply(env.ID, protocol.MsgTaskReject, protocol.TaskReject{Code: protocol.CodeProtocolError, Message: err.Error()})
		return
	}
	spec, err := task.ParseJSON(sub.Spec)
	if err == nil && spec.ID == "" {
		err = protocol.Errf(protocol.CodeProtocolError, "task.submit without id")
	}
	if err != nil {
		e := protocol.AsError(err)
		id := ""
		if spec != nil {
			id = spec.ID
		}
		c.Reply(env.ID, protocol.MsgTaskReject, rejectMsg(id, e, 0))
		return
	}
	peerRec, err := d.st.GetPeer(ctx, c.PeerFP)
	if err != nil {
		c.Reply(env.ID, protocol.MsgTaskReject, rejectMsg(spec.ID, protocol.Errf(protocol.CodeAuthFailed, "not paired"), 0))
		return
	}
	d.gateMu.Lock()
	defer d.gateMu.Unlock()

	// Idempotent resubmit: answer with the existing record.
	if ex, err := d.st.GetTask(ctx, spec.ID); err == nil {
		if ex.PeerFP != c.PeerFP || ex.Role != store.RoleExecutor {
			c.Reply(env.ID, protocol.MsgTaskReject, rejectMsg(spec.ID, protocol.Errf(protocol.CodeProtocolError, "task id already in use"), 0))
			return
		}
		seq, _ := d.st.LastSeq(ctx, ex.ID)
		if ex.State == task.Rejected {
			c.Reply(env.ID, protocol.MsgTaskReject, protocol.TaskReject{TaskID: ex.ID, Code: ex.Code, Message: ex.Detail, Remediation: ex.Remediation, Seq: seq})
		} else {
			c.Reply(env.ID, protocol.MsgTaskAccept, protocol.TaskAccept{TaskID: ex.ID, AcceptedAt: ex.CreatedAt, PendingApproval: ex.PendingApproval, State: ex.State, Seq: seq})
		}
		return
	}
	ask := peerRec.Policy != config.PolicyAuto
	t, _, err := d.st.CreateTask(ctx, store.Task{ID: spec.ID, Role: store.RoleExecutor, PeerFP: c.PeerFP, Project: spec.Project,
		Name: spec.Name, SpecJSON: string(spec.JSON()), State: task.Submitted, PendingApproval: ask, WSDir: d.wsDir(spec.ID)})
	if err != nil {
		c.Reply(env.ID, protocol.MsgTaskReject, rejectMsg(spec.ID, protocol.AsError(err), 0))
		return
	}
	d.audit.Write(audit.Event{Event: audit.EvSubmit, TaskID: t.ID, PeerFP: c.PeerFP, Role: t.Role, Argv: spec.Command})
	if e := d.gate(ctx, spec, ask); e != nil {
		d.reject(ctx, t.ID, e, func(seq int) { c.Reply(env.ID, protocol.MsgTaskReject, rejectMsg(t.ID, e, seq)) })
		return
	}
	if ask {
		d.notify()
		d.log.Info("task awaiting approval", "task", t.ID, "argv", strings.Join(spec.Command, " "))
		c.Reply(env.ID, protocol.MsgTaskAccept, protocol.TaskAccept{TaskID: t.ID, AcceptedAt: t.CreatedAt, PendingApproval: true, State: task.Submitted, Seq: 1})
		return
	}
	t, seq, err := d.st.Transition(ctx, t.ID, store.Change{State: task.Accepted, PendingApproval: boolp(false)}, nil)
	if err != nil {
		c.Reply(env.ID, protocol.MsgTaskReject, rejectMsg(spec.ID, protocol.AsError(err), 0))
		return
	}
	d.audit.Write(audit.Event{Event: audit.EvAccept, TaskID: t.ID, PeerFP: c.PeerFP, Role: t.Role, Argv: spec.Command})
	d.notify()
	c.Reply(env.ID, protocol.MsgTaskAccept, protocol.TaskAccept{TaskID: t.ID, AcceptedAt: t.UpdatedAt, State: task.Accepted, Seq: seq})
	d.startPipeline(t.ID)
}

// reject journals SUBMITTED → REJECTED, then calls announce with the seq.
func (d *Daemon) reject(ctx context.Context, id string, e *protocol.Error, announce func(seq int)) {
	detail := e.Code + ": " + e.Message
	t, seq, err := d.st.Transition(ctx, id, store.Change{State: task.Rejected, Detail: detail, Code: e.Code, Remediation: e.Remediation,
		PendingApproval: boolp(false)}, func(cur store.Task) error { return task.CheckTransition(cur.State, task.Rejected) })
	if err != nil {
		d.log.Warn("reject", "task", id, "err", err)
		return
	}
	d.auditTask(audit.EvReject, t, task.Rejected, detail)
	d.log.Info("task rejected", "task", id, "code", e.Code, "msg", e.Message)
	d.notify()
	if announce != nil {
		announce(seq)
	} else if c := d.conn(t.PeerFP); c != nil {
		c.Send(protocol.MsgTaskState, d.stateMsg(t, seq))
	}
}

// gate is the synchronous accept-time capability check (§10.2).
func (d *Daemon) gate(ctx context.Context, spec *task.Spec, skipBusy bool) *protocol.Error {
	pk := make([]string, 0, len(spec.Env.Packages))
	for n := range spec.Env.Packages {
		pk = append(pk, n)
	}
	sort.Strings(pk)
	m, err := d.caps.Probe(ctx, caps.Request{Packages: pk, DiskPath: filepath.Join(d.home, "work")})
	if err != nil {
		return protocol.Errf(protocol.CodeInternal, "capability probe failed: %v", err)
	}
	if e := caps.Check(m, spec.Env, spec.InputsBytes()); e != nil {
		return e
	}
	if !skipBusy {
		n, _ := d.activeCount(ctx, spec.ID)
		if n >= 1 {
			e := protocol.Errf(protocol.CodeBusy, "executor is already running a task (queue_len %d)", n).
				WithFix("wait for the running task to finish, then resubmit with a new task id")
			e.QueueLen = &n
			return e
		}
	}
	if len(spec.Env.Probe) > 0 {
		scratch := filepath.Join(d.home, "scratch", spec.ID)
		os.MkdirAll(scratch, 0o700)
		defer os.RemoveAll(scratch)
		if e := caps.RunProbe(ctx, spec.Env.Probe, scratch); e != nil {
			return e
		}
	}
	return nil
}

// Approve / Deny (ask policy).
func (d *Daemon) approve(ctx context.Context, id string) (store.Task, error) {
	d.gateMu.Lock()
	defer d.gateMu.Unlock()
	t, err := d.st.GetTask(ctx, id)
	if err != nil {
		return t, protocol.Errf(protocol.CodeNotFound, "no task %s", id)
	}
	if t.Role != store.RoleExecutor || t.State != task.Submitted || !t.PendingApproval {
		return t, protocol.Errf(protocol.CodeNotFound, "task %s is not awaiting approval here (state %s)", id, t.State)
	}
	spec, err := task.ParseJSON([]byte(t.SpecJSON))
	if err != nil {
		return t, err
	}
	d.audit.Write(audit.Event{Event: audit.EvApprove, TaskID: id, PeerFP: t.PeerFP, Role: t.Role, Argv: spec.Command})
	if e := d.gate(ctx, spec, false); e != nil {
		d.reject(ctx, id, e, nil)
		t, _ = d.st.GetTask(ctx, id)
		return t, nil
	}
	t, err = d.execTransition(ctx, id, task.Accepted, "approved", func(c *store.Change) { c.PendingApproval = boolp(false) })
	if err != nil {
		return t, err
	}
	d.audit.Write(audit.Event{Event: audit.EvAccept, TaskID: id, PeerFP: t.PeerFP, Role: t.Role, Argv: spec.Command})
	d.startPipeline(id)
	return t, nil
}

func (d *Daemon) deny(ctx context.Context, id, why string) (store.Task, error) {
	d.gateMu.Lock()
	defer d.gateMu.Unlock()
	t, err := d.st.GetTask(ctx, id)
	if err != nil {
		return t, protocol.Errf(protocol.CodeNotFound, "no task %s", id)
	}
	if t.Role != store.RoleExecutor || t.State != task.Submitted {
		return t, protocol.Errf(protocol.CodeNotFound, "task %s is not awaiting approval here (state %s)", id, t.State)
	}
	d.auditTask(audit.EvDeny, t, "", why)
	d.reject(ctx, id, protocol.Errf(protocol.CodePolicyDenied, "%s", why).WithFix("ask the owner of the executor to run `kith approve`, or set the peer policy to auto"), nil)
	return d.st.GetTask(ctx, id)
}

// expiryLoop rejects unapproved tasks after ApprovalTTL (§6).
func (d *Daemon) expiryLoop() {
	every := d.opt.ApprovalTTL / 10
	if every > 30*time.Second {
		every = 30 * time.Second
	}
	if every < 50*time.Millisecond {
		every = 50 * time.Millisecond
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-d.ctx.Done():
			return
		case <-t.C:
			ts, _ := d.st.ListTasks(d.ctx, store.TaskFilter{Role: store.RoleExecutor, States: []string{task.Submitted}})
			for _, tk := range ts {
				if tk.PendingApproval && time.Since(store.ParseTS(tk.CreatedAt)) > d.opt.ApprovalTTL {
					d.deny(d.ctx, tk.ID, fmt.Sprintf("not approved within %s", d.opt.ApprovalTTL))
				}
			}
		}
	}
}

// startPipeline drives an executor task from its current state.
func (d *Daemon) startPipeline(id string) {
	d.mu.Lock()
	if d.running[id] != nil || d.ctx.Err() != nil {
		d.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(d.ctx)
	rt := &runningTask{id: id, ctx: ctx, cancel: cancel}
	d.running[id] = rt
	d.mu.Unlock()
	d.goFn(func() {
		defer func() {
			cancel()
			d.mu.Lock()
			delete(d.running, id)
			d.mu.Unlock()
			d.refreshLocalCaps(d.ctx)
		}()
		defer func() {
			if p := recover(); p != nil {
				d.log.Error("pipeline panic", "task", id, "panic", p)
				d.execTransition(context.Background(), id, task.Failed, fmt.Sprintf("%s: internal error", protocol.CodeInternal), nil)
			}
		}()
		d.pipeline(rt)
	})
}

func (d *Daemon) pipeline(rt *runningTask) {
	ctx := rt.ctx
	for ctx.Err() == nil {
		t, err := d.st.GetTask(ctx, rt.id)
		if err != nil {
			return
		}
		spec, err := task.ParseJSON([]byte(t.SpecJSON))
		if err != nil {
			d.execTransition(ctx, t.ID, task.Failed, "bad spec: "+err.Error(), nil)
			return
		}
		switch t.State {
		case task.Accepted:
			if _, err := d.execTransition(ctx, t.ID, task.Transferring, "", nil); err != nil {
				return
			}
		case task.Transferring:
			if err := d.pullInputs(ctx, t, spec); err != nil {
				if ctx.Err() != nil {
					return // cancelled or shutting down; state already handled
				}
				e := protocol.AsError(err)
				d.execTransition(ctx, t.ID, task.Failed, e.Code+": "+e.Message, func(c *store.Change) { c.Code = e.Code })
				return
			}
			if _, err := d.execTransition(ctx, t.ID, task.Running, "", nil); err != nil {
				return
			}
		case task.Running:
			if !d.runProcess(rt, t, spec) {
				return
			}
		case task.Finalizing:
			d.finalize(ctx, t, spec)
			return
		default:
			return
		}
	}
}

// pullInputs fetches every offered input into the workspace, resuming across
// disconnects. TRANSFER_ERROR is retried (whole file) up to 3 times.
func (d *Daemon) pullInputs(ctx context.Context, t store.Task, spec *task.Spec) error {
	ws := d.wsDir(t.ID)
	if err := os.MkdirAll(ws, 0o700); err != nil {
		return err
	}
	for _, o := range spec.Inputs.Offers {
		dest := filepath.Join(ws, filepath.FromSlash(o.Path))
		if tr, err := d.st.GetTransfer(ctx, t.ID, o.SHA256, store.DirIn); err == nil && tr.BytesDone == o.Size {
			if st, err := os.Stat(dest); err == nil && st.Size() == o.Size {
				continue
			}
		}
		d.st.UpsertTransfer(ctx, store.Transfer{TaskID: t.ID, SHA256: o.SHA256, Path: o.Path, Size: o.Size, Direction: store.DirIn})
		failures := 0
		for {
			c, err := d.waitConn(ctx, t.PeerFP)
			if err != nil {
				return err
			}
			err = d.xfer.Fetch(ctx, c, t.ID, o, dest, func(n int64) {
				d.st.UpsertTransfer(context.Background(), store.Transfer{TaskID: t.ID, SHA256: o.SHA256, Path: o.Path, Size: o.Size, BytesDone: n, Direction: store.DirIn})
			})
			if err == nil {
				break
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			switch protocol.CodeOf(err) {
			case protocol.CodePeerUnreachable:
				d.log.Info("input transfer interrupted; will resume", "task", t.ID, "file", o.Path)
				time.Sleep(200 * time.Millisecond)
				continue
			case protocol.CodeTransferError:
				failures++
				d.log.Warn("input transfer failed", "task", t.ID, "file", o.Path, "attempt", failures, "err", err)
				if failures >= 3 {
					return err
				}
				continue
			default:
				return err
			}
		}
	}
	return nil
}

// runProcess executes the command; returns true to continue the pipeline.
func (d *Daemon) runProcess(rt *runningTask, t store.Task, spec *task.Spec) bool {
	ctx := rt.ctx
	ws := d.wsDir(t.ID)
	os.MkdirAll(ws, 0o700)
	lf, err := os.OpenFile(d.logPath(t.ID), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		d.execTransition(ctx, t.ID, task.Failed, "INTERNAL: open log: "+err.Error(), nil)
		return false
	}
	if t.Attempt > 1 {
		fmt.Fprintf(lf, "\n=== kith: attempt %d ===\n", t.Attempt)
	}
	env := append(os.Environ(), "KITH_TASK_ID="+t.ID)
	p, err := task.Start(spec.Command, ws, env, lf)
	lf.Close()
	if err != nil {
		d.execTransition(ctx, t.ID, task.Failed, fmt.Sprintf("could not start %q: %v", spec.Command[0], err), func(c *store.Change) {
			c.Code = protocol.CodeNoMatch
			c.Remediation = "make sure " + spec.Command[0] + " is on the executor daemon's PATH"
		})
		return false
	}
	d.st.SetPID(ctx, t.ID, p.PID, store.Now())
	rt.mu.Lock()
	rt.proc = p
	rt.mu.Unlock()
	d.log.Info("task process started", "task", t.ID, "pid", p.PID, "argv", strings.Join(spec.Command, " "))
	if c := d.conn(t.PeerFP); c != nil {
		d.startLogPump(c, t.ID)
	}
	var timeout <-chan time.Time
	if spec.Policy.TimeoutMinutes > 0 {
		left := time.Duration(spec.Policy.TimeoutMinutes)*d.opt.TimeoutUnit - time.Since(store.ParseTS(t.UpdatedAt))
		if left < 0 {
			left = 0
		}
		tm := time.NewTimer(left)
		defer tm.Stop()
		timeout = tm.C
	}
	select {
	case <-p.Done():
	case <-timeout:
		rt.mu.Lock()
		rt.timeout = true
		rt.mu.Unlock()
		d.execTransition(ctx, t.ID, task.Cancelling, protocol.CodeTimeout, nil)
		p.Terminate(d.opt.CancelGrace)
		<-p.Done()
	case <-ctx.Done():
		// Shutdown: the shutdown path marks INTERRUPTED and kills the tree.
		<-p.Done()
		p.Cleanup()
		return false
	}
	p.Cleanup()
	rt.mu.Lock()
	rt.proc = nil
	timedOut := rt.timeout
	rt.mu.Unlock()
	if st, err := os.Stat(d.logPath(t.ID)); err == nil {
		d.st.SetLogSize(context.Background(), t.ID, st.Size())
	}
	cur, err := d.st.GetTask(context.Background(), t.ID)
	if err != nil {
		return false
	}
	switch cur.State {
	case task.Cancelling:
		if timedOut {
			d.execTransition(context.Background(), t.ID, task.Failed, protocol.CodeTimeout, func(c *store.Change) {
				c.Code = protocol.CodeTimeout
				c.Remediation = "raise policy.timeout_minutes"
			})
		} else {
			d.execTransition(context.Background(), t.ID, task.Cancelled, "cancelled by requester", func(c *store.Change) { c.Code = protocol.CodeCancelledByPeer })
		}
		return false
	case task.Running:
		if code := p.ExitCode(); code != 0 {
			var mod func(*store.Change)
			if runtime.GOOS == "windows" && code == 9009 {
				// 9009 = the Microsoft Store "python"/"python3" alias stub ran instead of a real interpreter.
				mod = func(c *store.Change) {
					c.Remediation = "argv[0] " + spec.Command[0] + " resolved to a Windows App Execution Alias stub; use \"python\" (or a full path), " +
						"or disable the alias in Settings > Apps > Advanced app settings > App execution aliases"
				}
			}
			d.execTransition(ctx, t.ID, task.Failed, fmt.Sprintf("process exited with status %d", code), mod)
			return false
		}
		_, err := d.execTransition(ctx, t.ID, task.Finalizing, "", nil)
		return err == nil
	default:
		return false // INTERRUPTED by shutdown, etc.
	}
}

// finalize globs outputs, registers artifacts, offers them, completes.
func (d *Daemon) finalize(ctx context.Context, t store.Task, spec *task.Spec) {
	col, err := artifacts.Collect(d.wsDir(t.ID), spec.Outputs.Include, *spec.Outputs.CopyBack, spec.Outputs.RetainCapGB, d.id.FP, t.ID)
	if err != nil {
		d.execTransition(ctx, t.ID, task.Failed, "collect outputs: "+err.Error(), nil)
		return
	}
	for _, a := range col.Artifacts {
		if err := d.st.UpsertArtifact(ctx, a); err != nil {
			d.execTransition(ctx, t.ID, task.Failed, "register artifacts: "+err.Error(), nil)
			return
		}
	}
	cur, _ := d.st.GetTask(ctx, t.ID)
	if off, ok := d.artifactOfferFor(ctx, cur); ok {
		if c := d.conn(t.PeerFP); c != nil {
			c.Send(protocol.MsgArtifactOffer, off)
		}
	}
	detail := fmt.Sprintf("%d artifacts", len(col.Artifacts))
	if !col.CopyBack && len(col.Artifacts) > 0 {
		detail += " retained on executor (fetch on demand)"
	}
	d.execTransition(ctx, t.ID, task.Completed, detail, nil)
}

func (d *Daemon) artifactOfferFor(ctx context.Context, t store.Task) (protocol.ArtifactOffer, bool) {
	as, _ := d.st.ListArtifacts(ctx, t.ID)
	if len(as) == 0 {
		return protocol.ArtifactOffer{}, false
	}
	off := protocol.ArtifactOffer{TaskID: t.ID}
	for _, a := range as {
		off.Artifacts = append(off.Artifacts, protocol.Artifact{Path: a.Path, Size: a.Size, SHA256: a.SHA256, Remote: !a.CopyBack})
	}
	return off, true
}

// startLogPump streams the task log to c from seq 0 until the task is
// terminal and fully sent, or the connection closes.
func (d *Daemon) startLogPump(c *peer.Conn, id string) {
	k := pumpKey{c, id}
	d.mu.Lock()
	if d.pumps[k] {
		d.mu.Unlock()
		return
	}
	d.pumps[k] = true
	d.mu.Unlock()
	d.goFn(func() {
		defer func() {
			d.mu.Lock()
			delete(d.pumps, k)
			d.mu.Unlock()
		}()
		var off int64
		buf := make([]byte, 256<<10)
		for {
			t, err := d.st.GetTask(d.ctx, id)
			if err != nil {
				return
			}
			terminal := task.IsTerminal(t.State)
			n := 0
			if f, err := os.Open(d.logPath(id)); err == nil {
				n, err = f.ReadAt(buf, off)
				f.Close()
				if err != nil && !errors.Is(err, io.EOF) {
					n = 0
				}
			}
			if n > 0 {
				hdr, err := protocol.EncodeLogChunkHeader(id, uint64(off))
				if err != nil {
					return
				}
				if err := c.SendFrame(protocol.FrameLogChunk, hdr, buf[:n]); err != nil {
					return
				}
				off += int64(n)
				continue
			}
			if terminal {
				return
			}
			select {
			case <-c.Done():
				return
			case <-d.ctx.Done():
				return
			case <-time.After(250 * time.Millisecond):
			}
		}
	})
}

// onRemoteCancel handles task.cancel from the requester.
func (d *Daemon) onRemoteCancel(peerFP, id string) {
	t, err := d.st.GetTask(d.ctx, id)
	if err != nil || t.Role != store.RoleExecutor || t.PeerFP != peerFP {
		return
	}
	d.auditTask(audit.EvCancel, t, "", "requested by peer")
	d.cancelExec(d.ctx, id)
}

// cancelExec cancels an executor task according to its state.
func (d *Daemon) cancelExec(ctx context.Context, id string) {
	t, err := d.st.GetTask(ctx, id)
	if err != nil {
		return
	}
	d.mu.Lock()
	rt := d.running[id]
	d.mu.Unlock()
	switch t.State {
	case task.Submitted:
		d.reject(ctx, id, protocol.Errf(protocol.CodeCancelledByPeer, "cancelled by requester before acceptance"), nil)
	case task.Accepted:
		if rt != nil {
			rt.cancel()
		}
		d.execTransition(ctx, id, task.Transferring, "", nil)
		d.execTransition(ctx, id, task.Failed, protocol.CodeCancelledByPeer, func(c *store.Change) { c.Code = protocol.CodeCancelledByPeer })
	case task.Transferring, task.Interrupted:
		if rt != nil {
			rt.cancel()
		}
		d.execTransition(ctx, id, task.Failed, protocol.CodeCancelledByPeer, func(c *store.Change) { c.Code = protocol.CodeCancelledByPeer })
	case task.Running:
		if _, err := d.execTransition(ctx, id, task.Cancelling, "cancel requested", nil); err != nil {
			return
		}
		if rt != nil {
			rt.mu.Lock()
			p := rt.proc
			rt.mu.Unlock()
			if p != nil {
				go p.Terminate(d.opt.CancelGrace)
				return
			}
		}
		// No live process (should not happen): finish the cancel directly.
		d.execTransition(ctx, id, task.Cancelled, "cancelled by requester", func(c *store.Change) { c.Code = protocol.CodeCancelledByPeer })
	}
}

// recover applies startup recovery (§9.4).
func (d *Daemon) recover(ctx context.Context) error {
	ts, err := d.st.ListTasks(ctx, store.TaskFilter{Role: store.RoleExecutor, States: task.NonTerminal})
	if err != nil {
		return err
	}
	for _, t := range ts {
		switch t.State {
		case task.Running, task.Finalizing:
			task.KillOrphan(t.PID)
			d.execTransition(ctx, t.ID, task.Interrupted, "daemon restarted", nil)
		case task.Cancelling:
			task.KillOrphan(t.PID)
			d.execTransition(ctx, t.ID, task.Cancelled, "cancelled (completed after daemon restart)", func(c *store.Change) { c.Code = protocol.CodeCancelledByPeer })
		}
	}
	ts, _ = d.st.ListTasks(ctx, store.TaskFilter{Role: store.RoleExecutor, States: task.NonTerminal})
	for _, t := range ts {
		switch t.State {
		case task.Interrupted:
			d.applyInterruptPolicy(ctx, t)
		case task.Accepted, task.Transferring:
			// Transfers are resumable; continue where we stopped (DECISIONS.md D-016).
			d.startPipeline(t.ID)
		}
	}
	return nil
}

func (d *Daemon) applyInterruptPolicy(ctx context.Context, t store.Task) {
	spec, err := task.ParseJSON([]byte(t.SpecJSON))
	if err != nil {
		d.execTransition(ctx, t.ID, task.Failed, "interrupted (bad spec)", nil)
		return
	}
	pol := spec.Policy.OnInterrupt
	if pol == task.OnInterruptFail {
		d.execTransition(ctx, t.ID, task.Failed, "interrupted", nil)
		return
	}
	next := t.Attempt + 1
	if next > task.MaxAttempts {
		d.execTransition(ctx, t.ID, task.Failed, fmt.Sprintf("interrupted; attempt cap %d reached", task.MaxAttempts), nil)
		return
	}
	ws := d.wsDir(t.ID)
	how := "restart"
	if pol == task.OnInterruptResume && spec.Policy.Checkpoint.Pattern != "" {
		if m, _ := task.ExpandGlobs(ws, []string{spec.Policy.Checkpoint.Pattern}); len(m) > 0 {
			how = "resume-if-checkpoint (found " + m[len(m)-1] + ")"
		}
	}
	if strings.HasPrefix(how, "restart") {
		if err := cleanWorkspace(ws, spec); err != nil {
			d.execTransition(ctx, t.ID, task.Failed, "interrupted; cannot restart: "+err.Error(), func(c *store.Change) {
				c.Remediation = "resubmit the task with a new id"
			})
			return
		}
	}
	if _, err := d.execTransition(ctx, t.ID, task.Running, "re-run after interrupt: "+how, func(c *store.Change) { c.Attempt = next }); err != nil {
		return
	}
	d.startPipeline(t.ID)
}

// cleanWorkspace removes everything except verified inputs (restart policy).
func cleanWorkspace(ws string, spec *task.Spec) error {
	keep := map[string]protocol.FileOffer{}
	for _, o := range spec.Inputs.Offers {
		keep[o.Path] = o
	}
	all, err := task.ExpandGlobs(ws, []string{"**"})
	if err != nil {
		return err
	}
	for _, f := range all {
		if _, ok := keep[f]; !ok {
			os.Remove(filepath.Join(ws, filepath.FromSlash(f)))
		}
	}
	for _, o := range spec.Inputs.Offers {
		sha, _, err := transfer.SHAFile(filepath.Join(ws, filepath.FromSlash(o.Path)))
		if err != nil || sha != o.SHA256 {
			return fmt.Errorf("input %s missing or modified by the previous attempt", o.Path)
		}
	}
	removeEmptyDirs(ws)
	return nil
}

func removeEmptyDirs(root string) {
	var dirs []string
	filepath.WalkDir(root, func(p string, de os.DirEntry, err error) error {
		if err == nil && de.IsDir() && p != root {
			dirs = append(dirs, p)
		}
		return nil
	})
	for i := len(dirs) - 1; i >= 0; i-- {
		os.Remove(dirs[i])
	}
}
