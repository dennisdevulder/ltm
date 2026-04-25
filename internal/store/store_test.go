package store

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// openTestStore opens a fresh SQLite DB in a test-local temp dir. modernc sqlite
// supports on-disk paths; in-memory (":memory:") works too but file-backed lets
// us verify persistence across Open calls when needed.
func openTestStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ltm.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// seedUser inserts a user and returns its id, so tests can write owned packets
// without the middleware wiring.
func seedUser(t *testing.T, s *Store, display string) string {
	t.Helper()
	id := NewULID()
	if err := s.PutUser(context.Background(), id, "", display); err != nil {
		t.Fatalf("PutUser: %v", err)
	}
	return id
}

// seedTeam creates a team owned by ownerID.
func seedTeam(t *testing.T, s *Store, name, ownerID string) string {
	t.Helper()
	id := NewULID()
	if err := s.CreateTeam(context.Background(), id, name, ownerID); err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	return id
}

func TestPutAndGetPacket(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	created := time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC)
	owner := seedUser(t, s, "alice")

	if err := s.PutPacket(ctx, "abc123", created, "build a feature", []byte(`{"goal":"x"}`), owner, ""); err != nil {
		t.Fatalf("PutPacket: %v", err)
	}
	row, err := s.GetPacket(ctx, "abc123")
	if err != nil {
		t.Fatalf("GetPacket: %v", err)
	}
	if row.ID != "abc123" || row.Goal != "build a feature" {
		t.Errorf("unexpected row: %+v", row)
	}
	if row.OwnerID != owner {
		t.Errorf("OwnerID = %q, want %q", row.OwnerID, owner)
	}
	if row.TeamID != "" {
		t.Errorf("TeamID = %q, want empty for personal packet", row.TeamID)
	}
	if !row.CreatedAt.Equal(created) {
		t.Errorf("CreatedAt = %v, want %v", row.CreatedAt, created)
	}
	if string(row.Body) != `{"goal":"x"}` {
		t.Errorf("Body = %q, want %q", row.Body, `{"goal":"x"}`)
	}
}

func TestGetPacket_NotFound(t *testing.T) {
	s := openTestStore(t)
	_, err := s.GetPacket(context.Background(), "does-not-exist")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got: %v", err)
	}
}

func TestPutPacket_UpsertsOnSameID(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	owner := seedUser(t, s, "alice")

	if err := s.PutPacket(ctx, "id1", now, "v1", []byte("first"), owner, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.PutPacket(ctx, "id1", now, "v1", []byte("second"), owner, ""); err != nil {
		t.Fatalf("second Put on same id should succeed: %v", err)
	}
	row, err := s.GetPacket(ctx, "id1")
	if err != nil {
		t.Fatal(err)
	}
	if string(row.Body) != "second" {
		t.Errorf("Body = %q, want %q", row.Body, "second")
	}
}

func TestDeletePacket_HidesFromGetAndList(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	owner := seedUser(t, s, "alice")

	_ = s.PutPacket(ctx, "id1", now, "g", []byte("b"), owner, "")
	if err := s.DeletePacket(ctx, "id1"); err != nil {
		t.Fatalf("DeletePacket: %v", err)
	}
	if _, err := s.GetPacket(ctx, "id1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after Delete: expected ErrNotFound, got: %v", err)
	}
	rows, err := s.ListPacketsForOwner(ctx, owner, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Errorf("List after Delete: expected 0 rows, got %d", len(rows))
	}
}

func TestDeletePacket_IdempotentReturnsNotFoundOnSecondCall(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	owner := seedUser(t, s, "alice")
	_ = s.PutPacket(ctx, "id1", time.Now().UTC(), "g", []byte("b"), owner, "")

	if err := s.DeletePacket(ctx, "id1"); err != nil {
		t.Fatalf("first Delete: %v", err)
	}
	err := s.DeletePacket(ctx, "id1")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("second Delete: expected ErrNotFound, got: %v", err)
	}
}

func TestDeletePacket_NonExistentReturnsNotFound(t *testing.T) {
	s := openTestStore(t)
	err := s.DeletePacket(context.Background(), "never-existed")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got: %v", err)
	}
}

func TestPutPacket_ResurrectsSoftDeleted(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	owner := seedUser(t, s, "alice")

	_ = s.PutPacket(ctx, "id1", now, "g", []byte("first"), owner, "")
	_ = s.DeletePacket(ctx, "id1")

	if err := s.PutPacket(ctx, "id1", now, "g", []byte("resurrected"), owner, ""); err != nil {
		t.Fatal(err)
	}
	row, err := s.GetPacket(ctx, "id1")
	if err != nil {
		t.Fatalf("Get after resurrection: %v", err)
	}
	if string(row.Body) != "resurrected" {
		t.Errorf("Body = %q, want %q", row.Body, "resurrected")
	}
}

func TestListPackets_OrderedByCreatedDesc(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	base := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	owner := seedUser(t, s, "alice")

	_ = s.PutPacket(ctx, "middle", base.Add(1*time.Hour), "m", []byte("m"), owner, "")
	_ = s.PutPacket(ctx, "oldest", base, "o", []byte("o"), owner, "")
	_ = s.PutPacket(ctx, "newest", base.Add(2*time.Hour), "n", []byte("n"), owner, "")

	rows, err := s.ListPacketsForOwner(ctx, owner, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(rows))
	}
	want := []string{"newest", "middle", "oldest"}
	for i, r := range rows {
		if r.ID != want[i] {
			t.Errorf("rows[%d] = %q, want %q", i, r.ID, want[i])
		}
	}
}

func TestListPackets_LimitClampedToDefaultWhenInvalid(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	owner := seedUser(t, s, "alice")
	for _, invalid := range []int{0, -5, 1000} {
		_, err := s.ListPacketsForOwner(ctx, owner, invalid)
		if err != nil {
			t.Errorf("ListPacketsForOwner(limit=%d) errored: %v", invalid, err)
		}
	}
}

func TestListPackets_RespectsLimit(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	owner := seedUser(t, s, "alice")
	base := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		id := "id" + string(rune('0'+i))
		_ = s.PutPacket(ctx, id, base.Add(time.Duration(i)*time.Minute), "g", []byte("b"), owner, "")
	}
	rows, err := s.ListPacketsForOwner(ctx, owner, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Errorf("got %d rows, want 3", len(rows))
	}
}

func TestListPackets_ExcludesSoftDeleted(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	owner := seedUser(t, s, "alice")

	_ = s.PutPacket(ctx, "alive", now, "a", []byte("a"), owner, "")
	_ = s.PutPacket(ctx, "gone", now, "g", []byte("g"), owner, "")
	_ = s.DeletePacket(ctx, "gone")

	rows, _ := s.ListPacketsForOwner(ctx, owner, 100)
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1 (only alive)", len(rows))
	}
	if rows[0].ID != "alive" {
		t.Errorf("rows[0].ID = %q, want alive", rows[0].ID)
	}
}

// ---- tokens ----

func TestTokens_PutExistsAny(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	any, err := s.AnyToken(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if any {
		t.Error("AnyToken on fresh store should be false")
	}

	if err := s.PutTokenHash(ctx, "hash1", "laptop"); err != nil {
		t.Fatalf("PutTokenHash: %v", err)
	}

	exists, err := s.TokenExists(ctx, "hash1")
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Error("TokenExists(hash1) = false, want true")
	}

	exists, err = s.TokenExists(ctx, "unknown")
	if err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Error("TokenExists(unknown) = true, want false")
	}

	any, _ = s.AnyToken(ctx)
	if !any {
		t.Error("AnyToken after insert = false, want true")
	}
}

func TestPutTokenHash_DuplicateIsNoop(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if err := s.PutTokenHash(ctx, "h", "one"); err != nil {
		t.Fatal(err)
	}
	if err := s.PutTokenHash(ctx, "h", "two"); err != nil {
		t.Errorf("duplicate PutTokenHash should be noop, got: %v", err)
	}
}

func TestUserForToken_BoundToken(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	owner := seedUser(t, s, "alice")
	if err := s.PutTokenHashForUser(ctx, "hbound", "alice-laptop", owner); err != nil {
		t.Fatal(err)
	}
	got, err := s.UserForToken(ctx, "hbound")
	if err != nil {
		t.Fatalf("UserForToken: %v", err)
	}
	if got != owner {
		t.Errorf("UserForToken = %q, want %q", got, owner)
	}
}

func TestUserForToken_UnknownReturnsNotFound(t *testing.T) {
	s := openTestStore(t)
	_, err := s.UserForToken(context.Background(), "nope")
	if !errors.Is(err, ErrUserNotFound) {
		t.Errorf("expected ErrUserNotFound, got: %v", err)
	}
}

// ---- migration: collapseToRootUser ----

func TestMigrate_CollapsesLegacyTokensToRootUser(t *testing.T) {
	// Simulate a pre-teams server: Open, insert tokens before teams schema,
	// then reopen so migrateTeams sees tokens-but-no-users and creates root.
	// With a single Open() we apply teams schema immediately, but we can
	// still insert tokens without a user_id (the column allows NULL), then
	// reach into the store to blank user_id to mimic the pre-teams state,
	// and re-apply the collapse step.
	path := filepath.Join(t.TempDir(), "ltm.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	// Insert a "legacy" token with no user and a packet with no owner.
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO tokens (token_hash, label, created_at, user_id) VALUES ('legacy', 'root', '2025-01-01T00:00:00Z', NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO packets (id, created_at, goal, body) VALUES ('pre1', '2025-01-01T00:00:00Z', 'g', X'01')`); err != nil {
		t.Fatal(err)
	}
	// Force the migration guard back to v0 so collapseToRootUser runs.
	if _, err := s.db.ExecContext(ctx, `PRAGMA user_version = 0`); err != nil {
		t.Fatal(err)
	}
	if err := s.migrateTeams(); err != nil {
		t.Fatalf("migrateTeams: %v", err)
	}

	// A root user now exists.
	var rootID string
	if err := s.db.QueryRowContext(ctx, `SELECT id FROM users WHERE display = 'root'`).Scan(&rootID); err != nil {
		t.Fatalf("root user not created: %v", err)
	}
	// The legacy token is bound to root.
	got, err := s.UserForToken(ctx, "legacy")
	if err != nil {
		t.Fatalf("UserForToken: %v", err)
	}
	if got != rootID {
		t.Errorf("legacy token user_id = %q, want root %q", got, rootID)
	}
	// The legacy packet is owned by root, personal scope.
	var owner, team string
	if err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(owner_id, ''), COALESCE(team_id, '') FROM packets WHERE id = 'pre1'`).Scan(&owner, &team); err != nil {
		t.Fatal(err)
	}
	if owner != rootID {
		t.Errorf("legacy packet owner_id = %q, want root %q", owner, rootID)
	}
	if team != "" {
		t.Errorf("legacy packet team_id = %q, want empty (personal)", team)
	}
}

func TestMigrate_IdempotentAcrossReopens(t *testing.T) {
	// Opening the same DB file twice must not double-apply the migration
	// or create a second root user. Uses a stable path so both Opens hit
	// the same sqlite file.
	path := filepath.Join(t.TempDir(), "ltm.db")

	s1, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	// Simulate pre-teams state: insert a legacy token, wipe users,
	// reset user_version, then re-run migrateTeams.
	if _, err := s1.db.ExecContext(ctx,
		`INSERT INTO tokens (token_hash, label, created_at) VALUES ('legacy', 'root', '2025-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s1.db.ExecContext(ctx, `DELETE FROM users`); err != nil {
		t.Fatal(err)
	}
	if _, err := s1.db.ExecContext(ctx, `PRAGMA user_version = 0`); err != nil {
		t.Fatal(err)
	}
	if err := s1.migrateTeams(); err != nil {
		t.Fatalf("first reapply: %v", err)
	}
	_ = s1.Close()

	// Second Open re-runs migrate(). It must be a no-op.
	s2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	var rootCount int
	if err := s2.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE display = 'root'`).Scan(&rootCount); err != nil {
		t.Fatal(err)
	}
	if rootCount != 1 {
		t.Errorf("root user count after reopen = %d, want 1 (migration ran twice?)", rootCount)
	}
	var version int
	if err := s2.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 1 {
		t.Errorf("user_version = %d, want 1", version)
	}
}

func TestCheckInviteRedeemable(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	owner := seedUser(t, s, "alice")
	teamID := seedTeam(t, s, "alpha", owner)
	now := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	_ = s.CreateInvite(ctx, "live", teamID, owner, now.Add(7*24*time.Hour))

	if err := s.CheckInviteRedeemable(ctx, "live", now); err != nil {
		t.Errorf("live invite check: got %v, want nil", err)
	}
	if err := s.CheckInviteRedeemable(ctx, "nope", now); !errors.Is(err, ErrInviteGone) {
		t.Errorf("missing invite check: got %v, want ErrInviteGone", err)
	}
	// Consume, then check.
	joiner := seedUser(t, s, "bob")
	_, _ = s.ConsumeInvite(ctx, "live", joiner, now)
	if err := s.CheckInviteRedeemable(ctx, "live", now); !errors.Is(err, ErrInviteGone) {
		t.Errorf("consumed invite check: got %v, want ErrInviteGone", err)
	}
}

// ---- teams / members ----

func TestCreateTeam_SeedsOwnerAsMember(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	owner := seedUser(t, s, "alice")
	id := NewULID()
	if err := s.CreateTeam(ctx, id, "alpha", owner); err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	role, err := s.TeamMembership(ctx, id, owner)
	if err != nil {
		t.Fatal(err)
	}
	if role != "owner" {
		t.Errorf("owner role = %q, want owner", role)
	}
}

func TestCreateTeam_DuplicateNameErrors(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	owner := seedUser(t, s, "alice")
	_ = s.CreateTeam(ctx, NewULID(), "alpha", owner)
	err := s.CreateTeam(ctx, NewULID(), "alpha", owner)
	if !errors.Is(err, ErrNameTaken) {
		t.Errorf("expected ErrNameTaken, got: %v", err)
	}
}

func TestListTeamsForUser_OnlyTheirs(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	alice := seedUser(t, s, "alice")
	bob := seedUser(t, s, "bob")
	seedTeam(t, s, "alpha", alice)
	seedTeam(t, s, "beta", bob)

	got, err := s.ListTeamsForUser(ctx, alice)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "alpha" {
		t.Errorf("alice teams = %+v, want [alpha]", got)
	}
}

func TestAddRemoveTeamMember(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	owner := seedUser(t, s, "alice")
	member := seedUser(t, s, "bob")
	teamID := seedTeam(t, s, "alpha", owner)

	if err := s.AddTeamMember(ctx, teamID, member, "member"); err != nil {
		t.Fatal(err)
	}
	role, _ := s.TeamMembership(ctx, teamID, member)
	if role != "member" {
		t.Errorf("after Add: role = %q, want member", role)
	}

	if err := s.RemoveTeamMember(ctx, teamID, member); err != nil {
		t.Fatal(err)
	}
	role, _ = s.TeamMembership(ctx, teamID, member)
	if role != "" {
		t.Errorf("after Remove: role = %q, want empty", role)
	}
}

func TestDeleteTeam_SoftDeletesPackets(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	owner := seedUser(t, s, "alice")
	teamID := seedTeam(t, s, "alpha", owner)
	_ = s.PutPacket(ctx, "pk1", time.Now().UTC(), "g", []byte("b"), owner, teamID)

	if err := s.DeleteTeam(ctx, teamID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetPacket(ctx, "pk1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("team packet still readable after team delete: %v", err)
	}
	if _, err := s.GetTeamByID(ctx, teamID); !errors.Is(err, ErrTeamNotFound) {
		t.Errorf("team still readable after delete: %v", err)
	}
}

// ---- invites ----

func TestConsumeInvite_HappyPath(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	owner := seedUser(t, s, "alice")
	joiner := seedUser(t, s, "bob")
	teamID := seedTeam(t, s, "alpha", owner)
	now := time.Now().UTC()
	_ = s.CreateInvite(ctx, "code1", teamID, owner, now.Add(7*24*time.Hour))

	inv, err := s.ConsumeInvite(ctx, "code1", joiner, now)
	if err != nil {
		t.Fatalf("ConsumeInvite: %v", err)
	}
	if inv.ConsumedAt == nil || inv.ConsumedBy != joiner {
		t.Errorf("invite not marked consumed: %+v", inv)
	}
}

func TestConsumeInvite_SecondCallIsGone(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	owner := seedUser(t, s, "alice")
	joiner := seedUser(t, s, "bob")
	teamID := seedTeam(t, s, "alpha", owner)
	now := time.Now().UTC()
	_ = s.CreateInvite(ctx, "code1", teamID, owner, now.Add(7*24*time.Hour))

	if _, err := s.ConsumeInvite(ctx, "code1", joiner, now); err != nil {
		t.Fatal(err)
	}
	_, err := s.ConsumeInvite(ctx, "code1", joiner, now)
	if !errors.Is(err, ErrInviteGone) {
		t.Errorf("second consume: expected ErrInviteGone, got: %v", err)
	}
}

func TestConsumeInvite_ConcurrentRedeemsExactlyOnce(t *testing.T) {
	// If two callers race the SELECT → UPDATE window, only one should see
	// success. The guard is the `WHERE consumed_at IS NULL` predicate plus
	// RowsAffected. Regression: prior to this check, both callers could
	// return nil and hand out tokens for the same invite.
	s := openTestStore(t)
	ctx := context.Background()
	owner := seedUser(t, s, "alice")
	teamID := seedTeam(t, s, "alpha", owner)
	now := time.Now().UTC()
	_ = s.CreateInvite(ctx, "race", teamID, owner, now.Add(7*24*time.Hour))

	joiner1 := seedUser(t, s, "bob")
	joiner2 := seedUser(t, s, "carol")

	const parallel = 8
	results := make(chan error, parallel)
	start := make(chan struct{})
	for i := 0; i < parallel; i++ {
		who := joiner1
		if i%2 == 0 {
			who = joiner2
		}
		go func(uid string) {
			<-start
			_, err := s.ConsumeInvite(ctx, "race", uid, now)
			results <- err
		}(who)
	}
	close(start)

	var successes, gones int
	for i := 0; i < parallel; i++ {
		err := <-results
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrInviteGone):
			gones++
		default:
			t.Errorf("unexpected error: %v", err)
		}
	}
	if successes != 1 {
		t.Errorf("successes = %d, want exactly 1", successes)
	}
	if gones != parallel-1 {
		t.Errorf("gones = %d, want %d", gones, parallel-1)
	}
}

func TestConsumeInvite_ExpiredIsGone(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	owner := seedUser(t, s, "alice")
	joiner := seedUser(t, s, "bob")
	teamID := seedTeam(t, s, "alpha", owner)
	issued := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	_ = s.CreateInvite(ctx, "code1", teamID, owner, issued.Add(7*24*time.Hour))

	// 8 days later — past the TTL.
	later := issued.Add(8 * 24 * time.Hour)
	_, err := s.ConsumeInvite(ctx, "code1", joiner, later)
	if !errors.Is(err, ErrInviteGone) {
		t.Errorf("expired consume: expected ErrInviteGone, got: %v", err)
	}
}

// ---- RedeemInviteAsNewUser display fallback ----

func TestRedeemInviteAsNewUser_DisplayFallbackUsesUserID(t *testing.T) {
	// Empty display should yield "invited-<userID tail>", not anything
	// derived from the invite code. Documents the privacy choice: invite
	// codes are sensitive single-use bearers and shouldn't leak into
	// permanent user records.
	s := openTestStore(t)
	ctx := context.Background()
	owner := seedUser(t, s, "alice")
	teamID := seedTeam(t, s, "alpha", owner)
	now := time.Now().UTC()
	_ = s.CreateInvite(ctx, "code-with-known-prefix", teamID, owner, now.Add(7*24*time.Hour))

	uid, _, err := s.RedeemInviteAsNewUser(ctx, "code-with-known-prefix", "", "tokhash", now)
	if err != nil {
		t.Fatalf("RedeemInviteAsNewUser: %v", err)
	}
	u, err := s.GetUser(ctx, uid)
	if err != nil {
		t.Fatal(err)
	}
	wantSuffix := uid[len(uid)-6:]
	want := "invited-" + wantSuffix
	if u.Display != want {
		t.Errorf("display = %q, want %q", u.Display, want)
	}
	if strings.Contains(u.Display, "code-with") {
		t.Errorf("display %q leaks invite code prefix", u.Display)
	}
}

func TestRedeemInviteAsNewUser_RespectsExplicitDisplay(t *testing.T) {
	// A non-empty display should be used verbatim — the fallback only
	// kicks in when the caller didn't supply one.
	s := openTestStore(t)
	ctx := context.Background()
	owner := seedUser(t, s, "alice")
	teamID := seedTeam(t, s, "alpha", owner)
	now := time.Now().UTC()
	_ = s.CreateInvite(ctx, "code1", teamID, owner, now.Add(7*24*time.Hour))

	uid, _, err := s.RedeemInviteAsNewUser(ctx, "code1", "carol", "tokhash", now)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := s.GetUser(ctx, uid)
	if u.Display != "carol" {
		t.Errorf("display = %q, want carol", u.Display)
	}
}

// ---- ConsumeInviteForExistingUser ----

func TestConsumeInviteForExistingUser_HappyPath(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	owner := seedUser(t, s, "alice")
	bob := seedUser(t, s, "bob")
	teamID := seedTeam(t, s, "alpha", owner)
	now := time.Now().UTC()
	_ = s.CreateInvite(ctx, "code1", teamID, owner, now.Add(7*24*time.Hour))

	inv, err := s.ConsumeInviteForExistingUser(ctx, "code1", bob, now)
	if err != nil {
		t.Fatalf("ConsumeInviteForExistingUser: %v", err)
	}
	if inv.TeamID != teamID {
		t.Errorf("inv.TeamID = %q, want %q", inv.TeamID, teamID)
	}
	role, _ := s.TeamMembership(ctx, teamID, bob)
	if role != "member" {
		t.Errorf("bob role = %q, want member", role)
	}
}

func TestConsumeInviteForExistingUser_GoneRollsBackMembership(t *testing.T) {
	// If the invite is already consumed, the membership insert must not
	// happen — the whole tx rolls back.
	s := openTestStore(t)
	ctx := context.Background()
	owner := seedUser(t, s, "alice")
	bob := seedUser(t, s, "bob")
	carol := seedUser(t, s, "carol")
	teamID := seedTeam(t, s, "alpha", owner)
	now := time.Now().UTC()
	_ = s.CreateInvite(ctx, "code1", teamID, owner, now.Add(7*24*time.Hour))

	if _, err := s.ConsumeInviteForExistingUser(ctx, "code1", bob, now); err != nil {
		t.Fatal(err)
	}
	// Carol racing the now-consumed code: must get ErrInviteGone and
	// must NOT be added as a member.
	_, err := s.ConsumeInviteForExistingUser(ctx, "code1", carol, now)
	if !errors.Is(err, ErrInviteGone) {
		t.Errorf("err = %v, want ErrInviteGone", err)
	}
	role, _ := s.TeamMembership(ctx, teamID, carol)
	if role != "" {
		t.Errorf("carol role = %q, want empty (tx rollback)", role)
	}
}

// ---- meta ----

func TestOpen_WALModeEnabled(t *testing.T) {
	s := openTestStore(t)
	var mode string
	if err := s.db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(mode, "wal") {
		t.Errorf("journal_mode = %q, want wal", mode)
	}
}

func TestOpen_InvalidPathErrors(t *testing.T) {
	_, err := Open(filepath.Join(t.TempDir(), "nope", "does-not-exist", "ltm.db"))
	if err == nil {
		t.Fatal("expected Open to error on bogus path, got nil")
	}
}
