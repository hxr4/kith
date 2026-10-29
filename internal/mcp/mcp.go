// Package mcp exposes Kith to AI agents over MCP (stdio): find the paired
// machine, check and submit tasks, follow their progress (parsed metrics,
// not just raw logs), fetch results and cancel. The SDK is confined to
// this file's NewServer; tool logic depends only on the Backend interface,
// so the SDK can be swapped.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"kith/internal/caps"
	"kith/internal/config"
	"kith/internal/daemon"
	"kith/internal/ipc"
	"kith/internal/protocol"
	"kith/internal/store"
	"kith/internal/task"
)

// Backend is what the tools need from the daemon.
type Backend interface {
	ListPeers(ctx context.Context) ([]daemon.PeerView, error)
	Submit(ctx context.Context, peer string, spec json.RawMessage, projectDir string) (daemon.SubmitResult, error)
	GetTask(ctx context.Context, id string) (daemon.TaskView, error)
	Logs(ctx context.Context, id string, offset int64) (daemon.LogsResult, error)
	Fetch(ctx context.Context, id, path string) (daemon.FetchResult, error)
	Cancel(ctx context.Context, id string) (daemon.TaskView, error)
	ListTasks(ctx context.Context, limit int) ([]daemon.TaskView, error)
}

// IPCBackend talks to the local daemon.
type IPCBackend struct{ Client ipc.Client }

func (b IPCBackend) ListPeers(ctx context.Context) (out []daemon.PeerView, err error) {
	err = b.Client.Call(ctx, "peers", nil, &out)
	return
}
func (b IPCBackend) Submit(ctx context.Context, peer string, spec json.RawMessage, dir string) (out daemon.SubmitResult, err error) {
	err = b.Client.Call(ctx, "submit", map[string]any{"peer": peer, "spec": spec, "project_dir": dir}, &out)
	return
}
func (b IPCBackend) GetTask(ctx context.Context, id string) (out daemon.TaskView, err error) {
	err = b.Client.Call(ctx, "task", map[string]any{"id": id}, &out)
	return
}
func (b IPCBackend) Logs(ctx context.Context, id string, off int64) (out daemon.LogsResult, err error) {
	err = b.Client.Call(ctx, "logs", map[string]any{"id": id, "offset": off, "follow": false, "max": 256 << 10}, &out)
	return
}
func (b IPCBackend) Fetch(ctx context.Context, id, path string) (out daemon.FetchResult, err error) {
	err = b.Client.Call(ctx, "fetch", map[string]any{"id": id, "path": path}, &out)
	return
}
func (b IPCBackend) ListTasks(ctx context.Context, limit int) (out []daemon.TaskView, err error) {
	err = b.Client.Call(ctx, "tasks", map[string]any{"limit": limit}, &out)
	return
}
func (b IPCBackend) Cancel(ctx context.Context, id string) (out daemon.TaskView, err error) {
	err = b.Client.Call(ctx, "cancel", map[string]any{"id": id}, &out)
	return
}

// Tool inputs.
type (
	ListPeersIn struct{}
	SubmitIn    struct {
		Peer       string         `json:"peer,omitempty" jsonschema:"peer name or fingerprint; omit to use the paired machine (the only one, or the only one online)"`
		Spec       map[string]any `json:"spec,omitempty" jsonschema:"task spec object: {command:[argv...], inputs:{files:[...]}, env:{...}, outputs:{include:[...]}, policy:{...}, id?:tsk_ULID}"`
		SpecFile   string         `json:"spec_file,omitempty" jsonschema:"alternative to spec: path to a task.toml file"`
		ProjectDir string         `json:"project_dir,omitempty" jsonschema:"directory input paths are relative to and outputs are copied back into (default: spec_file's dir or the current dir)"`
	}
	TaskIn struct {
		TaskID string `json:"task_id"`
	}
	LogsIn struct {
		TaskID string `json:"task_id"`
		Offset int64  `json:"offset,omitempty" jsonschema:"byte offset to read from (use next_offset from the previous call)"`
		Follow bool   `json:"follow,omitempty" jsonschema:"wait up to ~20s for new output before returning"`
	}
	FetchIn struct {
		TaskID string `json:"task_id"`
		Path   string `json:"path" jsonschema:"artifact path as listed by get_task"`
	}
	ListTasksIn struct {
		Active bool `json:"active,omitempty" jsonschema:"only tasks that haven't finished"`
		Limit  int  `json:"limit,omitempty" jsonschema:"most recent first (default 20)"`
	}
	ProgressIn struct {
		TaskID       string `json:"task_id"`
		TailLines    int    `json:"tail_lines,omitempty" jsonschema:"recent output lines to include (default 12, max 200)"`
		RecentEpochs int    `json:"recent_epochs,omitempty" jsonschema:"recent per-epoch metric records to include (default 5)"`
		FullHistory  bool   `json:"full_history,omitempty" jsonschema:"include every epoch record (for a final report or a plot)"`
	}
	WaitIn struct {
		TaskID   string `json:"task_id"`
		Until    string `json:"until,omitempty" jsonschema:"finish (default): return when the task ends; epoch: also return after each new epoch record; change: also on any state change or new metrics"`
		TimeoutS int    `json:"timeout_s,omitempty" jsonschema:"longest wait in seconds (default 50, max 600); some clients time tools out at 60 s"`
	}
)

// Tools implements the agent verbs over a Backend.
type Tools struct {
	B        Backend
	PollWait time.Duration // follow long-poll budget (default 20s)
	// Prog caches parsed progress per task across calls; nil reads each
	// log from the start.
	Prog *progressStore
	// Tick is how often wait_task re-checks (default 1s).
	Tick time.Duration
}

func (t Tools) store() *progressStore {
	if t.Prog != nil {
		return t.Prog
	}
	return newProgressStore()
}

// resolvePeer turns an optional peer reference into a fingerprint.
func (t Tools) resolvePeer(ctx context.Context, ref string) (daemon.PeerView, error) {
	ps, err := t.B.ListPeers(ctx)
	if err != nil {
		return daemon.PeerView{}, err
	}
	p, err := daemon.PickPeer(ps, ref)
	if err != nil {
		return daemon.PeerView{}, protocol.Errf(protocol.CodeNotFound, "%v", err).WithFix("call list_peers; pair a machine with the Kith app if none is listed")
	}
	return p, nil
}

// loadSpec reads spec or spec_file into canonical JSON plus the project dir.
func loadSpec(spec map[string]any, specFile, dir string) (*task.Spec, json.RawMessage, string, error) {
	var s *task.Spec
	switch {
	case specFile != "":
		b, err := os.ReadFile(specFile)
		if err != nil {
			return nil, nil, "", protocol.Errf(protocol.CodeNotFound, "read spec_file: %v", err)
		}
		if s, err = task.ParseTOML(b); err != nil {
			return nil, nil, "", err
		}
		if dir == "" {
			dir = filepath.Dir(specFile)
		}
	case spec != nil:
		b, _ := json.Marshal(spec)
		var err error
		if s, err = task.ParseJSON(b); err != nil {
			return nil, nil, "", err
		}
	default:
		return nil, nil, "", protocol.Errf(protocol.CodeProtocolError, "provide spec or spec_file")
	}
	if dir == "" {
		dir, _ = os.Getwd()
	}
	dir, _ = filepath.Abs(dir)
	return s, s.JSON(), dir, nil
}

func (t Tools) ListPeers(ctx context.Context, _ ListPeersIn) (any, error) {
	ps, err := t.B.ListPeers(ctx)
	if err != nil {
		return nil, err
	}
	type peer struct {
		Name        string `json:"name"`
		Fingerprint string `json:"fingerprint"`
		Display     string `json:"display"`
		Reachable   bool   `json:"reachable"`
		Caps        string `json:"caps,omitempty"`
		Policy      string `json:"policy"`
	}
	out := []peer{}
	for _, p := range ps {
		out = append(out, peer{p.Name, p.FP, p.Display, p.Reachable, p.Caps, p.Policy})
	}
	return map[string]any{"peers": out}, nil
}

func (t Tools) SubmitTask(ctx context.Context, in SubmitIn) (any, error) {
	_, raw, dir, err := loadSpec(in.Spec, in.SpecFile, in.ProjectDir)
	if err != nil {
		return nil, err
	}
	p, err := t.resolvePeer(ctx, in.Peer)
	if err != nil {
		return nil, err
	}
	r, err := t.B.Submit(ctx, p.FP, raw, dir)
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	b, _ := json.Marshal(r)
	json.Unmarshal(b, &out)
	if r.PendingApproval {
		out["next"] = fmt.Sprintf("%s asks before running tasks from this machine: someone must press Approve there (a notification or the Kith app). To skip this next time, set this machine's policy to auto on %s. Meanwhile call wait_task; it returns when the task starts or ends.", r.PeerName, r.PeerName)
	} else {
		out["next"] = "call wait_task (until=epoch to hear about each epoch) or task_progress"
	}
	return out, nil
}

// CheckIn is check_task's input.
type CheckIn struct {
	Peer       string         `json:"peer,omitempty" jsonschema:"peer name or fingerprint; omit for the paired machine"`
	Spec       map[string]any `json:"spec,omitempty" jsonschema:"task spec object (as for submit_task)"`
	SpecFile   string         `json:"spec_file,omitempty" jsonschema:"path to a task.toml"`
	ProjectDir string         `json:"project_dir,omitempty" jsonschema:"directory input paths are relative to"`
}

// CheckTask preflights a task against the peer's advertised capabilities
// (python, packages, GPU and VRAM, disk) and the local input files, before
// anything is sent. The executor checks again on submit.
func (t Tools) CheckTask(ctx context.Context, in CheckIn) (any, error) {
	s, _, dir, err := loadSpec(in.Spec, in.SpecFile, in.ProjectDir)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"project_dir": dir, "command": s.Command}
	var problems []string
	files, err := task.ExpandGlobs(dir, s.Inputs.Files)
	if err != nil {
		problems = append(problems, "inputs: "+err.Error())
	}
	var total int64
	for _, f := range files {
		st, err := os.Stat(filepath.Join(dir, filepath.FromSlash(f)))
		if err != nil {
			problems = append(problems, fmt.Sprintf("input %s: %v", f, err))
			continue
		}
		total += st.Size()
	}
	out["inputs"], out["input_bytes"] = files, total
	reqs := []caps.Requirement{}
	if p, err := t.resolvePeer(ctx, in.Peer); err != nil {
		problems = append(problems, err.Error())
	} else {
		out["peer"], out["peer_name"], out["reachable"] = p.FP, p.Name, p.Reachable
		if !p.Reachable {
			problems = append(problems, p.Name+" is offline")
		}
		if p.Manifest != nil {
			reqs = caps.Explain(*p.Manifest, s.Env, total)
		}
	}
	ok := len(problems) == 0
	for _, r := range reqs {
		if r.Status == "fail" {
			ok = false
		}
	}
	out["requirements"], out["ok"] = reqs, ok
	if len(problems) > 0 {
		out["problems"] = problems
	}
	return out, nil
}

func (t Tools) GetTask(ctx context.Context, in TaskIn) (any, error) {
	v, err := t.B.GetTask(ctx, in.TaskID)
	if err != nil {
		return nil, err
	}
	return v, nil
}

func (t Tools) StreamLogs(ctx context.Context, in LogsIn) (any, error) {
	wait := t.PollWait
	if wait == 0 {
		wait = 20 * time.Second
	}
	deadline := time.Now().Add(wait)
	for {
		r, err := t.B.Logs(ctx, in.TaskID, in.Offset)
		if err != nil {
			return nil, err
		}
		if !in.Follow || r.Data != "" || r.Done || time.Now().After(deadline) {
			return map[string]any{"task_id": r.TaskID, "text": r.Data, "next_offset": r.Offset, "state": r.State, "complete": r.Done}, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// ListTasks lists tasks in both directions, newest first: what this machine
// sent (direction out) and what the peer sent here (in).
func (t Tools) ListTasks(ctx context.Context, in ListTasksIn) (any, error) {
	limit := in.Limit
	if limit <= 0 {
		limit = 20
	}
	ts, err := t.B.ListTasks(ctx, 200)
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for _, v := range ts {
		if in.Active && task.IsTerminal(v.State) {
			continue
		}
		dir := "out"
		if v.Role == store.RoleExecutor {
			dir = "in"
		}
		r := map[string]any{"task_id": v.ID, "name": v.Name, "project": v.Project, "state": v.State, "direction": dir,
			"peer_name": v.PeerName, "created_at": v.CreatedAt, "updated_at": v.UpdatedAt}
		if v.PendingApproval {
			r["pending_approval"] = true
		}
		if v.Detail != "" {
			r["detail"] = v.Detail
		}
		out = append(out, r)
		if len(out) == limit {
			break
		}
	}
	return map[string]any{"tasks": out}, nil
}

// progressView combines the task's state with its parsed progress.
func (t Tools) progressView(ctx context.Context, id string, p *progress, tail, recent int, full bool) (map[string]any, error) {
	lr, err := p.update(ctx, t.B, id)
	if err != nil {
		return nil, err
	}
	v, err := t.B.GetTask(ctx, id)
	if err != nil {
		return nil, err
	}
	out := p.snapshot(tail, recent)
	out["task_id"], out["name"], out["state"], out["peer_name"] = v.ID, v.Name, v.State, v.PeerName
	out["complete"] = task.IsTerminal(v.State) && lr.Done
	if v.Detail != "" {
		out["detail"] = v.Detail
	}
	if v.Attempt > 1 {
		out["attempt"] = v.Attempt
	}
	if v.Code != "" {
		out["code"], out["remediation"] = v.Code, v.Remediation
	}
	if v.PendingApproval {
		out["pending_approval"] = true
		out["hint"] = fmt.Sprintf("waiting for someone to press Approve on %s", v.PeerName)
	}
	if ts, err := time.Parse(time.RFC3339Nano, v.StartedAt); err == nil && v.StartedAt != "" {
		end := time.Now()
		if task.IsTerminal(v.State) {
			if u, err := time.Parse(time.RFC3339Nano, v.UpdatedAt); err == nil {
				end = u
			}
		}
		out["elapsed_s"] = int(end.Sub(ts).Seconds())
	}
	if len(v.Artifacts) > 0 {
		arts := []map[string]any{}
		for _, a := range v.Artifacts {
			arts = append(arts, map[string]any{"path": a.Path, "size": a.Size, "status": a.Status})
		}
		out["artifacts"] = arts
	}
	if full {
		out["history"] = p.history()
	}
	return out, nil
}

func clampTail(n, def int) int {
	if n <= 0 {
		return def
	}
	return min(n, maxTail)
}

// TaskProgress is the agent's view of a running task: state, the latest and
// best metrics (from KITH_METRIC lines, or parsed from "epoch 3 ... f1=0.8"
// output), in-epoch progress and the last lines of output.
func (t Tools) TaskProgress(ctx context.Context, in ProgressIn) (any, error) {
	recent := in.RecentEpochs
	if recent == 0 {
		recent = 5
	}
	return t.progressView(ctx, in.TaskID, t.store().get(in.TaskID), clampTail(in.TailLines, 12), recent, in.FullHistory)
}

// WaitTask blocks until the task ends (or, with until=epoch/change, until it
// has news) or the timeout passes, then returns the task_progress view.
func (t Tools) WaitTask(ctx context.Context, in WaitIn) (any, error) {
	limit := 50 * time.Second
	if in.TimeoutS > 0 {
		limit = min(time.Duration(in.TimeoutS)*time.Second, 600*time.Second)
	}
	tick := t.Tick
	if tick == 0 {
		tick = time.Second
	}
	until := strings.ToLower(in.Until)
	type mark struct {
		state         string
		pending       bool
		epochs, kinds int
		complete      bool
	}
	p := t.store().get(in.TaskID)
	look := func() (mark, error) {
		v, err := t.progressView(ctx, in.TaskID, p, 0, 0, false)
		if err != nil {
			return mark{}, err
		}
		pend, _ := v["pending_approval"].(bool)
		done, _ := v["complete"].(bool)
		p.mu.Lock()
		k := len(p.kinds)
		p.mu.Unlock()
		return mark{fmt.Sprint(v["state"]), pend, v["epochs_reported"].(int), k, done}, nil
	}
	start := time.Now()
	m0, err := look()
	if err != nil {
		return nil, err
	}
	reason := "timeout"
	for m := m0; ; {
		if m.complete {
			reason = "finished"
			break
		}
		if until == "epoch" && m.epochs > m0.epochs {
			reason = "new epoch"
			break
		}
		if until == "change" && m != m0 {
			reason = "changed"
			break
		}
		if time.Since(start) >= limit {
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(tick):
		}
		if m, err = look(); err != nil {
			return nil, err
		}
	}
	out, err := t.progressView(ctx, in.TaskID, p, 12, 3, false)
	if err != nil {
		return nil, err
	}
	out["waited_s"] = int(time.Since(start).Seconds())
	out["returned_because"] = reason
	return out, nil
}

func (t Tools) FetchArtifact(ctx context.Context, in FetchIn) (any, error) {
	r, err := t.B.Fetch(ctx, in.TaskID, in.Path)
	if err != nil {
		return nil, err
	}
	return r, nil
}

func (t Tools) CancelTask(ctx context.Context, in TaskIn) (any, error) {
	v, err := t.B.Cancel(ctx, in.TaskID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"ack": true, "task_id": v.ID, "state": v.State, "detail": v.Detail}, nil
}

// ToolNames lists the exposed tools.
var ToolNames = []string{"list_peers", "check_task", "submit_task", "list_tasks", "task_progress", "wait_task", "get_task", "stream_logs", "fetch_artifact", "cancel_task"}

func wrap[In any](f func(context.Context, In) (any, error)) sdk.ToolHandlerFor[In, any] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, in In) (*sdk.CallToolResult, any, error) {
		out, err := f(ctx, in)
		if err != nil {
			e := protocol.AsError(err)
			b, _ := json.Marshal(e)
			return &sdk.CallToolResult{IsError: true, Content: []sdk.Content{&sdk.TextContent{Text: string(b)}}}, nil, nil
		}
		return nil, out, nil
	}
}

func bp(b bool) *bool { return &b }

// Tool hints for agents' permission systems: reading Kith state is
// read-only; submit runs work on the peer (whose own Ask/Auto policy still
// gates it); cancel is destructive.
var (
	readOnly = &sdk.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: bp(false)}
	submits  = &sdk.ToolAnnotations{DestructiveHint: bp(false), IdempotentHint: true, OpenWorldHint: bp(false)}
	fetches  = &sdk.ToolAnnotations{DestructiveHint: bp(false), IdempotentHint: true, OpenWorldHint: bp(false)}
	cancels  = &sdk.ToolAnnotations{DestructiveHint: bp(true), IdempotentHint: true, OpenWorldHint: bp(false)}
)

// Instructions are sent to every MCP client at connect time, so any agent
// learns how to use Kith even without the skill installed.
const Instructions = `Kith runs commands on the user's other paired computer (for example a
Windows PC with an NVIDIA GPU) over the local network, end to end encrypted.
Use it when the user says they have a Kith-connected computer, or when work
needs hardware this machine lacks (a CUDA GPU, more RAM).

To find a task the user mentions, call list_tasks.
Workflow: list_peers (the machine and its GPU/Python/packages) -> check_task
(preflight a task.toml or spec) -> submit_task (peer may be omitted) ->
wait_task until=epoch in a loop, reporting each epoch's metrics to the user ->
fetch_artifact for results. Don't ask the user before each step; do report
progress. Tasks are argv commands run in a fresh workspace on the peer;
inputs are files sent from project_dir, outputs listed in outputs.include
come back. Print progress lines as
KITH_METRIC {"kind":"epoch","epoch":3,"epochs":20,"val_acc":0.91,"f1":0.90}
for exact metrics; plain "epoch 3/20 loss=0.4 f1=0.9" lines are parsed too.
If pending_approval is true, tell the user once that someone must press
Approve on the peer (or set this machine's policy to auto there), then keep
waiting.`

// NewServer builds the SDK server (adapter boundary).
func NewServer(t Tools) *sdk.Server {
	if t.Prog == nil {
		t.Prog = newProgressStore()
	}
	s := sdk.NewServer(&sdk.Implementation{Name: "kith", Title: "Kith", Version: config.KithVersion}, &sdk.ServerOptions{Instructions: Instructions})
	sdk.AddTool(s, &sdk.Tool{Name: "list_peers", Annotations: readOnly, Description: "List the paired Kith computers: name, fingerprint, online, policy, and capabilities (OS, Python, packages, GPU and VRAM, disk)."}, wrap(t.ListPeers))
	sdk.AddTool(s, &sdk.Tool{Name: "check_task", Annotations: readOnly, Description: "Preflight a task (spec or spec_file) against the paired computer before sending: each requirement (python, packages, GPU/VRAM, disk) with pass/fail and what the peer has, input sizes, ok."}, wrap(t.CheckTask))
	sdk.AddTool(s, &sdk.Tool{Name: "submit_task", Annotations: submits, Description: "Run a task on the paired computer: an argv command plus input files (relative to project_dir), env requirements and outputs to copy back. peer is optional. Returns task_id, state, pending_approval and what to do next."}, wrap(t.SubmitTask))
	sdk.AddTool(s, &sdk.Tool{Name: "list_tasks", Annotations: readOnly, Description: "List tasks newest first, both directions (out = sent from this machine, in = sent here): id, name, state, peer, pending_approval. active=true for unfinished ones. Use it to find a task the user mentions."}, wrap(t.ListTasks))
	sdk.AddTool(s, &sdk.Tool{Name: "task_progress", Annotations: readOnly, Description: "Live progress of a task: state, elapsed time, latest and best metrics per epoch (accuracy, F1, loss...), the current step within the epoch, recent output lines, artifacts. Cheap to call repeatedly."}, wrap(t.TaskProgress))
	sdk.AddTool(s, &sdk.Tool{Name: "wait_task", Annotations: readOnly, Description: "Wait for a task to finish (until=finish), or for its next epoch (until=epoch), or any change (until=change), up to timeout_s (default 50). Returns the task_progress view plus returned_because. Call in a loop to follow a training run."}, wrap(t.WaitTask))
	sdk.AddTool(s, &sdk.Tool{Name: "get_task", Annotations: readOnly, Description: "Full task record: state, timeline of transitions, artifacts, inputs, requirements, detail and remediation."}, wrap(t.GetTask))
	sdk.AddTool(s, &sdk.Tool{Name: "stream_logs", Annotations: readOnly, Description: "Raw task output from a byte offset. Poll with next_offset; follow=true waits briefly for new output. Prefer task_progress for metrics."}, wrap(t.StreamLogs))
	sdk.AddTool(s, &sdk.Tool{Name: "fetch_artifact", Annotations: fetches, Description: "Ensure an output file is present on this machine (pulling it from the peer if needed); returns the local path and sha256."}, wrap(t.FetchArtifact))
	sdk.AddTool(s, &sdk.Tool{Name: "cancel_task", Annotations: cancels, Description: "Cancel a task; returns an ack and the final state."}, wrap(t.CancelTask))
	return s
}

// ServeStdio runs the MCP server on stdin/stdout.
func ServeStdio(ctx context.Context, home string) error {
	return NewServer(Tools{B: IPCBackend{Client: ipc.Client{Home: home}}}).Run(ctx, &sdk.StdioTransport{})
}
