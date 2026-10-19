package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"kith/internal/config"
	"kith/internal/ipc"
	"kith/internal/protocol"
)

// KithService is bound to the frontend. Every method is a thin call to the
// local daemon (IPC) or to the kith CLI; the GUI holds no state of its own.
type KithService struct {
	cl     ipc.Client
	app    *application.App
	poller *Poller

	mu   sync.Mutex
	pair *pairSession
}

type pairSession struct {
	cn     *ipc.Conn
	cancel context.CancelFunc
}

func NewKithService() *KithService {
	return &KithService{cl: ipc.Client{Home: config.ResolveHome("")}}
}

func (s *KithService) call(method string, params any) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var out json.RawMessage
	if err := s.cl.Call(ctx, method, params, &out); err != nil {
		return nil, friendly(err)
	}
	return out, nil
}

// friendly turns a protocol error into "message — Fix: …" text for the UI.
func friendly(err error) error {
	var e *protocol.Error
	if errors.As(err, &e) {
		msg := e.Message
		if e.Remediation != "" {
			msg += "\nFix: " + e.Remediation
		}
		return errors.New(msg)
	}
	return err
}

// Snapshot returns the latest polled state (the frontend also receives it
// as the "kith:state" event).
func (s *KithService) Snapshot() Snapshot { return s.poller.Refresh() }

func (s *KithService) Task(id string) (json.RawMessage, error) {
	return s.call("task", map[string]string{"id": id})
}

// Logs returns task output from a byte offset (poll with next offset).
func (s *KithService) Logs(id string, offset int) (json.RawMessage, error) {
	return s.call("logs", map[string]any{"id": id, "offset": offset, "follow": false, "max": 256 << 10})
}

func (s *KithService) Approve(id string) (json.RawMessage, error) {
	defer s.poller.Kick()
	return s.call("approve", map[string]string{"id": id})
}

func (s *KithService) Deny(id string) (json.RawMessage, error) {
	defer s.poller.Kick()
	return s.call("deny", map[string]string{"id": id})
}

func (s *KithService) Cancel(id string) (json.RawMessage, error) {
	defer s.poller.Kick()
	return s.call("cancel", map[string]string{"id": id})
}

func (s *KithService) Artifacts(id string) (json.RawMessage, error) {
	return s.call("artifacts", map[string]string{"id": id})
}

func (s *KithService) Fetch(id, path string) (json.RawMessage, error) {
	return s.call("fetch", map[string]string{"id": id, "path": path})
}

func (s *KithService) SetPolicy(peer, policy string) error {
	defer s.poller.Kick()
	_, err := s.call("set_policy", map[string]string{"peer": peer, "policy": policy})
	return err
}

func (s *KithService) Unpair(peer string) error {
	defer s.poller.Kick()
	_, err := s.call("unpair", map[string]string{"peer": peer})
	return err
}

func (s *KithService) Probe() (json.RawMessage, error) { return s.call("probe", nil) }

// jsonFromCLI runs a kith command with --json; commands that report
// failure through their exit code (doctor, rejected submits) still print
// JSON, which is what the UI needs.
func jsonFromCLI(timeout time.Duration, args ...string) (json.RawMessage, error) {
	out, err := runKith(context.Background(), timeout, append(args, "--json")...)
	if i := strings.IndexAny(out, "[{"); i >= 0 && json.Valid([]byte(out[i:])) {
		return json.RawMessage(out[i:]), nil
	}
	if err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("unexpected output from kith %s: %s", strings.Join(args, " "), out)
}

func (s *KithService) Doctor() (json.RawMessage, error) { return jsonFromCLI(30*time.Second, "doctor") }

func (s *KithService) ConfigList() (json.RawMessage, error) {
	return jsonFromCLI(10*time.Second, "config", "list")
}

func (s *KithService) ConfigSet(key, value string) (string, error) {
	return runKith(context.Background(), 10*time.Second, "config", "set", key, value)
}

// Daemon controls the background daemon: start | stop | restart | install | uninstall.
func (s *KithService) Daemon(action string) (string, error) {
	defer s.poller.Kick()
	switch action {
	case "start", "stop", "restart", "uninstall":
		return runKith(context.Background(), 60*time.Second, "daemon", action)
	case "install":
		if runtime.GOOS == "windows" {
			return installWindowsElevated()
		}
		return runKith(context.Background(), 60*time.Second, "daemon", "install")
	}
	return "", fmt.Errorf("unknown daemon action %q", action)
}

// installWindowsElevated opens an Administrator console for `kith daemon
// install`, which prompts for the Windows password (the service runs as
// the user). The GUI never sees the password.
func installWindowsElevated() (string, error) {
	bin, err := kithBinary()
	if err != nil {
		return "", err
	}
	home := config.ResolveHome("")
	ps := fmt.Sprintf(`Start-Process -Verb RunAs -FilePath '%s' -ArgumentList 'daemon','install','--home','%s'`,
		strings.ReplaceAll(bin, "'", "''"), strings.ReplaceAll(home, "'", "''"))
	cmd := exec.Command("powershell", "-NoProfile", "-Command", ps)
	hideConsole(cmd)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("could not open an Administrator window: %v %s", err, out)
	}
	return "An Administrator window opened: enter your Windows password there to install the Kith service.", nil
}

// ---- pairing (human-only; streamed to the UI as "kith:pair" events) ----

type pairEvent struct {
	Kind  string          `json:"kind"` // waiting | code | peer-confirmed | done | error
	Data  json.RawMessage `json:"data,omitempty"`
	Error string          `json:"error,omitempty"`
	Code  string          `json:"code,omitempty"` // the error's code, e.g. PEER_UNREACHABLE
}

func (s *KithService) emitPair(e pairEvent) {
	if s.app != nil {
		s.app.Event.Emit("kith:pair", e)
	}
}

// StartPairing opens a pairing window; ip may be empty (wait for the other
// machine / mDNS) or an address to dial.
func (s *KithService) StartPairing(ip string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pair != nil {
		return errors.New("pairing is already in progress")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	cn, err := s.cl.Open(ctx, "pair", map[string]string{"ip": strings.TrimSpace(ip)})
	if err != nil {
		cancel()
		return friendly(err)
	}
	ps := &pairSession{cn: cn, cancel: cancel}
	s.pair = ps
	go func() {
		defer func() {
			cn.Close()
			cancel()
			s.mu.Lock()
			if s.pair == ps {
				s.pair = nil
			}
			s.mu.Unlock()
			s.poller.Kick()
		}()
		var peer json.RawMessage
		err := cn.Wait(func(ev string, data json.RawMessage) error {
			s.emitPair(pairEvent{Kind: ev, Data: data})
			if ev == "code" && s.app != nil {
				// Bring the dialog forward; it is already open on its code.
				showWindow(s.app, "")
			}
			return nil
		}, &peer)
		if err != nil {
			s.emitPair(pairEvent{Kind: "error", Error: friendly(err).Error(), Code: protocol.CodeOf(err)})
			return
		}
		s.emitPair(pairEvent{Kind: "done", Data: peer})
	}()
	return nil
}

// ConfirmPairing answers "do the codes match?" for the code on screen; the
// daemon ignores an answer about a code it no longer shows.
func (s *KithService) ConfirmPairing(match bool, code string) error {
	s.mu.Lock()
	ps := s.pair
	s.mu.Unlock()
	if ps == nil {
		return errors.New("no pairing in progress")
	}
	return ps.cn.Send(map[string]any{"confirm": match, "code": code})
}

func (s *KithService) CancelPairing() {
	s.mu.Lock()
	ps := s.pair
	s.mu.Unlock()
	if ps != nil {
		ps.cancel()
	}
}

// ---- files ----

// ChooseTaskFile shows an open dialog for a task.toml.
func (s *KithService) ChooseTaskFile() (string, error) {
	return s.app.Dialog.OpenFile().SetTitle("Choose a Kith task file").
		AddFilter("Kith task (*.toml)", "*.toml").CanChooseFiles(true).PromptForSingleSelection()
}

// SubmitFile submits a task.toml to a peer (inputs are relative to the file).
func (s *KithService) SubmitFile(path, peer string) (json.RawMessage, error) {
	defer s.poller.Kick()
	args := []string{"submit", "-f", path}
	if peer != "" {
		args = append(args, "--peer", peer)
	}
	return jsonFromCLI(5*time.Minute, args...)
}

// Reveal shows a file in Finder / Explorer / the file manager.
func (s *KithService) Reveal(path string) error { return s.app.Env.OpenFileManager(path, true) }

// Version of this GUI build.
func (s *KithService) Version() string { return config.KithVersion }
