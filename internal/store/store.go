package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
	sqlite3 "modernc.org/sqlite"
	sqlite3lib "modernc.org/sqlite/lib"
)

// NewULID returns a fresh ULID as a string. Exposed so the api layer can
// mint team/user ids without depending on oklog/ulid directly.
func NewULID() string {
	return ulid.Make().String()
}

// isUniqueViolation returns true when err is a SQLite UNIQUE-constraint
// violation. Uses the typed driver error (extended code 2067) instead of
// substring-matching the message, which would silently break across driver
// or SQLite-version upgrades.
func isUniqueViolation(err error) bool {
	var se *sqlite3.Error
	if errors.As(err, &se) && se.Code() == sqlite3lib.SQLITE_CONSTRAINT_UNIQUE {
		return true
	}
	return false
}

var (
	ErrNotFound     = errors.New("packet not found")
	ErrUserNotFound = errors.New("user not found")
	ErrTeamNotFound = errors.New("team not found")
	ErrInviteGone   = errors.New("invite expired or already consumed")
	ErrNameTaken    = errors.New("team name already taken")
)

// PacketRow mirrors the packets table, including scope columns.
// OwnerID is the user who pushed the packet. TeamID is empty for personal
// packets; non-empty means the packet is scoped to that team.
type PacketRow struct {
	ID        string
	CreatedAt time.Time
	Goal      string
	Body      []byte
	OwnerID   string
	TeamID    string
}

type User struct {
	ID        string
	Email     string
	Display   string
	CreatedAt time.Time
}

type Team struct {
	ID        string
	Name      string
	OwnerID   string
	CreatedAt time.Time
}

type Member struct {
	UserID   string
	Display  string
	Email    string
	Role     string
	JoinedAt time.Time
}

type Invite struct {
	Code        string
	TeamID      string
	CreatedBy   string
	CreatedAt   time.Time
	ExpiresAt   time.Time
	ConsumedAt  *time.Time
	ConsumedBy  string
}

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	// busy_timeout lets concurrent writers queue on the SQLite write lock
	// instead of failing with SQLITE_BUSY. Without it, two simultaneous
	// ConsumeInvite calls racing the same invite can both surface a
	// database-is-locked error before either gets a chance to commit.
	db, err := sql.Open("sqlite",
		path+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(on)&_pragma=busy_timeout(5000)")
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
	if _, err := s.db.Exec(`
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
	`); err != nil {
		return err
	}
	return s.migrateTeams()
}

// migrateTeams applies the teams schema on top of the pre-teams (v0) schema.
// Uses PRAGMA user_version to track that it has run. Idempotent: safe to call
// repeatedly on an already-migrated database.
//
// Migration rule for existing self-hosted servers: when tokens exist but users
// don't, collapse them to a single root user. Today every token is
// interchangeable, so a single logical user is lossless. Every pre-teams
// packet becomes owned by that root user (personal scope, team_id=NULL).
func (s *Store) migrateTeams() error {
	var version int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read user_version: %w", err)
	}
	if version >= 1 {
		return nil
	}
	stmts := []string{
		// SQLite's UNIQUE constraint allows multiple NULL emails — different
		// from Postgres, where NULLs collide. We rely on this so anonymous
		// invite-accept users (no email) don't trip a uniqueness error.
		`CREATE TABLE IF NOT EXISTS users (
			id         TEXT PRIMARY KEY,
			email      TEXT UNIQUE,
			display    TEXT NOT NULL,
			created_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS teams (
			id         TEXT PRIMARY KEY,
			name       TEXT NOT NULL UNIQUE,
			owner_id   TEXT NOT NULL REFERENCES users(id),
			created_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS team_members (
			team_id   TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
			user_id   TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			role      TEXT NOT NULL,
			joined_at TEXT NOT NULL,
			PRIMARY KEY (team_id, user_id)
		)`,
		`CREATE TABLE IF NOT EXISTS invites (
			code        TEXT PRIMARY KEY,
			team_id     TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
			created_by  TEXT NOT NULL REFERENCES users(id),
			created_at  TEXT NOT NULL,
			expires_at  TEXT NOT NULL,
			consumed_at TEXT,
			consumed_by TEXT REFERENCES users(id)
		)`,
	}
	for _, q := range stmts {
		if _, err := s.db.Exec(q); err != nil {
			return fmt.Errorf("create teams schema: %w", err)
		}
	}
	for _, alter := range []struct{ table, col, typ string }{
		{"tokens", "user_id", "TEXT REFERENCES users(id)"},
		{"packets", "owner_id", "TEXT REFERENCES users(id)"},
		{"packets", "team_id", "TEXT REFERENCES teams(id)"},
	} {
		if err := s.addColumnIfMissing(alter.table, alter.col, alter.typ); err != nil {
			return err
		}
	}
	for _, q := range []string{
		`CREATE INDEX IF NOT EXISTS idx_packets_owner ON packets(owner_id) WHERE team_id IS NULL`,
		`CREATE INDEX IF NOT EXISTS idx_packets_team  ON packets(team_id)  WHERE team_id IS NOT NULL`,
	} {
		if _, err := s.db.Exec(q); err != nil {
			return fmt.Errorf("create index: %w", err)
		}
	}

	if err := s.collapseToRootUser(); err != nil {
		return err
	}
	if _, err := s.db.Exec(`PRAGMA user_version = 1`); err != nil {
		return fmt.Errorf("bump user_version: %w", err)
	}
	return nil
}

// addColumnIfMissing runs ALTER TABLE ADD COLUMN only when the column is
// actually missing — SQLite errors otherwise, and our migration must be
// safe to re-run on already-migrated databases.
func (s *Store) addColumnIfMissing(table, col, typ string) error {
	rows, err := s.db.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return fmt.Errorf("inspect %s: %w", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return err
		}
		if strings.EqualFold(name, col) {
			return nil
		}
	}
	if _, err := s.db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, col, typ)); err != nil {
		return fmt.Errorf("add column %s.%s: %w", table, col, err)
	}
	return nil
}

// collapseToRootUser runs once, only when users is empty and the server has
// pre-teams state. See migrateTeams for the rationale.
func (s *Store) collapseToRootUser() error {
	var userCount int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&userCount); err != nil {
		return err
	}
	if userCount > 0 {
		return nil
	}
	var tokenCount int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM tokens`).Scan(&tokenCount); err != nil {
		return err
	}
	if tokenCount == 0 {
		return nil
	}
	rootID := NewULID()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := s.db.Exec(
		`INSERT INTO users (id, email, display, created_at) VALUES (?, NULL, 'root', ?)`,
		rootID, now,
	); err != nil {
		return fmt.Errorf("seed root user: %w", err)
	}
	if _, err := s.db.Exec(`UPDATE tokens SET user_id = ? WHERE user_id IS NULL`, rootID); err != nil {
		return fmt.Errorf("bind tokens to root: %w", err)
	}
	if _, err := s.db.Exec(`UPDATE packets SET owner_id = ? WHERE owner_id IS NULL`, rootID); err != nil {
		return fmt.Errorf("bind packets to root: %w", err)
	}
	return nil
}

// ---- packets ----

func (s *Store) PutPacket(ctx context.Context, id string, createdAt time.Time, goal string, body []byte, ownerID, teamID string) error {
	var team any
	if teamID != "" {
		team = teamID
	}
	var owner any
	if ownerID != "" {
		owner = ownerID
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO packets (id, created_at, goal, body, owner_id, team_id) VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET body=excluded.body, deleted_at=NULL`,
		id, createdAt.UTC().Format(time.RFC3339Nano), goal, body, owner, team,
	)
	return err
}

func (s *Store) GetPacket(ctx context.Context, id string) (*PacketRow, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, created_at, goal, body,
				COALESCE(owner_id, ''), COALESCE(team_id, '')
		 FROM packets WHERE id = ? AND deleted_at IS NULL`, id)
	var r PacketRow
	var created string
	if err := row.Scan(&r.ID, &created, &r.Goal, &r.Body, &r.OwnerID, &r.TeamID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	t, _ := time.Parse(time.RFC3339Nano, created)
	r.CreatedAt = t
	return &r, nil
}

// ListPacketsForOwner lists personal packets (team_id IS NULL) for the caller.
// ownerID empty returns zero rows — callers must always supply a user.
func (s *Store) ListPacketsForOwner(ctx context.Context, ownerID string, limit int) ([]PacketRow, error) {
	if ownerID == "" {
		return nil, nil
	}
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, created_at, goal FROM packets
		 WHERE deleted_at IS NULL AND team_id IS NULL AND owner_id = ?
		 ORDER BY created_at DESC LIMIT ?`, ownerID, limit)
	if err != nil {
		return nil, err
	}
	return scanPacketList(rows)
}

// ListPacketsForTeam lists a team's packets. Caller must be a member — this
// is enforced by the API layer before calling.
func (s *Store) ListPacketsForTeam(ctx context.Context, teamID string, limit int) ([]PacketRow, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, created_at, goal FROM packets
		 WHERE deleted_at IS NULL AND team_id = ?
		 ORDER BY created_at DESC LIMIT ?`, teamID, limit)
	if err != nil {
		return nil, err
	}
	return scanPacketList(rows)
}

func scanPacketList(rows *sql.Rows) ([]PacketRow, error) {
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
	return s.PutTokenHashForUser(ctx, hash, label, "")
}

// PutTokenHashForUser binds a bearer token hash to a user. userID may be
// empty during the brief pre-teams window before a root user exists; the
// migration then assigns the token to root.
func (s *Store) PutTokenHashForUser(ctx context.Context, hash, label, userID string) error {
	var uid any
	if userID != "" {
		uid = userID
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO tokens (token_hash, label, created_at, user_id) VALUES (?, ?, ?, ?)
		 ON CONFLICT(token_hash) DO NOTHING`,
		hash, label, time.Now().UTC().Format(time.RFC3339Nano), uid,
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

// CountUsers returns the total number of users on the server. Exposed for
// tests and for ops endpoints that may want a simple health/size readout.
func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

// CountTokens mirrors CountUsers for the tokens table.
func (s *Store) CountTokens(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tokens`).Scan(&n)
	return n, err
}

// UserForToken resolves a bearer-token hash to a user id. Returns
// ErrUserNotFound if the token is unknown or unbound.
func (s *Store) UserForToken(ctx context.Context, hash string) (string, error) {
	var uid sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT user_id FROM tokens WHERE token_hash = ?`, hash).Scan(&uid)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrUserNotFound
		}
		return "", err
	}
	if !uid.Valid || uid.String == "" {
		return "", ErrUserNotFound
	}
	return uid.String, nil
}

// ---- users ----

// PutUser inserts a user record. Re-inserting the same id is a no-op.
// email may be empty; if non-empty it must be unique across users.
func (s *Store) PutUser(ctx context.Context, id, email, display string) error {
	var e any
	if email != "" {
		e = email
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO users (id, email, display, created_at) VALUES (?, ?, ?, ?)
		 ON CONFLICT(id) DO NOTHING`,
		id, e, display, time.Now().UTC().Format(time.RFC3339Nano),
	)
	return err
}

func (s *Store) GetUser(ctx context.Context, id string) (*User, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, COALESCE(email, ''), display, created_at FROM users WHERE id = ?`, id)
	return scanUser(row)
}

func (s *Store) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	if email == "" {
		return nil, ErrUserNotFound
	}
	row := s.db.QueryRowContext(ctx,
		`SELECT id, COALESCE(email, ''), display, created_at FROM users WHERE email = ?`, email)
	return scanUser(row)
}

func scanUser(row *sql.Row) (*User, error) {
	var u User
	var created string
	if err := row.Scan(&u.ID, &u.Email, &u.Display, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		return nil, err
	}
	u.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	return &u, nil
}

// ---- teams ----

func (s *Store) CreateTeam(ctx context.Context, id, name, ownerID string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO teams (id, name, owner_id, created_at) VALUES (?, ?, ?, ?)`,
		id, name, ownerID, now,
	); err != nil {
		if isUniqueViolation(err) {
			return ErrNameTaken
		}
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO team_members (team_id, user_id, role, joined_at) VALUES (?, ?, 'owner', ?)`,
		id, ownerID, now,
	); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) GetTeamByName(ctx context.Context, name string) (*Team, error) {
	return s.scanTeam(s.db.QueryRowContext(ctx,
		`SELECT id, name, owner_id, created_at FROM teams WHERE name = ?`, name))
}

func (s *Store) GetTeamByID(ctx context.Context, id string) (*Team, error) {
	return s.scanTeam(s.db.QueryRowContext(ctx,
		`SELECT id, name, owner_id, created_at FROM teams WHERE id = ?`, id))
}

func (s *Store) scanTeam(row *sql.Row) (*Team, error) {
	var t Team
	var created string
	if err := row.Scan(&t.ID, &t.Name, &t.OwnerID, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrTeamNotFound
		}
		return nil, err
	}
	t.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	return &t, nil
}

// DeleteTeam removes the team, its members, its invites, and soft-deletes its
// packets. Members and invites are removed by foreign-key cascade; packets
// keep their soft-delete semantics (deleted_at set, row retained for audit)
// but have team_id nulled out so the teams row can actually be removed —
// SQLite can't add ON DELETE SET NULL to an existing column after the fact.
func (s *Store) DeleteTeam(ctx context.Context, teamID string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`UPDATE packets SET deleted_at = ? WHERE team_id = ? AND deleted_at IS NULL`,
		now, teamID,
	); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE packets SET team_id = NULL WHERE team_id = ?`, teamID,
	); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM teams WHERE id = ?`, teamID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrTeamNotFound
	}
	return tx.Commit()
}

// ListTeamsForUser returns every team the user is a member of, ordered by
// creation time.
func (s *Store) ListTeamsForUser(ctx context.Context, userID string) ([]Team, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT t.id, t.name, t.owner_id, t.created_at
		 FROM teams t JOIN team_members m ON m.team_id = t.id
		 WHERE m.user_id = ?
		 ORDER BY t.created_at ASC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Team
	for rows.Next() {
		var t Team
		var created string
		if err := rows.Scan(&t.ID, &t.Name, &t.OwnerID, &created); err != nil {
			return nil, err
		}
		t.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		out = append(out, t)
	}
	return out, rows.Err()
}

// TeamMembership returns the caller's role on a team, or "" if not a member.
func (s *Store) TeamMembership(ctx context.Context, teamID, userID string) (string, error) {
	var role string
	err := s.db.QueryRowContext(ctx,
		`SELECT role FROM team_members WHERE team_id = ? AND user_id = ?`,
		teamID, userID).Scan(&role)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	return role, nil
}

func (s *Store) AddTeamMember(ctx context.Context, teamID, userID, role string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO team_members (team_id, user_id, role, joined_at) VALUES (?, ?, ?, ?)
		 ON CONFLICT(team_id, user_id) DO NOTHING`,
		teamID, userID, role, time.Now().UTC().Format(time.RFC3339Nano),
	)
	return err
}

// RemoveTeamMember removes a user from a team. Returns ErrNotFound if the
// user wasn't a member.
func (s *Store) RemoveTeamMember(ctx context.Context, teamID, userID string) error {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM team_members WHERE team_id = ? AND user_id = ?`,
		teamID, userID,
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

func (s *Store) ListTeamMembers(ctx context.Context, teamID string) ([]Member, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT m.user_id, u.display, COALESCE(u.email, ''), m.role, m.joined_at
		 FROM team_members m JOIN users u ON u.id = m.user_id
		 WHERE m.team_id = ?
		 ORDER BY m.joined_at ASC`, teamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Member
	for rows.Next() {
		var m Member
		var joined string
		if err := rows.Scan(&m.UserID, &m.Display, &m.Email, &m.Role, &joined); err != nil {
			return nil, err
		}
		m.JoinedAt, _ = time.Parse(time.RFC3339Nano, joined)
		out = append(out, m)
	}
	return out, rows.Err()
}

// ---- invites ----

func (s *Store) CreateInvite(ctx context.Context, code, teamID, createdBy string, expiresAt time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO invites (code, team_id, created_by, created_at, expires_at)
		 VALUES (?, ?, ?, ?, ?)`,
		code, teamID, createdBy,
		time.Now().UTC().Format(time.RFC3339Nano),
		expiresAt.UTC().Format(time.RFC3339Nano),
	)
	return err
}

// CheckInviteRedeemable returns nil if the code points to a live, unexpired,
// unconsumed invite, and ErrInviteGone otherwise. This lets the API layer
// gate user-minting on invite validity so a flurry of bad /accept calls
// can't fill the users+tokens tables with orphan rows.
func (s *Store) CheckInviteRedeemable(ctx context.Context, code string, now time.Time) error {
	inv, err := s.GetInvite(ctx, code)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return ErrInviteGone
		}
		return err
	}
	if inv.ConsumedAt != nil {
		return ErrInviteGone
	}
	if !inv.ExpiresAt.After(now) {
		return ErrInviteGone
	}
	return nil
}

func (s *Store) GetInvite(ctx context.Context, code string) (*Invite, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT code, team_id, created_by, created_at, expires_at,
				COALESCE(consumed_at, ''), COALESCE(consumed_by, '')
		 FROM invites WHERE code = ?`, code)
	var inv Invite
	var created, expires, consumed string
	if err := row.Scan(&inv.Code, &inv.TeamID, &inv.CreatedBy, &created, &expires, &consumed, &inv.ConsumedBy); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	inv.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	inv.ExpiresAt, _ = time.Parse(time.RFC3339Nano, expires)
	if consumed != "" {
		t, _ := time.Parse(time.RFC3339Nano, consumed)
		inv.ConsumedAt = &t
	}
	return &inv, nil
}

// ConsumeInvite atomically redeems an invite for userID at now. It returns
// ErrInviteGone if the invite is expired, already consumed, or missing; that
// single error drives the 410 Gone response on /v1/invites/{code}/accept.
//
// Implementation note: this used to BEGIN/SELECT/UPDATE inside one txn, but
// in WAL mode that pattern hits SQLITE_BUSY_SNAPSHOT under concurrent
// callers — two deferred-txn readers race past the SELECT, then both try
// to upgrade to writer and SQLite's busy handler refuses to retry the
// upgrade. We sidestep the snapshot conflict by issuing the conditional
// UPDATE directly: SQLite serializes writers via the writer lock (with
// busy_timeout backing off), and the `WHERE consumed_at IS NULL` predicate
// means only the first writer flips the row. RowsAffected==0 → loser, no
// snapshot involved.
func (s *Store) ConsumeInvite(ctx context.Context, code, userID string, now time.Time) (*Invite, error) {
	nowStr := now.UTC().Format(time.RFC3339Nano)
	res, err := s.db.ExecContext(ctx,
		`UPDATE invites SET consumed_at = ?, consumed_by = ?
		 WHERE code = ? AND consumed_at IS NULL AND expires_at > ?`,
		nowStr, userID, code, nowStr,
	)
	if err != nil {
		return nil, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}
	if n == 0 {
		// Either no such code, already consumed, or expired — all 410.
		return nil, ErrInviteGone
	}
	// We won. Read back the row to populate the returned Invite.
	return s.GetInvite(ctx, code)
}

// RedeemInviteAsNewUser atomically mints a fresh user, binds tokenHash to
// them, consumes the invite (single-use, expiry-checked at now), and adds
// the new user as a member of the invite's team — all in one transaction.
//
// The conditional UPDATE on invites is what guards single-use semantics
// across concurrent callers. When that UPDATE matches zero rows (race lost
// or expired), the entire tx rolls back, undoing the speculative user +
// token inserts. This is the orphan-row guarantee for unauthenticated
// /v1/invites/{code}/accept calls: N concurrent unauth callers against
// the same code mint exactly one user + token + membership, no leaks.
func (s *Store) RedeemInviteAsNewUser(ctx context.Context, code, display, tokenHash string, now time.Time) (userID, teamID string, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", "", err
	}
	defer tx.Rollback()

	userID = NewULID()
	nowStr := now.UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO users (id, email, display, created_at) VALUES (?, NULL, ?, ?)`,
		userID, display, nowStr,
	); err != nil {
		return "", "", err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO tokens (token_hash, label, created_at, user_id) VALUES (?, ?, ?, ?)
		 ON CONFLICT(token_hash) DO NOTHING`,
		tokenHash, display, nowStr, userID,
	); err != nil {
		return "", "", err
	}
	res, err := tx.ExecContext(ctx,
		`UPDATE invites SET consumed_at = ?, consumed_by = ?
		 WHERE code = ? AND consumed_at IS NULL AND expires_at > ?`,
		nowStr, userID, code, nowStr,
	)
	if err != nil {
		return "", "", err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return "", "", err
	}
	if n == 0 {
		// Race lost / expired / missing — speculative inserts roll back.
		return "", "", ErrInviteGone
	}
	if err := tx.QueryRowContext(ctx,
		`SELECT team_id FROM invites WHERE code = ?`, code,
	).Scan(&teamID); err != nil {
		return "", "", err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO team_members (team_id, user_id, role, joined_at) VALUES (?, ?, 'member', ?)
		 ON CONFLICT(team_id, user_id) DO NOTHING`,
		teamID, userID, nowStr,
	); err != nil {
		return "", "", err
	}
	if err := tx.Commit(); err != nil {
		return "", "", err
	}
	return userID, teamID, nil
}
