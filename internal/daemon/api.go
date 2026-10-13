package daemon

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"time"

	"kith/internal/artifacts"
	"kith/internal/audit"
	"kith/internal/caps"
	"kith/internal/config"
	"kith/internal/identity"
	"kith/internal/ipc"
	"kith/internal/protocol"
	"kith/internal/store"
	"kith/internal/task"
)

// PeerView is the IPC/MCP shape of a paired peer.
type PeerView struct {
	FP        string `json:"fp"`
	Display   string `json:"display"`
	Name      string `json:"name"`
	Policy    string `json:"policy,omitempty"`
	LastAddr  string `json:"last_addr,omitempty"`
	LastSeen  string `json:"last_seen,omitempty"`
	Reachable bool   `json:"reachable"`
	Caps      string `json:"caps,omitempty"`
	// Manifest is the peer's last advertised capability manifest.
	Manifest *caps.Manifest `json:"manifest,omitempty"`
}

// TaskView is the IPC/MCP shape of a task.
type TaskView struct {
	store.Task
	Timeline  []store.Transition `json:"timeline,omitempty"`
	Artifacts []ArtifactView     `json:"artifacts,omitempty"`
	Argv      []string           `json:"argv,omitempty"`
	PeerName  string             `json:"peer_name,omitempty"`
}

type ArtifactView struct {
	store.Artifact
	ProducerOffline bool   `json:"producer_offline"`
	Status          string `json:"status"`
}

type StatusView struct {
	FP      string `json:"fp"`
	Display string `json:"display"`
	Name    string `json:"name"`
	Port    int    `json:"port"`
	Home    string `json:"home"`
	Version string `json:"version"`
	Peers   int    `json:"peers"`
	Online  int    `json:"online"`
	MDNS    bool   `json:"mdns"`
	PID     int    `json:"pid"`
}

func decode[T any](raw json.RawMessage) (T, error) {
	var v T
	if len(raw) == 0 {
		return v, nil
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return v, protocol.Errf(protocol.CodeProtocolError, "bad params: %v", err)
	}
	return v, nil
}

func (d *Daemon) handleIPC(ctx context.Context, req ipc.Request, s *ipc.Stream) (any, error) {
	switch req.Method {
	case "status":
		peers, _ := d.st.ListPeers(ctx)
		on := 0
		for _, p := range peers {
			if d.conn(p.FP) != nil {
				on++
			}
		}
		d.mu.Lock()
		adv := d.adv != nil
		d.mu.Unlock()
		return StatusView{FP: d.id.FP, Display: identity.Display(d.id.FP), Name: d.cfg.Name, Port: d.port, Home: d.home,
			Version: config.KithVersion, Peers: len(peers), Online: on, MDNS: adv, PID: os.Getpid()}, nil
	case "id":
		return map[string]string{"fp": d.id.FP, "display": identity.Display(d.id.FP), "name": d.cfg.Name}, nil
	case "probe":
		return d.refreshLocalCaps(ctx), nil
	case "peers":
		return d.peerViews(ctx)
	case "unpair":
		p, _ := decode[struct{ Peer string }](req.Params)
		pr, err := d.resolvePeer(ctx, p.Peer)
		if err != nil {
			return nil, err
		}
		if err := d.st.DeletePeer(ctx, pr.FP); err != nil {
			return nil, err
		}
		if c := d.conn(pr.FP); c != nil {
			c.GoAway(protocol.Errf(protocol.CodeAuthFailed, "unpaired"))
		}
		d.audit.Write(audit.Event{Event: audit.EvUnpair, PeerFP: pr.FP})
		return map[string]string{"unpaired": pr.FP}, nil
	case "set_policy":
		p, _ := decode[struct{ Peer, Policy string }](req.Params)
		if p.Policy != config.PolicyAsk && p.Policy != config.PolicyAuto {
			return nil, protocol.Errf(protocol.CodeProtocolError, "policy must be ask or auto")
		}
		pr, err := d.resolvePeer(ctx, p.Peer)
		if err != nil {
			return nil, err
		}
		return map[string]string{"peer": pr.FP, "policy": p.Policy}, d.st.SetPeerPolicy(ctx, pr.FP, p.Policy)
	case "submit":
		p, err := decode[struct {
			Peer       string          `json:"peer"`
			Spec       json.RawMessage `json:"spec"`
			ProjectDir string          `json:"project_dir"`
		}](req.Params)
		if err != nil {
			return nil, err
		}
		return d.submit(ctx, p.Peer, p.Spec, p.ProjectDir)
	case "tasks":
		p, _ := decode[struct{ Limit int }](req.Params)
		if p.Limit == 0 {
			p.Limit = 50
		}
		ts, err := d.st.ListTasks(ctx, store.TaskFilter{Limit: p.Limit})
		if err != nil {
			return nil, err
		}
		out := []TaskView{}
		for _, t := range ts {
			out = append(out, TaskView{Task: t, PeerName: d.peerName(ctx, t.PeerFP)})
		}
		return out, nil
	case "task":
		p, _ := decode[struct{ ID string }](req.Params)
		return d.taskView(ctx, p.ID)
	case "logs":
		p, _ := decode[struct {
			ID     string `json:"id"`
			Offset int64  `json:"offset"`
			Follow bool   `json:"follow"`
			Max    int64  `json:"max"`
		}](req.Params)
		return d.streamLogs(ctx, s, p.ID, p.Offset, p.Follow, p.Max)
	case "cancel":
		p, _ := decode[struct{ ID string }](req.Params)
		t, err := d.cancelTask(ctx, p.ID)
		if err != nil {
			return nil, err
		}
		return d.taskView(ctx, t.ID)
	case "approve", "deny":
		p, _ := decode[struct{ ID string }](req.Params)
		t, err := d.findTask(ctx, p.ID)
		if err != nil {
			return nil, err
		}
		if req.Method == "approve" {
			_, err = d.approve(ctx, t.ID)
		} else {
			_, err = d.deny(ctx, t.ID, "denied by the executor's owner")
		}
		if err != nil {
			return nil, err
		}
		return d.taskView(ctx, t.ID)
	case "artifacts":
		p, _ := decode[struct{ ID string }](req.Params)
		id := ""
		if p.ID != "" {
			t, err := d.findTask(ctx, p.ID)
			if err != nil {
				return nil, err
			}
			id = t.ID
		}
		as, err := d.st.ListArtifacts(ctx, id)
		if err != nil {
			return nil, err
		}
		return d.artifactViews(as), nil
	case "fetch":
		p, _ := decode[struct{ ID, Path string }](req.Params)
		return d.fetch(ctx, p.ID, p.Path)
	case "pair":
		p, _ := decode[struct{ IP string }](req.Params)
		return d.pairIPC(ctx, s, p.IP)
	case "doctor":
		return d.doctorInfo(ctx), nil
	}
	return nil, protocol.Errf(protocol.CodeNotFound, "unknown method %q", req.Method)
}

func (d *Daemon) peerName(ctx context.Context, fp string) string {
	if p, err := d.st.GetPeer(ctx, fp); err == nil {
		return p.Name
	}
	return ""
}

func (d *Daemon) peerViews(ctx context.Context) ([]PeerView, error) {
	ps, err := d.st.ListPeers(ctx)
	if err != nil {
		return nil, err
	}
	out := []PeerView{}
	for _, p := range ps {
		v := PeerView{FP: p.FP, Display: identity.Display(p.FP), Name: p.Name, Policy: p.Policy, LastAddr: p.LastAddr,
			LastSeen: p.LastSeen, Reachable: d.conn(p.FP) != nil}
		d.mu.Lock()
		if m, ok := d.remoteCaps[p.FP]; ok {
			mm := m
			v.Manifest = &mm
			v.Caps = m.Summary()
		}
		d.mu.Unlock()
		out = append(out, v)
	}
	return out, nil
}

func (d *Daemon) taskView(ctx context.Context, id string) (TaskView, error) {
	t, err := d.findTask(ctx, id)
	if err != nil {
		return TaskView{}, err
	}
	v := TaskView{Task: t, PeerName: d.peerName(ctx, t.PeerFP)}
	v.Timeline, _ = d.st.Transitions(ctx, t.ID)
	as, _ := d.st.ListArtifacts(ctx, t.ID)
	v.Artifacts = d.artifactViews(as)
	if spec, err := task.ParseJSON([]byte(t.SpecJSON)); err == nil {
		v.Argv = spec.Command
	}
	if st, err := os.Stat(d.logPath(t.ID)); err == nil && st.Size() > v.LogSize {
		v.LogSize = st.Size()
	}
	return v, nil
}

func (d *Daemon) artifactViews(as []store.Artifact) []ArtifactView {
	out := []ArtifactView{}
	for _, a := range as {
		v := ArtifactView{Artifact: a}
		v.Status, v.ProducerOffline = artifacts.Status(a, d.conn(a.ProducerFP) != nil)
		out = append(out, v)
	}
	return out
}

// LogChunkEvent is streamed by `logs`.
type LogChunkEvent struct {
	Offset int64  `json:"offset"`
	Data   string `json:"data"`
}

type LogsResult struct {
	TaskID string `json:"task_id"`
	Offset int64  `json:"offset"` // next offset to read
	State  string `json:"state"`
	Done   bool   `json:"done"` // task terminal and the full log is local
	Data   string `json:"data,omitempty"`
}

// streamLogs returns log text from offset. Without follow the text is
// returned in the result (poll-friendly); with follow it is streamed as
// events until the task is terminal and the log is complete.
func (d *Daemon) streamLogs(ctx context.Context, s *ipc.Stream, id string, off int64, follow bool, max int64) (LogsResult, error) {
	t, err := d.findTask(ctx, id)
	if err != nil {
		return LogsResult{}, err
	}
	if max <= 0 {
		max = 1 << 20
	}
	var acc strings.Builder
	for {
		ch := d.changedCh()
		t, _ = d.st.GetTask(ctx, t.ID)
		f, err := os.Open(d.logPath(t.ID))
		var size int64
		if err == nil {
			if st, err := f.Stat(); err == nil {
				size = st.Size()
			}
			if size > off {
				n := size - off
				if !follow && int64(acc.Len())+n > max {
					n = max - int64(acc.Len())
				}
				buf := make([]byte, n)
				m, _ := f.ReadAt(buf, off)
				if m > 0 {
					if follow {
						s.Send("log", LogChunkEvent{Offset: off, Data: string(buf[:m])})
					} else {
						acc.Write(buf[:m])
					}
					off += int64(m)
				}
			}
			f.Close()
		}
		done := task.IsTerminal(t.State) && off >= t.LogSize && size <= off
		if t.Role == store.RoleExecutor {
			done = task.IsTerminal(t.State) && size <= off
		}
		if !follow || done {
			return LogsResult{TaskID: t.ID, Offset: off, State: t.State, Done: done, Data: acc.String()}, nil
		}
		select {
		case <-ch:
		case <-time.After(500 * time.Millisecond):
		case <-ctx.Done():
			return LogsResult{TaskID: t.ID, Offset: off, State: t.State}, nil
		}
	}
}

// pairIPC drives a pairing session for one `kith pair` client.
func (d *Daemon) pairIPC(ctx context.Context, s *ipc.Stream, ip string) (any, error) {
	sess, err := d.openPairing()
	if err != nil {
		return nil, err
	}
	defer d.closePairing(sess)
	d.startAdvertise("pairing")
	if ip != "" {
		addr := withPort(ip, config.DefaultPort)
		d.goFn(func() { d.pairDial(sess, addr) })
		s.Send("waiting", map[string]string{"message": "connecting to " + addr})
	} else {
		s.Send("waiting", map[string]string{"message": "waiting for the other machine (run `kith pair` there too)"})
	}
	// Client lines (confirmations) arrive on the same connection.
	confirms := make(chan bool, 1)
	go func() {
		for {
			var m struct {
				Confirm *bool `json:"confirm"`
			}
			if err := s.Recv(&m); err != nil {
				if err != io.EOF {
					select {
					case confirms <- false:
					default:
					}
				}
				close(confirms)
				return
			}
			if m.Confirm != nil {
				confirms <- *m.Confirm
			}
		}
	}()
	window := time.NewTimer(5 * time.Minute)
	defer window.Stop()
	for {
		select {
		case ev := <-sess.events:
			switch ev.Kind {
			case "code":
				s.Send("code", ev)
			case "done":
				return ev.Peer, nil
			case "error":
				return nil, ev.Err
			}
		case ok, more := <-confirms:
			if !more {
				return nil, protocol.Errf(protocol.CodeAuthFailed, "pairing aborted")
			}
			select {
			case sess.confirm <- ok:
			default:
			}
		case <-window.C:
			return nil, protocol.Errf(protocol.CodeTimeout, "no peer connected within 5 minutes").
				WithFix("run `kith pair --ip <addr>` on one machine while `kith pair` runs on the other; check `kith doctor`")
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// DoctorInfo is the daemon-side part of `kith doctor`.
type DoctorInfo struct {
	Status StatusView    `json:"status"`
	Peers  []PeerView    `json:"peers"`
	Caps   caps.Manifest `json:"caps"`
}

func (d *Daemon) doctorInfo(ctx context.Context) DoctorInfo {
	st, _ := d.handleIPC(ctx, ipc.Request{Method: "status"}, nil)
	ps, _ := d.peerViews(ctx)
	d.mu.Lock()
	m := d.localCaps
	d.mu.Unlock()
	return DoctorInfo{Status: st.(StatusView), Peers: ps, Caps: m}
}
