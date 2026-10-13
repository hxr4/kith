package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"kith/internal/caps"
	"kith/internal/peer"
	"kith/internal/protocol"
	"kith/internal/store"
	"kith/internal/task"
	"kith/internal/transfer"
)

// HandleCtrl dispatches CTRL messages. It runs on the connection reader, so
// anything slow is moved to a goroutine; ordering-sensitive mirror updates
// are applied inline.
func (d *Daemon) HandleCtrl(c *peer.Conn, env protocol.Envelope) {
	switch env.Type {
	case protocol.MsgCaps:
		var m caps.Manifest
		if json.Unmarshal(env.Payload, &m) == nil {
			d.mu.Lock()
			d.remoteCaps[c.PeerFP] = m
			d.mu.Unlock()
		}
	case protocol.MsgTaskSubmit:
		d.goFn(func() { d.onTaskSubmit(c, env) })
	case protocol.MsgTaskAccept:
		var a protocol.TaskAccept
		if json.Unmarshal(env.Payload, &a) == nil {
			st := a.State
			if st == "" {
				st = task.Accepted
				if a.PendingApproval {
					st = task.Submitted
				}
			}
			d.mirrorApply(c.PeerFP, protocol.TaskState{TaskID: a.TaskID, State: st, PendingApproval: a.PendingApproval, TS: a.AcceptedAt, Seq: a.Seq})
		}
	case protocol.MsgTaskReject:
		var r protocol.TaskReject
		if json.Unmarshal(env.Payload, &r) == nil {
			d.mirrorApply(c.PeerFP, protocol.TaskState{TaskID: r.TaskID, State: task.Rejected, Code: r.Code,
				Detail: r.Code + ": " + r.Message, Remediation: r.Remediation, Seq: r.Seq})
		}
	case protocol.MsgTaskState:
		var s protocol.TaskState
		if json.Unmarshal(env.Payload, &s) == nil {
			d.mirrorApply(c.PeerFP, s)
		}
	case protocol.MsgArtifactOffer:
		var a protocol.ArtifactOffer
		if json.Unmarshal(env.Payload, &a) == nil {
			d.onArtifactOffer(c.PeerFP, a)
		}
	case protocol.MsgTaskCancel:
		var tc protocol.TaskCancel
		if json.Unmarshal(env.Payload, &tc) == nil {
			d.goFn(func() { d.onRemoteCancel(c.PeerFP, tc.TaskID) })
		}
	case protocol.MsgFileRequest:
		var r protocol.FileRequest
		if json.Unmarshal(env.Payload, &r) == nil {
			d.goFn(func() { d.serveFile(c, r) })
		}
	case protocol.MsgFileError:
		var fe protocol.FileError
		if json.Unmarshal(env.Payload, &fe) == nil {
			d.xfer.OnError(c, fe)
		}
	case protocol.MsgFileOffer:
		var fo protocol.FileOfferMsg
		if json.Unmarshal(env.Payload, &fo) == nil {
			d.checkFileOffer(c.PeerFP, fo)
		}
	case protocol.MsgFileDone:
		var fd protocol.FileDone
		if json.Unmarshal(env.Payload, &fd) == nil && !fd.OK {
			d.log.Warn("peer reported a failed transfer (hash mismatch); it will retry", "task", fd.TaskID, "sha256", fd.SHA256)
		}
	case protocol.MsgSyncRequest:
		var r protocol.SyncRequest
		if json.Unmarshal(env.Payload, &r) == nil {
			d.goFn(func() { d.onSyncRequest(c, env.ID, r) })
		}
	case protocol.MsgSyncState:
		var s protocol.SyncState
		if json.Unmarshal(env.Payload, &s) == nil {
			d.onSyncState(c, s)
		}
	default:
		d.log.Debug("ignoring unknown message type", "type", env.Type)
	}
}

func (d *Daemon) HandleFileChunk(c *peer.Conn, ch protocol.FileChunk) {
	if d.opt.OnFileChunk != nil {
		d.opt.OnFileChunk(c)
		if !c.Alive() {
			return
		}
	}
	d.xfer.OnChunk(c, ch)
}

// HandleLogChunk appends to the requester's mirror log, deduplicating by
// byte offset (replay from seq 0 after reconnect is idempotent).
func (d *Daemon) HandleLogChunk(c *peer.Conn, ch protocol.LogChunk) {
	t, err := d.st.GetTask(d.ctx, ch.TaskID)
	if err != nil || t.Role != store.RoleRequester || t.PeerFP != c.PeerFP {
		return
	}
	f, err := os.OpenFile(d.logPath(ch.TaskID), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return
	}
	cur := uint64(st.Size())
	end := ch.Seq + uint64(len(ch.Data))
	if ch.Seq > cur || end <= cur {
		return // gap (a later replay fills it) or duplicate
	}
	if _, err := f.WriteAt(ch.Data[cur-ch.Seq:], int64(cur)); err == nil {
		d.notify()
	}
}

// onConnect pushes caps and asks the peer to sync.
func (d *Daemon) onConnect(c *peer.Conn) {
	m := d.refreshLocalCaps(d.ctx)
	c.Send(protocol.MsgCaps, m)
	mirrors, _ := d.st.ListTasks(d.ctx, store.TaskFilter{Role: store.RoleRequester, PeerFP: c.PeerFP, States: task.NonTerminal})
	var ids []string
	for _, t := range mirrors {
		ids = append(ids, t.ID)
	}
	// Completed tasks whose artifacts are still being copied back.
	done, _ := d.st.ListTasks(d.ctx, store.TaskFilter{Role: store.RoleRequester, PeerFP: c.PeerFP, States: []string{task.Completed}, Limit: 50})
	for _, t := range done {
		if d.pendingCopyBack(t.ID) {
			ids = append(ids, t.ID)
		}
	}
	c.Send(protocol.MsgSyncRequest, protocol.SyncRequest{TaskIDs: ids})
}

// onSyncRequest (executor side): full push of non-terminal tasks for that
// peer plus any task the requester asked about, with artifact records.
// Log streams are replayed from seq 0.
func (d *Daemon) onSyncRequest(c *peer.Conn, id string, r protocol.SyncRequest) {
	ctx := d.ctx
	ts, _ := d.st.ListTasks(ctx, store.TaskFilter{Role: store.RoleExecutor, PeerFP: c.PeerFP, States: task.NonTerminal})
	seen := map[string]bool{}
	for _, t := range ts {
		seen[t.ID] = true
	}
	var unknown []string
	for _, tid := range r.TaskIDs {
		if seen[tid] {
			continue
		}
		t, err := d.st.GetTask(ctx, tid)
		if err != nil || t.Role != store.RoleExecutor || t.PeerFP != c.PeerFP {
			unknown = append(unknown, tid)
			continue
		}
		seen[tid] = true
		ts = append(ts, t)
	}
	out := protocol.SyncState{Unknown: unknown}
	out.Tasks = []protocol.TaskState{}
	out.Artifacts = []protocol.ArtifactOffer{}
	for _, t := range ts {
		seq, _ := d.st.LastSeq(ctx, t.ID)
		out.Tasks = append(out.Tasks, d.stateMsg(t, seq))
		if off, ok := d.artifactOfferFor(ctx, t); ok {
			out.Artifacts = append(out.Artifacts, off)
		}
	}
	if err := c.Reply(id, protocol.MsgSyncState, out); err != nil {
		return
	}
	for _, t := range ts {
		if t.State != task.Submitted && t.State != task.Accepted && t.State != task.Transferring {
			d.startLogPump(c, t.ID)
		}
	}
}

// onSyncState (requester side): reconcile mirrors, register artifacts and
// re-send submits the executor never received.
func (d *Daemon) onSyncState(c *peer.Conn, s protocol.SyncState) {
	for _, a := range s.Artifacts {
		d.onArtifactOffer(c.PeerFP, a)
	}
	for _, t := range s.Tasks {
		d.mirrorApply(c.PeerFP, t)
	}
	for _, id := range s.Unknown {
		t, err := d.st.GetTask(d.ctx, id)
		if err == nil && t.Role == store.RoleRequester && t.State == task.Submitted {
			d.goFn(func() { d.resendSubmit(c, t) })
		}
	}
}

// serveFile is the holder side of file.request for both roles.
func (d *Daemon) serveFile(c *peer.Conn, r protocol.FileRequest) {
	path, err := d.resolveFile(c.PeerFP, r.TaskID, r.SHA256)
	if err != nil {
		e := protocol.AsError(err)
		c.Send(protocol.MsgFileError, protocol.FileError{TaskID: r.TaskID, SHA256: r.SHA256, Code: e.Code, Message: e.Message})
		return
	}
	sent, err := transfer.Serve(d.ctx, c, r.TaskID, r.SHA256, path, r.Offset)
	if st, serr := os.Stat(path); serr == nil {
		rel, _ := filepath.Rel(d.home, path)
		d.st.UpsertTransfer(d.ctx, store.Transfer{TaskID: r.TaskID, SHA256: r.SHA256, Path: filepath.ToSlash(rel), Size: st.Size(),
			BytesDone: r.Offset + sent, Direction: store.DirOut})
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		d.log.Debug("serve file", "task", r.TaskID, "err", err)
		if c.Alive() {
			c.Send(protocol.MsgFileError, protocol.FileError{TaskID: r.TaskID, SHA256: r.SHA256, Code: protocol.CodeTransferError, Message: err.Error()})
		}
	}
}

func (d *Daemon) resolveFile(peerFP, taskID, sha string) (string, error) {
	t, err := d.st.GetTask(d.ctx, taskID)
	if err != nil || t.PeerFP != peerFP {
		return "", protocol.Errf(protocol.CodeNotFound, "unknown task %s", taskID)
	}
	switch t.Role {
	case store.RoleRequester:
		spec, err := task.ParseJSON([]byte(t.SpecJSON))
		if err != nil {
			return "", err
		}
		for _, o := range spec.Inputs.Offers {
			if o.SHA256 == sha {
				return filepath.Join(t.ProjectDir, filepath.FromSlash(o.Path)), nil
			}
		}
	case store.RoleExecutor:
		as, _ := d.st.ListArtifacts(d.ctx, taskID)
		for _, a := range as {
			if a.SHA256 == sha {
				return filepath.Join(d.wsDir(taskID), filepath.FromSlash(a.Path)), nil
			}
		}
	}
	return "", protocol.Errf(protocol.CodeNotFound, "task %s has no file with sha256 %s", taskID, sha)
}

// checkFileOffer verifies the holder's offer matches the submitted spec.
func (d *Daemon) checkFileOffer(peerFP string, fo protocol.FileOfferMsg) {
	t, err := d.st.GetTask(d.ctx, fo.TaskID)
	if err != nil || t.Role != store.RoleExecutor || t.PeerFP != peerFP {
		return
	}
	spec, err := task.ParseJSON([]byte(t.SpecJSON))
	if err != nil {
		return
	}
	want := map[string]string{}
	for _, o := range spec.Inputs.Offers {
		want[o.Path] = o.SHA256
	}
	for _, f := range fo.Files {
		if want[f.Path] != f.SHA256 {
			d.log.Warn("file.offer does not match the submitted spec; the spec snapshot wins", "task", t.ID, "path", f.Path)
		}
	}
}
