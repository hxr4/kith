package store

import (
	"context"
	"database/sql"
	"errors"
)

const (
	LocLocal  = "local"
	LocRemote = "remote"

	DirIn  = "in"  // this side pulls
	DirOut = "out" // this side serves
)

type Artifact struct {
	ID         string `json:"id"`
	TaskID     string `json:"task_id"`
	Path       string `json:"path"`
	SHA256     string `json:"sha256"`
	Size       int64  `json:"size"`
	Location   string `json:"location"`
	ProducerFP string `json:"producer_fp"`
	Fetched    bool   `json:"fetched"`
	CopyBack   bool   `json:"copy_back"`
}

// ArtifactID is deterministic so re-offers are idempotent.
func ArtifactID(taskID, path string) string { return taskID + ":" + path }

func (s *Store) UpsertArtifact(ctx context.Context, a Artifact) error {
	if a.ID == "" {
		a.ID = ArtifactID(a.TaskID, a.Path)
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO artifacts(id,task_id,path,sha256,size,location,producer_fp,fetched,copy_back)
		VALUES(?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET sha256=excluded.sha256, size=excluded.size,
			location=CASE WHEN artifacts.sha256=excluded.sha256 THEN artifacts.location ELSE excluded.location END,
			fetched=CASE WHEN artifacts.sha256=excluded.sha256 THEN artifacts.fetched ELSE excluded.fetched END,
			copy_back=excluded.copy_back`,
		a.ID, a.TaskID, a.Path, a.SHA256, a.Size, a.Location, a.ProducerFP, b2i(a.Fetched), b2i(a.CopyBack))
	return err
}

func (s *Store) MarkArtifactLocal(ctx context.Context, taskID, path string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE artifacts SET location=?, fetched=1 WHERE id=?`, LocLocal, ArtifactID(taskID, path))
	return err
}

func (s *Store) ListArtifacts(ctx context.Context, taskID string) ([]Artifact, error) {
	q := `SELECT id,task_id,path,sha256,size,location,producer_fp,fetched,copy_back FROM artifacts`
	var args []any
	if taskID != "" {
		q += ` WHERE task_id=?`
		args = append(args, taskID)
	}
	q += ` ORDER BY task_id, path`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Artifact
	for rows.Next() {
		var a Artifact
		var f, cb int
		if err := rows.Scan(&a.ID, &a.TaskID, &a.Path, &a.SHA256, &a.Size, &a.Location, &a.ProducerFP, &f, &cb); err != nil {
			return nil, err
		}
		a.Fetched, a.CopyBack = f != 0, cb != 0
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) GetArtifact(ctx context.Context, taskID, path string) (Artifact, error) {
	var a Artifact
	var f, cb int
	err := s.db.QueryRowContext(ctx, `SELECT id,task_id,path,sha256,size,location,producer_fp,fetched,copy_back FROM artifacts WHERE id=?`,
		ArtifactID(taskID, path)).Scan(&a.ID, &a.TaskID, &a.Path, &a.SHA256, &a.Size, &a.Location, &a.ProducerFP, &f, &cb)
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrNotFound
	}
	a.Fetched, a.CopyBack = f != 0, cb != 0
	return a, err
}

type Transfer struct {
	TaskID    string
	SHA256    string
	Path      string
	Size      int64
	BytesDone int64
	Direction string
	UpdatedAt string
}

func (s *Store) UpsertTransfer(ctx context.Context, t Transfer) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO transfers(task_id,sha256,path,size,bytes_done,direction,updated_at)
		VALUES(?,?,?,?,?,?,?)
		ON CONFLICT(task_id,sha256,direction) DO UPDATE SET bytes_done=excluded.bytes_done, updated_at=excluded.updated_at, path=excluded.path`,
		t.TaskID, t.SHA256, t.Path, t.Size, t.BytesDone, t.Direction, Now())
	return err
}

func (s *Store) GetTransfer(ctx context.Context, taskID, sha, dir string) (Transfer, error) {
	var t Transfer
	err := s.db.QueryRowContext(ctx, `SELECT task_id,sha256,path,size,bytes_done,direction,updated_at FROM transfers
		WHERE task_id=? AND sha256=? AND direction=?`, taskID, sha, dir).
		Scan(&t.TaskID, &t.SHA256, &t.Path, &t.Size, &t.BytesDone, &t.Direction, &t.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return t, ErrNotFound
	}
	return t, err
}

func (s *Store) ListTransfers(ctx context.Context, taskID string) ([]Transfer, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT task_id,sha256,path,size,bytes_done,direction,updated_at FROM transfers WHERE task_id=? ORDER BY path`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Transfer
	for rows.Next() {
		var t Transfer
		if err := rows.Scan(&t.TaskID, &t.SHA256, &t.Path, &t.Size, &t.BytesDone, &t.Direction, &t.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
