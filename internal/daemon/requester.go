package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"kith/internal/artifacts"
	"kith/internal/audit"
	"kith/internal/caps"
	"kith/internal/identity"
	"kith/internal/peer"
	"kith/internal/protocol"
	"kith/internal/store"
	"kith/internal/task"
	"kith/internal/transfer"
)

// SubmitResult is returned by submit (and MCP submit_task).
type SubmitResult struct {
	TaskID          string `json:"task_id"`
	State           string `json:"state"`
	PendingApproval bool   `json:"pending_approval"`
	Peer            string `json:"peer"`
	PeerName        string `json:"peer_name,omitempty"`
	Code            string `json:"code,omitempty"`
	Detail          string `json:"detail,omitempty"`
	Remediation     string `json:"remediation,omitempty"`
	Existing        bool   `json:"existing,omitempty"`
}

func (d *Daemon) submitResult(t store.Task, existing bool) SubmitResult {
	return SubmitResult{TaskID: t.ID, State: t.State, PendingApproval: t.PendingApproval, Peer: t.PeerFP,
		PeerName: d.peerName(d.ctx, t.PeerFP), Code: t.Code, Detail: t.Detail, Remediation: t.Remediation, Existing: existing}
}

// resolvePeer accepts a peer name, display fingerprint or hex prefix.
func (d *Daemon) resolvePeer(ctx context.Context, ref string) (store.Peer, error) {
	peers, err := d.st.ListPeers(ctx)
	if err != nil {
		return store.Peer{}, err
	}
	if ref == "" && len(peers) == 1 {
		return peers[0], nil
	}
	var hits []store.Peer
	norm := identity.NormalizeFP(ref)
	for _, p := range peers {
		if strings.EqualFold(p.Name, ref) || (norm != "" && strings.HasPrefix(p.FP, norm)) {
			hits = append(hits, p)
		}
	}
	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		return store.Peer{}, protocol.Errf(protocol.CodeNotFound, "no paired peer matches %q", ref).WithFix("run `kith peers` to list peers, or `kith pair` to add one")
	default:
		return store.Peer{}, protocol.Errf(protocol.CodeNotFound, "%q matches %d peers", ref, len(hits)).WithFix("use the fingerprint shown by `kith peers`")
	}
}

// submit (requester side).
func (d *Daemon) submit(ctx context.Context, peerRef string, rawSpec json.RawMessage, projectDir string) (SubmitResult, error) {
	spec, err := task.ParseJSON(rawSpec)
	if err != nil {
		return SubmitResult{}, err
	}
	if len(spec.Inputs.Offers) > 0 {
		return SubmitResult{}, protocol.Errf(protocol.CodeProtocolError, "inputs.offers is computed by kith; list files in inputs.files")
	}
	if spec.ID == "" {
		spec.ID = task.NewID()
	}
	// Idempotency: an existing id returns the existing record.
	if ex, err := d.st.GetTask(ctx, spec.ID); err == nil {
		if ex.Role == store.RoleRequester && ex.State == task.Submitted && ex.RemoteSeq == 0 {
			if c := d.conn(ex.PeerFP); c != nil {
				d.resendSubmit(c, ex)
				ex, _ = d.st.GetTask(ctx, ex.ID)
			}
		}
		return d.submitResult(ex, true), nil
	}
	p, err := d.resolvePeer(ctx, peerRef)
	if err != nil {
		return SubmitResult{}, err
	}
	if projectDir == "" {
		return SubmitResult{}, protocol.Errf(protocol.CodeProtocolError, "project_dir is required")
	}
	projectDir, _ = filepath.Abs(projectDir)
	if st, err := os.Stat(projectDir); err != nil || !st.IsDir() {
		return SubmitResult{}, protocol.Errf(protocol.CodeNotFound, "project dir %s does not exist", projectDir)
	}
	files, err := task.ExpandGlobs(projectDir, spec.Inputs.Files)
	if err != nil {
		return SubmitResult{}, protocol.Errf(protocol.CodeNotFound, "inputs: %v", err)
	}
	var total int64
	for _, f := range files {
		sha, size, err := transfer.SHAFile(filepath.Join(projectDir, filepath.FromSlash(f)))
		if err != nil {
			return SubmitResult{}, protocol.Errf(protocol.CodeNotFound, "input %s: %v", f, err)
		}
		spec.Inputs.Offers = append(spec.Inputs.Offers, protocol.FileOffer{Path: f, Size: size, SHA256: sha})
		total += size
	}
	if float64(total) > spec.Inputs.MaxTotalGB*caps.GiB {
		return SubmitResult{}, protocol.Errf(protocol.CodeDiskFull, "inputs total %.1fGB exceeds max_total_gb %.1f", float64(total)/caps.GiB, spec.Inputs.MaxTotalGB).
			WithFix("raise inputs.max_total_gb or send fewer files")
	}
	c := d.conn(p.FP)
	if c == nil {
		return SubmitResult{}, protocol.Errf(protocol.CodePeerUnreachable, "peer %s (%s) is not connected", p.Name, identity.Display(p.FP)).
			WithFix("make sure the kith daemon runs on %s; run `kith doctor` on both machines", p.Name)
	}
	t, created, err := d.st.CreateTask(ctx, store.Task{ID: spec.ID, Role: store.RoleRequester, PeerFP: p.FP, Project: spec.Project,
		Name: spec.Name, SpecJSON: string(spec.JSON()), State: task.Submitted, ProjectDir: projectDir})
	if err != nil {
		return SubmitResult{}, err
	}
	if !created {
		return d.submitResult(t, true), nil
	}
	d.auditTask(audit.EvSubmit, t, task.Submitted, "")
	d.notify()
	d.sendSubmit(ctx, c, t)
	t, _ = d.st.GetTask(ctx, t.ID)
	return d.submitResult(t, false), nil
}

// sendSubmit sends task.submit and waits for the accept/reject (which the
// connection handler applies to the mirror before we wake).
func (d *Daemon) sendSubmit(ctx context.Context, c *peer.Conn, t store.Task) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	if _, err := c.Request(ctx, protocol.MsgTaskSubmit, protocol.TaskSubmit{Spec: json.RawMessage(t.SpecJSON)}); err != nil {
		d.log.Warn("task.submit not acknowledged yet; will retry on reconnect", "task", t.ID, "err", err)
	}
}

func (d *Daemon) resendSubmit(c *peer.Conn, t store.Task) { d.sendSubmit(d.ctx, c, t) }

// mirrorApply updates the requester's mirror from executor-authoritative
// state. Stale updates (by executor seq) are ignored. COMPLETED is held as
// FINALIZING until copy-back finished (DECISIONS.md D-026).
func (d *Daemon) mirrorApply(peerFP string, s protocol.TaskState) {
	d.mirrorMu.Lock()
	defer d.mirrorMu.Unlock()
	ctx := d.ctx
	t, err := d.st.GetTask(ctx, s.TaskID)
	if err != nil || t.Role != store.RoleRequester || t.PeerFP != peerFP {
		return
	}
	if s.Seq != 0 && s.Seq <= t.RemoteSeq {
		return
	}
	want := s.State
	detail := s.Detail
	if want == task.Completed && d.pendingCopyBack(t.ID) {
		want = task.Finalizing
		detail = "copying artifacts back"
		d.goFn(func() { d.copyBack(t.ID) })
	}
	if s.LogSize > 0 {
		d.st.SetLogSize(ctx, t.ID, s.LogSize)
	}
	ch := store.Change{State: want, Detail: detail, Code: s.Code, Remediation: s.Remediation, Attempt: s.Attempt,
		PendingApproval: boolp(s.PendingApproval), RemoteSeq: s.Seq, RemoteState: s.State}
	if want == t.State && detail == t.Detail && s.PendingApproval == t.PendingApproval && s.Code == t.Code {
		// No visible change: just remember the executor's seq.
		if s.Seq > 0 {
			d.st.SetRemoteSeq(ctx, t.ID, s.Seq)
		}
		return
	}
	if want == t.State {
		// Same state, new detail/flags (e.g. pending approval, "copying
		// artifacts back"): update in place; the timeline only records
		// state changes.
		if _, err := d.st.UpdateInPlace(ctx, t.ID, ch); err == nil {
			d.notify()
		}
		return
	}
	nt, _, err := d.st.Transition(ctx, t.ID, ch, nil)
	if err != nil {
		return
	}
	ev := audit.EvState
	if want == task.Rejected {
		ev = audit.EvReject
	} else if want == task.Accepted {
		ev = audit.EvAccept
	}
	d.auditTask(ev, nt, want, detail)
	d.notify()
	if want == task.Transferring && t.State != task.Transferring {
		d.sendFileOffer(nt)
	}
}

func (d *Daemon) pendingCopyBack(id string) bool {
	as, _ := d.st.ListArtifacts(d.ctx, id)
	return artifacts.PendingCopyBack(as)
}

// sendFileOffer announces the input files the requester holds for a task
// (holder → puller). The executor checks it against the submitted spec.
func (d *Daemon) sendFileOffer(t store.Task) {
	spec, err := task.ParseJSON([]byte(t.SpecJSON))
	if err != nil {
		return
	}
	if c := d.conn(t.PeerFP); c != nil {
		files := spec.Inputs.Offers
		if files == nil {
			files = []protocol.FileOffer{}
		}
		c.Send(protocol.MsgFileOffer, protocol.FileOfferMsg{TaskID: t.ID, Files: files})
	}
}

// onArtifactOffer registers announced artifacts (requester side) and starts
// copy-back for those not retained on the executor.
func (d *Daemon) onArtifactOffer(peerFP string, off protocol.ArtifactOffer) {
	t, err := d.st.GetTask(d.ctx, off.TaskID)
	if err != nil || t.Role != store.RoleRequester || t.PeerFP != peerFP {
		return
	}
	any := false
	for _, a := range off.Artifacts {
		if _, err := task.CleanRel(a.Path); err != nil || len(a.SHA256) != 64 {
			continue
		}
		d.st.UpsertArtifact(d.ctx, store.Artifact{TaskID: t.ID, Path: a.Path, SHA256: a.SHA256, Size: a.Size,
			Location: store.LocRemote, ProducerFP: peerFP, CopyBack: !a.Remote})
		if !a.Remote {
			any = true
		}
	}
	d.notify()
	if any && d.pendingCopyBack(t.ID) {
		d.goFn(func() { d.copyBack(t.ID) })
	}
}

// copyBack pulls all copy-back artifacts into the project dir.
func (d *Daemon) copyBack(id string) {
	d.mu.Lock()
	if d.copying[id] {
		d.mu.Unlock()
		return
	}
	d.copying[id] = true
	d.mu.Unlock()
	defer func() {
		d.mu.Lock()
		delete(d.copying, id)
		d.mu.Unlock()
	}()
	t, err := d.st.GetTask(d.ctx, id)
	if err != nil {
		return
	}
	as, _ := d.st.ListArtifacts(d.ctx, id)
	var failed error
	for _, a := range as {
		if !a.CopyBack || a.Location != store.LocRemote {
			continue
		}
		if err := d.pullArtifact(d.ctx, t, a, filepath.Join(t.ProjectDir, filepath.FromSlash(a.Path))); err != nil {
			if d.ctx.Err() != nil || protocol.CodeOf(err) == protocol.CodePeerUnreachable {
				return // resumes on reconnect (sync)
			}
			failed = err
		}
	}
	d.mirrorMu.Lock()
	defer d.mirrorMu.Unlock()
	t, err = d.st.GetTask(d.ctx, id)
	if err != nil || t.RemoteState != task.Completed || t.State == task.Completed {
		d.notify()
		return
	}
	detail := "artifacts copied back"
	if failed != nil {
		detail = fmt.Sprintf("copy-back failed (%v); artifacts remain on the executor; use `kith fetch`", failed)
	}
	if _, _, err := d.st.Transition(d.ctx, id, store.Change{State: task.Completed, Detail: detail, RemoteState: task.Completed}, nil); err == nil {
		d.auditTask(audit.EvState, t, task.Completed, detail)
		d.log.Info("task completed", "task", id, "detail", detail)
	}
	d.notify()
}

// pullArtifact fetches one artifact with up to 3 whole-file retries.
func (d *Daemon) pullArtifact(ctx context.Context, t store.Task, a store.Artifact, dest string) error {
	off := protocol.FileOffer{Path: a.Path, Size: a.Size, SHA256: a.SHA256}
	var last error
	for attempt := 0; attempt < 3; attempt++ {
		c := d.conn(a.ProducerFP)
		if c == nil {
			return protocol.Errf(protocol.CodePeerUnreachable, "producer %s is offline", identity.Display(a.ProducerFP)).
				WithFix("start the kith daemon on the executor, then retry")
		}
		d.st.UpsertTransfer(ctx, store.Transfer{TaskID: t.ID, SHA256: a.SHA256, Path: a.Path, Size: a.Size, Direction: store.DirIn})
		err := d.xfer.Fetch(ctx, c, t.ID, off, dest, func(n int64) {
			d.st.UpsertTransfer(context.Background(), store.Transfer{TaskID: t.ID, SHA256: a.SHA256, Path: a.Path, Size: a.Size, BytesDone: n, Direction: store.DirIn})
		})
		if err == nil {
			d.st.MarkArtifactLocal(ctx, t.ID, a.Path)
			d.auditTask(audit.EvArtifactFetch, t, "", a.Path+" sha256="+a.SHA256)
			d.notify()
			return nil
		}
		last = err
		if protocol.CodeOf(err) != protocol.CodeTransferError {
			return err
		}
	}
	return last
}

// FetchResult is returned by fetch / MCP fetch_artifact.
type FetchResult struct {
	TaskID string `json:"task_id"`
	Path   string `json:"path"`
	Local  string `json:"local_path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

func (d *Daemon) fetch(ctx context.Context, id, path string) (FetchResult, error) {
	t, err := d.findTask(ctx, id)
	if err != nil {
		return FetchResult{}, err
	}
	rel, err := task.CleanRel(path)
	if err != nil {
		return FetchResult{}, protocol.Errf(protocol.CodeNotFound, "%v", err)
	}
	a, err := d.st.GetArtifact(ctx, t.ID, rel)
	if err != nil {
		return FetchResult{}, protocol.Errf(protocol.CodeNotFound, "task %s has no artifact %s", t.ID, rel).WithFix("list them with `kith artifacts %s`", t.ID)
	}
	var local string
	if t.Role == store.RoleExecutor {
		local = filepath.Join(d.wsDir(t.ID), filepath.FromSlash(rel))
	} else {
		local = filepath.Join(t.ProjectDir, filepath.FromSlash(rel))
		if a.Location == store.LocRemote {
			if err := d.pullArtifact(ctx, t, a, local); err != nil {
				return FetchResult{}, err
			}
		}
	}
	sha, size, err := transfer.SHAFile(local)
	if err != nil {
		return FetchResult{}, protocol.Errf(protocol.CodeNotFound, "local copy missing: %v", err)
	}
	if sha != a.SHA256 {
		return FetchResult{}, protocol.Errf(protocol.CodeTransferError, "local copy of %s does not match the registry hash (modified after fetch?)", rel)
	}
	return FetchResult{TaskID: t.ID, Path: rel, Local: local, SHA256: sha, Size: size}, nil
}

// cancel (either role) and wait briefly for the final state.
func (d *Daemon) cancelTask(ctx context.Context, id string) (store.Task, error) {
	t, err := d.findTask(ctx, id)
	if err != nil {
		return t, err
	}
	if task.IsTerminal(t.State) {
		return t, nil
	}
	if t.Role == store.RoleExecutor {
		d.auditTask(audit.EvCancel, t, "", "local")
		d.cancelExec(ctx, t.ID, cancelByExecutor)
	} else {
		c := d.conn(t.PeerFP)
		if c == nil {
			return t, protocol.Errf(protocol.CodePeerUnreachable, "executor is offline; cancel cannot be delivered").
				WithFix("retry when the peer is reachable (task state on the executor is unchanged)")
		}
		d.auditTask(audit.EvCancel, t, "", "")
		if _, err := c.Send(protocol.MsgTaskCancel, protocol.TaskCancel{TaskID: t.ID}); err != nil {
			return t, protocol.Errf(protocol.CodePeerUnreachable, "%v", err)
		}
	}
	deadline := time.After(d.opt.CancelGrace + 10*time.Second)
	for {
		ch := d.changedCh()
		t, _ = d.st.GetTask(ctx, t.ID)
		if task.IsTerminal(t.State) {
			return t, nil
		}
		select {
		case <-ch:
		case <-time.After(500 * time.Millisecond):
		case <-deadline:
			return t, nil
		case <-ctx.Done():
			return t, ctx.Err()
		}
	}
}

// findTask resolves an id or unique prefix.
func (d *Daemon) findTask(ctx context.Context, id string) (store.Task, error) {
	if t, err := d.st.GetTask(ctx, id); err == nil {
		return t, nil
	}
	if len(id) >= 6 {
		all, _ := d.st.ListTasks(ctx, store.TaskFilter{})
		var hits []store.Task
		for _, t := range all {
			if strings.HasPrefix(strings.ToUpper(t.ID), strings.ToUpper(id)) || strings.HasPrefix(strings.ToUpper(t.ID), "TSK_"+strings.ToUpper(id)) {
				hits = append(hits, t)
			}
		}
		if len(hits) == 1 {
			return hits[0], nil
		}
	}
	return store.Task{}, protocol.Errf(protocol.CodeNotFound, "no task %s", id).WithFix("list tasks with `kith tasks`")
}
