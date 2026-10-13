package store

import (
	"context"
	"database/sql"
	"errors"
)

type Peer struct {
	FP       string `json:"fp"`
	Name     string `json:"name"`
	Policy   string `json:"policy"`
	LastAddr string `json:"last_addr"`
	LastSeen string `json:"last_seen"`
	AddedAt  string `json:"added_at"`
	PubKey   []byte `json:"-"`
}

func (s *Store) UpsertPeer(ctx context.Context, p Peer) error {
	if p.AddedAt == "" {
		p.AddedAt = Now()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO peers(fp,name,policy,last_addr,last_seen,added_at,pubkey)
		VALUES(?,?,?,?,?,?,?)
		ON CONFLICT(fp) DO UPDATE SET name=excluded.name, last_addr=excluded.last_addr, pubkey=excluded.pubkey`,
		p.FP, p.Name, p.Policy, p.LastAddr, p.LastSeen, p.AddedAt, p.PubKey)
	return err
}

func (s *Store) GetPeer(ctx context.Context, fp string) (Peer, error) {
	var p Peer
	err := s.db.QueryRowContext(ctx, `SELECT fp,name,policy,last_addr,last_seen,added_at,pubkey FROM peers WHERE fp=?`, fp).
		Scan(&p.FP, &p.Name, &p.Policy, &p.LastAddr, &p.LastSeen, &p.AddedAt, &p.PubKey)
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	return p, err
}

func (s *Store) ListPeers(ctx context.Context) ([]Peer, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT fp,name,policy,last_addr,last_seen,added_at,pubkey FROM peers ORDER BY added_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Peer
	for rows.Next() {
		var p Peer
		if err := rows.Scan(&p.FP, &p.Name, &p.Policy, &p.LastAddr, &p.LastSeen, &p.AddedAt, &p.PubKey); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) TouchPeer(ctx context.Context, fp, addr string) error {
	if addr == "" {
		_, err := s.db.ExecContext(ctx, `UPDATE peers SET last_seen=? WHERE fp=?`, Now(), fp)
		return err
	}
	_, err := s.db.ExecContext(ctx, `UPDATE peers SET last_seen=?, last_addr=? WHERE fp=?`, Now(), addr, fp)
	return err
}

func (s *Store) SetPeerPolicy(ctx context.Context, fp, policy string) error {
	r, err := s.db.ExecContext(ctx, `UPDATE peers SET policy=? WHERE fp=?`, policy, fp)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeletePeer(ctx context.Context, fp string) error {
	r, err := s.db.ExecContext(ctx, `DELETE FROM peers WHERE fp=?`, fp)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
