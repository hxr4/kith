// Package mcp exposes exactly six agent tools over MCP (stdio). The SDK is
// confined to this file's Serve/NewServer; tool logic depends only on the
// Backend interface, so the SDK can be swapped.
package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"kith/internal/config"
	"kith/internal/daemon"
	"kith/internal/ipc"
	"kith/internal/protocol"
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
func (b IPCBackend) Cancel(ctx context.Context, id string) (out daemon.TaskView, err error) {
	err = b.Client.Call(ctx, "cancel", map[string]any{"id": id}, &out)
	return
}

// Tool inputs.
type (
	ListPeersIn struct{}
	SubmitIn    struct {
		Peer       string         `json:"peer" jsonschema:"peer name or fingerprint (from list_peers)"`
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
)

// Tools implements the six verbs over a Backend.
type Tools struct {
	B        Backend
	PollWait time.Duration // follow long-poll budget (default 20s)
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
	var raw json.RawMessage
	dir := in.ProjectDir
	switch {
	case in.SpecFile != "":
		b, err := os.ReadFile(in.SpecFile)
		if err != nil {
			return nil, protocol.Errf(protocol.CodeNotFound, "read spec_file: %v", err)
		}
		s, err := task.ParseTOML(b)
		if err != nil {
			return nil, err
		}
		raw = s.JSON()
		if dir == "" {
			dir = filepath.Dir(in.SpecFile)
		}
	case in.Spec != nil:
		b, _ := json.Marshal(in.Spec)
		s, err := task.ParseJSON(b)
		if err != nil {
			return nil, err
		}
		raw = s.JSON()
	default:
		return nil, protocol.Errf(protocol.CodeProtocolError, "provide spec or spec_file")
	}
	if dir == "" {
		dir, _ = os.Getwd()
	}
	dir, _ = filepath.Abs(dir)
	r, err := t.B.Submit(ctx, in.Peer, raw, dir)
	if err != nil {
		return nil, err
	}
	return r, nil
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

// ToolNames lists the exposed tools (exactly six).
var ToolNames = []string{"list_peers", "submit_task", "get_task", "stream_logs", "fetch_artifact", "cancel_task"}

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

// NewServer builds the SDK server (adapter boundary).
func NewServer(t Tools) *sdk.Server {
	s := sdk.NewServer(&sdk.Implementation{Name: "kith", Version: config.KithVersion}, nil)
	sdk.AddTool(s, &sdk.Tool{Name: "list_peers", Description: "List paired Kith peers with fingerprints, reachability and a capability summary."}, wrap(t.ListPeers))
	sdk.AddTool(s, &sdk.Tool{Name: "submit_task", Description: "Submit a task (argv command + input files + requirements) to a peer. Capabilities are verified before any transfer. Idempotent by task id. Returns {task_id, pending_approval, state}."}, wrap(t.SubmitTask))
	sdk.AddTool(s, &sdk.Tool{Name: "get_task", Description: "Get a task's state, timeline, artifacts and detail/remediation."}, wrap(t.GetTask))
	sdk.AddTool(s, &sdk.Tool{Name: "stream_logs", Description: "Read task output from a byte offset. Poll with next_offset; follow=true waits briefly for new output."}, wrap(t.StreamLogs))
	sdk.AddTool(s, &sdk.Tool{Name: "fetch_artifact", Description: "Ensure an artifact is present locally (pulling it from the executor if retained there); returns local path and sha256."}, wrap(t.FetchArtifact))
	sdk.AddTool(s, &sdk.Tool{Name: "cancel_task", Description: "Cancel a task; returns an ack and the final state."}, wrap(t.CancelTask))
	return s
}

// ServeStdio runs the MCP server on stdin/stdout.
func ServeStdio(ctx context.Context, home string) error {
	return NewServer(Tools{B: IPCBackend{Client: ipc.Client{Home: home}}}).Run(ctx, &sdk.StdioTransport{})
}
