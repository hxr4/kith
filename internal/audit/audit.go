// Package audit writes the append-only JSONL audit log ($KITH_HOME/audit.log).
// Never log pairing codes, keys or file contents.
package audit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const FileName = "audit.log"

// Event names.
const (
	EvSubmit           = "task.submit"
	EvAccept           = "task.accept"
	EvReject           = "task.reject"
	EvState            = "task.state"
	EvCancel           = "task.cancel"
	EvApprove          = "task.approve"
	EvDeny             = "task.deny"
	EvPairStart        = "pair.start"
	EvPairSuccess      = "pair.success"
	EvPairFail         = "pair.fail"
	EvPairRateLimited  = "pair.rate_limited"
	EvUnpair           = "peer.unpair"
	EvIdentityMismatch = "peer.identity_mismatch"
	EvAuthFailed       = "peer.auth_failed"
	EvArtifactFetch    = "artifact.fetch"
	EvDaemonStart      = "daemon.start"
	EvDaemonStop       = "daemon.stop"
)

type Event struct {
	TS     string   `json:"ts"`
	Event  string   `json:"event"`
	TaskID string   `json:"task_id,omitempty"`
	PeerFP string   `json:"peer_fp,omitempty"`
	Role   string   `json:"role,omitempty"`
	Argv   []string `json:"argv,omitempty"`
	State  string   `json:"state,omitempty"`
	Detail string   `json:"detail,omitempty"`
	Addr   string   `json:"addr,omitempty"`
}

type Log struct {
	mu sync.Mutex
	f  *os.File
}

func Open(home string) (*Log, error) {
	f, err := os.OpenFile(filepath.Join(home, FileName), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	return &Log{f: f}, nil
}

// Write appends one event. Errors are returned but callers usually only log them.
func (l *Log) Write(e Event) error {
	if l == nil {
		return nil
	}
	if e.TS == "" {
		e.TS = time.Now().UTC().Format(time.RFC3339Nano)
	}
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	l.mu.Lock()
	defer l.mu.Unlock()
	_, err = l.f.Write(b)
	return err
}

func (l *Log) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.f.Close()
}

// ReadAll parses an audit log file (used by tests and `kith doctor`).
func ReadAll(home string) ([]Event, error) {
	b, err := os.ReadFile(filepath.Join(home, FileName))
	if err != nil {
		return nil, err
	}
	var out []Event
	dec := json.NewDecoder(bytesReader(b))
	for dec.More() {
		var e Event
		if err := dec.Decode(&e); err != nil {
			return out, err
		}
		out = append(out, e)
	}
	return out, nil
}
