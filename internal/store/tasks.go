package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

const (
	RoleRequester = "requester"
	RoleExecutor  = "executor"
)

type Task struct {
	ID              string `json:"id"`
	Role            string `json:"role"`
	PeerFP          string `json:"peer_fp"`
	Project         string `json:"project"`
	Name            string `json:"name"`
	SpecJSON        string `json:"-"`
	State           string `json:"state"`
	Detail          string `json:"detail,omitempty"`
	Attempt         int    `json:"attempt"`
	PendingApproval bool   `json:"pending_approval"`
	WSDir           string `json:"ws_dir,omitempty"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
	ProjectDir      string `json:"project_dir,omitempty"`
	Code            string `json:"code,omitempty"`
	Remediation     string `json:"remediation,omitempty"`
	RemoteSeq       int    `json:"-"`
	RemoteState     string `json:"remote_state,omitempty"`
	PID             int    `json:"-"`
	StartedAt       string `json:"started_at,omitempty"`
	LogSize         int64  `json:"log_size"`
	LastSynced      string `json:"last_synced,omitempty"`
}

type Transition struct {
	Seq    int    `json:"seq"`
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
	TS     string `json:"ts"`
}

const taskCols = `id,role,peer_fp,project,name,spec_json,state,detail,attempt,pending_approval,ws_dir,created_at,updated_at,project_dir,code,remediation,remote_seq,remote_state,pid,started_at,log_size,last_synced`

func scanTask(sc interface{ Scan(...any) error }) (Task, error) {
	var t Task
	var pend int
	err := sc.Scan(&t.ID, &t.Role, &t.PeerFP, &t.Project, &t.Name, &t.SpecJSON, &t.State, &t.Detail, &t.Attempt, &pend,
		&t.WSDir, &t.CreatedAt, &t.UpdatedAt, &t.ProjectDir, &t.Code, &t.Remediation, &t.RemoteSeq, &t.RemoteState,
		&t.PID, &t.StartedAt, &t.LogSize, &t.LastSynced)
	t.PendingApproval = pend != 0
	return t, err
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// CreateTask inserts a task and its first transition atomically. Returns
// (existing, false) when the id already exists (idempotent submit).
func (s *Store) CreateTask(ctx context.Context, t Task) (Task, bool, error) {
	var out Task
	created := false
	err := s.tx(ctx, func(tx *sql.Tx) error {
		ex, err := scanTask(tx.QueryRowContext(ctx, `SELECT `+taskCols+` FROM tasks WHERE id=?`, t.ID))
		if err == nil {
			out = ex
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		now := Now()
		if t.CreatedAt == "" {
			t.CreatedAt = now
		}
		t.UpdatedAt = now
		if t.Attempt == 0 {
			t.Attempt = 1
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO tasks(`+taskCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			t.ID, t.Role, t.PeerFP, t.Project, t.Name, t.SpecJSON, t.State, t.Detail, t.Attempt, b2i(t.PendingApproval),
			t.WSDir, t.CreatedAt, t.UpdatedAt, t.ProjectDir, t.Code, t.Remediation, t.RemoteSeq, t.RemoteState,
			t.PID, t.StartedAt, t.LogSize, t.LastSynced)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO transitions(task_id,seq,state,detail,ts) VALUES(?,1,?,?,?)`,
			t.ID, t.State, t.Detail, now); err != nil {
			return err
		}
		out = t
		created = true
		return nil
	})
	return out, created, err
}

func (s *Store) GetTask(ctx context.Context, id string) (Task, error) {
	t, err := scanTask(s.db.QueryRowContext(ctx, `SELECT `+taskCols+` FROM tasks WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return t, ErrNotFound
	}
	return t, err
}

// TaskFilter selects tasks. Empty fields match anything.
type TaskFilter struct {
	Role   string
	PeerFP string
	States []string
	Limit  int
}

func (s *Store) ListTasks(ctx context.Context, f TaskFilter) ([]Task, error) {
	q := `SELECT ` + taskCols + ` FROM tasks WHERE 1=1`
	var args []any
	if f.Role != "" {
		q += ` AND role=?`
		args = append(args, f.Role)
	}
	if f.PeerFP != "" {
		q += ` AND peer_fp=?`
		args = append(args, f.PeerFP)
	}
	if len(f.States) > 0 {
		q += ` AND state IN (?` + strings.Repeat(",?", len(f.States)-1) + `)`
		for _, s := range f.States {
			args = append(args, s)
		}
	}
	q += ` ORDER BY created_at DESC`
	if f.Limit > 0 {
		q += ` LIMIT ?`
		args = append(args, f.Limit)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Change describes a journaled state transition.
type Change struct {
	State           string
	Detail          string
	Code            string
	Remediation     string
	Attempt         int // 0 = unchanged
	PendingApproval *bool
	RemoteSeq       int // requester mirror only
	RemoteState     string
}

// Transition journals a state change: it updates the task row and appends to
// transitions in one transaction. It returns the new transition seq.
// If check is non-nil it is called with the current row inside the
// transaction and may veto the change.
func (s *Store) Transition(ctx context.Context, id string, c Change, check func(Task) error) (Task, int, error) {
	return s.apply(ctx, id, c, check, true)
}

func (s *Store) update(ctx context.Context, id string, c Change, journal bool) (Task, error) {
	t, _, err := s.apply(ctx, id, c, nil, journal)
	return t, err
}

func (s *Store) apply(ctx context.Context, id string, c Change, check func(Task) error, journal bool) (Task, int, error) {
	var out Task
	var seq int
	err := s.tx(ctx, func(tx *sql.Tx) error {
		t, err := scanTask(tx.QueryRowContext(ctx, `SELECT `+taskCols+` FROM tasks WHERE id=?`, id))
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if check != nil {
			if err := check(t); err != nil {
				return err
			}
		}
		now := Now()
		t.State, t.Detail, t.Code, t.Remediation, t.UpdatedAt = c.State, c.Detail, c.Code, c.Remediation, now
		if c.Attempt > 0 {
			t.Attempt = c.Attempt
		}
		if c.PendingApproval != nil {
			t.PendingApproval = *c.PendingApproval
		}
		if c.RemoteSeq > 0 {
			t.RemoteSeq = c.RemoteSeq
			t.LastSynced = now
		}
		if c.RemoteState != "" {
			t.RemoteState = c.RemoteState
		}
		if _, err := tx.ExecContext(ctx, `UPDATE tasks SET state=?,detail=?,code=?,remediation=?,updated_at=?,attempt=?,
			pending_approval=?,remote_seq=?,remote_state=?,last_synced=? WHERE id=?`,
			t.State, t.Detail, t.Code, t.Remediation, now, t.Attempt, b2i(t.PendingApproval), t.RemoteSeq, t.RemoteState,
			t.LastSynced, id); err != nil {
			return err
		}
		if !journal {
			out = t
			return nil
		}
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq),0)+1 FROM transitions WHERE task_id=?`, id).Scan(&seq); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO transitions(task_id,seq,state,detail,ts) VALUES(?,?,?,?,?)`,
			id, seq, c.State, c.Detail, now); err != nil {
			return err
		}
		out = t
		return nil
	})
	return out, seq, err
}

func (s *Store) Transitions(ctx context.Context, id string) ([]Transition, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT seq,state,detail,ts FROM transitions WHERE task_id=? ORDER BY seq`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Transition
	for rows.Next() {
		var t Transition
		if err := rows.Scan(&t.Seq, &t.State, &t.Detail, &t.TS); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) LastSeq(ctx context.Context, id string) (int, error) {
	var seq int
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq),0) FROM transitions WHERE task_id=?`, id).Scan(&seq)
	return seq, err
}

func (s *Store) SetPID(ctx context.Context, id string, pid int, startedAt string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE tasks SET pid=?, started_at=? WHERE id=?`, pid, startedAt, id)
	return err
}

func (s *Store) SetLogSize(ctx context.Context, id string, n int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE tasks SET log_size=? WHERE id=?`, n, id)
	return err
}

func (s *Store) SetWSDir(ctx context.Context, id, dir string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE tasks SET ws_dir=? WHERE id=?`, dir, id)
	return err
}

func (s *Store) TouchSynced(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE tasks SET last_synced=? WHERE id=?`, Now(), id)
	return err
}

// SetRemoteSeq records the executor's latest seq without a visible change.
func (s *Store) SetRemoteSeq(ctx context.Context, id string, seq int) error {
	_, err := s.db.ExecContext(ctx, `UPDATE tasks SET remote_seq=MAX(remote_seq,?), last_synced=? WHERE id=?`, seq, Now(), id)
	return err
}

// UpdateInPlace applies c to the task row without appending a transition
// (the requester mirror uses it when only details change, not the state).
func (s *Store) UpdateInPlace(ctx context.Context, id string, c Change) (Task, error) {
	return s.update(ctx, id, c, false)
}
