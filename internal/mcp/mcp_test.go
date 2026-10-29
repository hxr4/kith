package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"kith/internal/caps"
	"kith/internal/daemon"
	"kith/internal/ipc"
	"kith/internal/transfer"
)

type staticCaps struct{}

func (staticCaps) Probe(context.Context, caps.Request) (caps.Manifest, error) {
	return caps.Manifest{OS: "test", Arch: caps.NormArch(runtime.GOARCH), Python: []string{"3.11.0"}, Packages: map[string]string{},
		GPUs: []caps.GPU{}, FreeDiskGB: 100, Zstd: true, KithVer: "1.0.0"}, nil
}

func startDaemon(t *testing.T, name string) (ipc.Client, int) {
	home, _ := os.MkdirTemp("", "km")
	t.Cleanup(func() { time.Sleep(200 * time.Millisecond); os.RemoveAll(home) })
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	os.WriteFile(filepath.Join(home, "kith.toml"), []byte(fmt.Sprintf("port=%d\nname=%q\nbind=\"127.0.0.1\"\nmdns=false\ndefault_policy=\"auto\"\n", port, name)), 0o600)
	d, err := daemon.New(daemon.Options{Home: home, Caps: staticCaps{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { d.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	cl := ipc.Client{Home: home}
	for i := 0; i < 200; i++ {
		if cl.Call(context.Background(), "status", nil, nil) == nil {
			return cl, port
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("daemon did not start")
	return cl, 0
}

func pairIPC(cl ipc.Client, ip string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cn, err := cl.Open(ctx, "pair", map[string]string{"ip": ip})
	if err != nil {
		return err
	}
	defer cn.Close()
	return cn.Wait(func(ev string, _ json.RawMessage) error {
		if ev == "code" {
			return cn.Send(map[string]bool{"confirm": true})
		}
		return nil
	}, nil)
}

func callTool(t *testing.T, s *sdk.ClientSession, name string, args map[string]any) (map[string]any, bool) {
	t.Helper()
	res, err := s.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	text := res.Content[0].(*sdk.TextContent).Text
	var m map[string]any
	if err := json.Unmarshal([]byte(text), &m); err != nil {
		t.Fatalf("%s: %v: %s", name, err, text)
	}
	return m, res.IsError
}

func TestSixToolsOverMCP(t *testing.T) {
	py := findPython()
	if py == "" {
		t.Skip("python not found")
	}
	mac, _ := startDaemon(t, "mac")
	win, winPort := startDaemon(t, "win")
	errc := make(chan error, 1)
	go func() { errc <- pairIPC(win, "") }()
	time.Sleep(300 * time.Millisecond)
	if err := pairIPC(mac, fmt.Sprintf("127.0.0.1:%d", winPort)); err != nil {
		t.Fatal(err)
	}
	if err := <-errc; err != nil {
		t.Fatal(err)
	}

	// In-process MCP client ↔ server over the SDK's in-memory transport.
	srv := NewServer(Tools{B: IPCBackend{Client: mac}, PollWait: 3 * time.Second})
	ct, st := sdk.NewInMemoryTransports()
	ctx := context.Background()
	go srv.Run(ctx, st)
	client := sdk.NewClient(&sdk.Implementation{Name: "test-agent", Version: "1"}, nil)
	s, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// The advertised tool set.
	lt, err := s.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tl := range lt.Tools {
		names = append(names, tl.Name)
	}
	sort.Strings(names)
	want := append([]string{}, ToolNames...)
	sort.Strings(want)
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("tools %v", names)
	}

	// list_peers
	var peers map[string]any
	for i := 0; i < 100; i++ {
		peers, _ = callTool(t, s, "list_peers", nil)
		ps := peers["peers"].([]any)
		if len(ps) == 1 && ps[0].(map[string]any)["reachable"] == true {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	p0 := peers["peers"].([]any)[0].(map[string]any)
	if p0["name"] != "win" || len(p0["fingerprint"].(string)) != 64 {
		t.Fatalf("%+v", p0)
	}

	// submit_task (spec object)
	proj, _ := os.MkdirTemp("", "kmp")
	defer os.RemoveAll(proj)
	os.WriteFile(filepath.Join(proj, "train.py"), []byte(`import os, sys, time
os.makedirs("out", exist_ok=True)
n = int(sys.argv[1])
for i in range(n):
    print("epoch %d" % (i + 1), flush=True)
    time.sleep(0.05)
open("out/model.pt", "wb").write(b"weights" * 1000)
`), 0o644)
	spec := map[string]any{"command": []string{py, "train.py", "3"}, "inputs": map[string]any{"files": []string{"train.py"}},
		"outputs": map[string]any{"include": []string{"out/model.pt"}, "retain_cap_gb": 1e-6}}
	sub, isErr := callTool(t, s, "submit_task", map[string]any{"peer": "win", "spec": spec, "project_dir": proj})
	if isErr || sub["task_id"] == nil {
		t.Fatalf("%+v", sub)
	}
	id := sub["task_id"].(string)
	if sub["pending_approval"] != false {
		t.Fatalf("%+v", sub)
	}
	// Idempotent by id.
	spec["id"] = id
	again, _ := callTool(t, s, "submit_task", map[string]any{"peer": "win", "spec": spec, "project_dir": proj})
	if again["task_id"] != id || again["existing"] != true {
		t.Fatalf("%+v", again)
	}

	// get_task until done
	var task map[string]any
	for i := 0; i < 400; i++ {
		task, _ = callTool(t, s, "get_task", map[string]any{"task_id": id})
		if task["state"] == "COMPLETED" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if task["state"] != "COMPLETED" || task["timeline"] == nil {
		t.Fatalf("%+v", task)
	}
	arts := task["artifacts"].([]any)
	if len(arts) != 1 || arts[0].(map[string]any)["location"] != "remote" {
		t.Fatalf("expected retained artifact: %+v", arts)
	}

	// stream_logs: poll from offset 0, then follow at the end returns complete.
	var text string
	off := 0.0
	for i := 0; i < 50; i++ {
		lg, _ := callTool(t, s, "stream_logs", map[string]any{"task_id": id, "offset": off})
		text += lg["text"].(string)
		off = lg["next_offset"].(float64)
		if lg["complete"] == true {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !strings.Contains(text, "epoch 3") {
		t.Fatalf("logs %q", text)
	}
	lg, _ := callTool(t, s, "stream_logs", map[string]any{"task_id": id, "offset": off, "follow": true})
	if lg["complete"] != true || lg["text"] != "" {
		t.Fatalf("%+v", lg)
	}

	// fetch_artifact pulls the retained artifact and verifies it.
	fa, isErr := callTool(t, s, "fetch_artifact", map[string]any{"task_id": id, "path": "out/model.pt"})
	if isErr {
		t.Fatalf("%+v", fa)
	}
	sha, _, _ := transfer.SHAFile(fa["local_path"].(string))
	if sha != fa["sha256"] || fa["local_path"] != filepath.Join(proj, "out", "model.pt") {
		t.Fatalf("%+v", fa)
	}

	// A training-style task, followed the way an agent does: check_task,
	// submit_task without a peer, wait_task per epoch, task_progress.
	os.WriteFile(filepath.Join(proj, "fit.py"), []byte(`import json, time
for e in range(1, 4):
    time.sleep(0.4)
    print("KITH_METRIC " + json.dumps({"kind": "epoch", "epoch": e, "epochs": 3, "val_acc": 0.5 + e / 10, "macro_f1": 0.4 + e / 10}), flush=True)
print("trained", flush=True)
`), 0o644)
	fit := map[string]any{"command": []string{py, "fit.py"}, "inputs": map[string]any{"files": []string{"fit.py"}}}
	ck, isErr := callTool(t, s, "check_task", map[string]any{"spec": fit, "project_dir": proj})
	if isErr || ck["ok"] != true || ck["peer_name"] != "win" {
		t.Fatalf("check_task %+v", ck)
	}
	sub3, isErr := callTool(t, s, "submit_task", map[string]any{"spec": fit, "project_dir": proj})
	if isErr || sub3["peer_name"] != "win" {
		t.Fatalf("submit without a peer: %+v", sub3)
	}
	id3 := sub3["task_id"].(string)
	var epochsSeen []float64
	for i := 0; i < 20; i++ {
		w, isErr := callTool(t, s, "wait_task", map[string]any{"task_id": id3, "until": "epoch", "timeout_s": 10})
		if isErr {
			t.Fatalf("%+v", w)
		}
		if l, ok := w["latest"].(map[string]any); ok && (len(epochsSeen) == 0 || l["epoch"].(float64) != epochsSeen[len(epochsSeen)-1]) {
			epochsSeen = append(epochsSeen, l["epoch"].(float64))
		}
		if w["complete"] == true {
			break
		}
	}
	if len(epochsSeen) < 2 || epochsSeen[len(epochsSeen)-1] != 3 {
		t.Fatalf("wait_task until=epoch should report epochs as they happen: %v", epochsSeen)
	}
	lt3, _ := callTool(t, s, "list_tasks", map[string]any{"limit": 3})
	if first := lt3["tasks"].([]any)[0].(map[string]any); first["task_id"] != id3 || first["direction"] != "out" || first["peer_name"] != "win" {
		t.Fatalf("list_tasks %+v", lt3)
	}
	pr, _ := callTool(t, s, "task_progress", map[string]any{"task_id": id3, "full_history": true})
	if pr["state"] != "COMPLETED" || pr["complete"] != true || len(pr["history"].([]any)) != 3 {
		t.Fatalf("task_progress %+v", pr)
	}
	if b := pr["best"].(map[string]any)["macro_f1"].(map[string]any); b["epoch"] != 3.0 {
		t.Fatalf("best %+v", b)
	}

	// cancel_task on a long task: ack + final state.
	spec2 := map[string]any{"command": []string{py, "train.py", "100000"}, "inputs": map[string]any{"files": []string{"train.py"}}}
	sub2, _ := callTool(t, s, "submit_task", map[string]any{"peer": "win", "spec": spec2, "project_dir": proj})
	id2 := sub2["task_id"].(string)
	for i := 0; i < 200; i++ {
		tk, _ := callTool(t, s, "get_task", map[string]any{"task_id": id2})
		if tk["state"] == "RUNNING" {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	cx, _ := callTool(t, s, "cancel_task", map[string]any{"task_id": id2})
	if cx["ack"] != true || cx["state"] != "CANCELLED" {
		t.Fatalf("%+v", cx)
	}

	// Errors carry stable codes.
	e, isErr := callTool(t, s, "get_task", map[string]any{"task_id": "tsk_nope"})
	if !isErr || e["code"] != "NOT_FOUND" {
		t.Fatalf("%+v", e)
	}
}

// findPython returns a Python interpreter that actually runs. On Windows
// "python" is tried first and the Microsoft Store alias stubs (which exit
// 9009 with "Python was not found") are rejected by running them.
func findPython() string {
	names := []string{"python3", "python"}
	if runtime.GOOS == "windows" {
		names = []string{"python", "python3"}
	}
	for _, n := range names {
		p, err := exec.LookPath(n)
		if err != nil {
			continue
		}
		if exec.Command(p, "-c", "import sys; sys.exit(0)").Run() == nil {
			return p
		}
	}
	return ""
}
