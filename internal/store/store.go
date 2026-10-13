// Package store is the daemon's SQLite state (WAL). The daemon is the only
// writer. Every state transition is committed before it is acted on.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

const FileName = "state.db"

// ErrNotFound is returned for missing rows.
var ErrNotFound = errors.New("not found")

type Store struct {
	db *sql.DB
}

var migrations = []string{
	// v1
	`CREATE TABLE peers(
		fp TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		policy TEXT NOT NULL,
		last_addr TEXT NOT NULL DEFAULT '',
		last_seen TEXT NOT NULL DEFAULT '',
		added_at TEXT NOT NULL,
		pubkey BLOB
	);
	CREATE TABLE tasks(
		id TEXT PRIMARY KEY,
		role TEXT NOT NULL,
		peer_fp TEXT NOT NULL,
		project TEXT NOT NULL,
		name TEXT NOT NULL,
		spec_json TEXT NOT NULL,
		state TEXT NOT NULL,
		detail TEXT NOT NULL DEFAULT '',
		attempt INTEGER NOT NULL DEFAULT 1,
		pending_approval INTEGER NOT NULL DEFAULT 0,
		ws_dir TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		project_dir TEXT NOT NULL DEFAULT '',
		code TEXT NOT NULL DEFAULT '',
		remediation TEXT NOT NULL DEFAULT '',
		remote_seq INTEGER NOT NULL DEFAULT 0,
		remote_state TEXT NOT NULL DEFAULT '',
		pid INTEGER NOT NULL DEFAULT 0,
		started_at TEXT NOT NULL DEFAULT '',
		log_size INTEGER NOT NULL DEFAULT 0,
		last_synced TEXT NOT NULL DEFAULT ''
	);
	CREATE INDEX tasks_peer_state ON tasks(peer_fp, state);
	CREATE TABLE transitions(
		task_id TEXT NOT NULL,
		seq INTEGER NOT NULL,
		state TEXT NOT NULL,
		detail TEXT NOT NULL DEFAULT '',
		ts TEXT NOT NULL,
		PRIMARY KEY(task_id, seq)
	);
	CREATE TABLE artifacts(
		id TEXT PRIMARY KEY,
		task_id TEXT NOT NULL,
		path TEXT NOT NULL,
		sha256 TEXT NOT NULL,
		size INTEGER NOT NULL,
		location TEXT NOT NULL,
		producer_fp TEXT NOT NULL,
		fetched INTEGER NOT NULL DEFAULT 0,
		copy_back INTEGER NOT NULL DEFAULT 0
	);
	CREATE INDEX artifacts_task ON artifacts(task_id);
	CREATE TABLE transfers(
		task_id TEXT NOT NULL,
		sha256 TEXT NOT NULL,
		path TEXT NOT NULL,
		size INTEGER NOT NULL,
		bytes_done INTEGER NOT NULL DEFAULT 0,
		direction TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		PRIMARY KEY(task_id, sha256, direction)
	);`,
}

// Open opens (creating and migrating) $home/state.db in WAL mode.
func Open(home string) (*Store, error) {
	return OpenPath(filepath.Join(home, FileName))
}

func OpenPath(path string) (*Store, error) {
	q := url.Values{}
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "synchronous(FULL)")
	q.Add("_pragma", "busy_timeout(10000)")
	q.Add("_pragma", "foreign_keys(1)")
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?"+q.Encode())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // single writer; serialises all access
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate %s: %w", path, err)
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS schema_version(v INTEGER NOT NULL)`); err != nil {
		return err
	}
	var v int
	err := s.db.QueryRow(`SELECT v FROM schema_version`).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		if _, err := s.db.Exec(`INSERT INTO schema_version(v) VALUES(0)`); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	for i := v; i < len(migrations); i++ {
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[i]); err != nil {
			tx.Rollback()
			return err
		}
		if _, err := tx.Exec(`UPDATE schema_version SET v=?`, i+1); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// Now is the timestamp format used everywhere (RFC3339 UTC, ms precision).
func Now() string { return time.Now().UTC().Format("2006-01-02T15:04:05.000Z07:00") }

func ParseTS(s string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t
}

func (s *Store) tx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}
