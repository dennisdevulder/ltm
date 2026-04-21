package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

var ErrNotFound = errors.New("packet not found")

type PacketRow struct {
	ID        string
	CreatedAt time.Time
	Goal      string
	Body      []byte
}

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(on)")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
	CREATE TABLE IF NOT EXISTS packets (
		id         TEXT PRIMARY KEY,
		created_at TEXT NOT NULL,
		goal       TEXT NOT NULL,
		body       BLOB NOT NULL,
		deleted_at TEXT
	);
	CREATE INDEX IF NOT EXISTS idx_packets_created ON packets(created_at DESC);

	CREATE TABLE IF NOT EXISTS tokens (
		token_hash TEXT PRIMARY KEY,
		label      TEXT NOT NULL,
		created_at TEXT NOT NULL
	);
	`)
	return err
}

func (s *Store) PutPacket(ctx context.Context, id string, createdAt time.Time, goal string, body []byte) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO packets (id, created_at, goal, body) VALUES (?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET body=excluded.body, deleted_at=NULL`,
		id, createdAt.UTC().Format(time.RFC3339Nano), goal, body,
	)
	return err
}

func (s *Store) GetPacket(ctx context.Context, id string) (*PacketRow, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, created_at, goal, body FROM packets WHERE id = ? AND deleted_at IS NULL`, id)
	var r PacketRow
	var created string
	if err := row.Scan(&r.ID, &created, &r.Goal, &r.Body); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	t, _ := time.Parse(time.RFC3339Nano, created)
	r.CreatedAt = t
	return &r, nil
}

func (s *Store) ListPackets(ctx context.Context, limit int) ([]PacketRow, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, created_at, goal FROM packets WHERE deleted_at IS NULL ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PacketRow
	for rows.Next() {
		var r PacketRow
		var created string
		if err := rows.Scan(&r.ID, &created, &r.Goal); err != nil {
			return nil, err
		}
		t, _ := time.Parse(time.RFC3339Nano, created)
		r.CreatedAt = t
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) DeletePacket(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE packets SET deleted_at = ? WHERE id = ? AND deleted_at IS NULL`,
		time.Now().UTC().Format(time.RFC3339Nano), id,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- tokens ----

func (s *Store) PutTokenHash(ctx context.Context, hash, label string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO tokens (token_hash, label, created_at) VALUES (?, ?, ?)
		 ON CONFLICT(token_hash) DO NOTHING`,
		hash, label, time.Now().UTC().Format(time.RFC3339Nano),
	)
	return err
}

func (s *Store) TokenExists(ctx context.Context, hash string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tokens WHERE token_hash = ?`, hash).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func (s *Store) AnyToken(ctx context.Context) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tokens`).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}
