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

func TestPutAndGetPacket(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	created := time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC)

	if err := s.PutPacket(ctx, "abc123", created, "build a feature", []byte(`{"goal":"x"}`)); err != nil {
		t.Fatalf("PutPacket: %v", err)
	}
	row, err := s.GetPacket(ctx, "abc123")
	if err != nil {
		t.Fatalf("GetPacket: %v", err)
	}
	if row.ID != "abc123" || row.Goal != "build a feature" {
		t.Errorf("unexpected row: %+v", row)
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
	// Re-pushing the same ID should update body (ON CONFLICT DO UPDATE), not error.
	s := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	if err := s.PutPacket(ctx, "id1", now, "v1", []byte("first")); err != nil {
		t.Fatal(err)
	}
	if err := s.PutPacket(ctx, "id1", now, "v1", []byte("second")); err != nil {
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

	_ = s.PutPacket(ctx, "id1", now, "g", []byte("b"))
	if err := s.DeletePacket(ctx, "id1"); err != nil {
		t.Fatalf("DeletePacket: %v", err)
	}
	if _, err := s.GetPacket(ctx, "id1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after Delete: expected ErrNotFound, got: %v", err)
	}
	rows, err := s.ListPackets(ctx, 100)
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
	_ = s.PutPacket(ctx, "id1", time.Now().UTC(), "g", []byte("b"))

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
	// Core multi-device behavior: after a delete, pushing the same ID again
	// must undelete the row (ON CONFLICT resets deleted_at to NULL).
	s := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	_ = s.PutPacket(ctx, "id1", now, "g", []byte("first"))
	_ = s.DeletePacket(ctx, "id1")

	if err := s.PutPacket(ctx, "id1", now, "g", []byte("resurrected")); err != nil {
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

	// Insert in non-chronological order.
	_ = s.PutPacket(ctx, "middle", base.Add(1*time.Hour), "m", []byte("m"))
	_ = s.PutPacket(ctx, "oldest", base, "o", []byte("o"))
	_ = s.PutPacket(ctx, "newest", base.Add(2*time.Hour), "n", []byte("n"))

	rows, err := s.ListPackets(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(rows))
	}
	want := []string{"newest", "middle", "oldest"}
	for i, r := range rows {
		if r.ID != want[i] {
			t.Errorf("rows[%d] = %q, want %q (full order: %+v)", i, r.ID, want[i], rows)
		}
	}
}

func TestListPackets_LimitClampedToDefaultWhenInvalid(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	for _, invalid := range []int{0, -5, 1000} {
		_, err := s.ListPackets(ctx, invalid)
		if err != nil {
			t.Errorf("ListPackets(limit=%d) errored: %v", invalid, err)
		}
	}
}

func TestListPackets_RespectsLimit(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	base := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		id := "id" + string(rune('0'+i))
		_ = s.PutPacket(ctx, id, base.Add(time.Duration(i)*time.Minute), "g", []byte("b"))
	}
	rows, err := s.ListPackets(ctx, 3)
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

	_ = s.PutPacket(ctx, "alive", now, "a", []byte("a"))
	_ = s.PutPacket(ctx, "gone", now, "g", []byte("g"))
	_ = s.DeletePacket(ctx, "gone")

	rows, _ := s.ListPackets(ctx, 100)
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1 (only alive)", len(rows))
	}
	if rows[0].ID != "alive" {
		t.Errorf("rows[0].ID = %q, want alive", rows[0].ID)
	}
}

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
	// ON CONFLICT DO NOTHING: re-inserting the same hash shouldn't error.
	s := openTestStore(t)
	ctx := context.Background()
	if err := s.PutTokenHash(ctx, "h", "one"); err != nil {
		t.Fatal(err)
	}
	if err := s.PutTokenHash(ctx, "h", "two"); err != nil {
		t.Errorf("duplicate PutTokenHash should be noop, got: %v", err)
	}
}

func TestOpen_WALModeEnabled(t *testing.T) {
	// Smoke-check that the pragma applied by Open() took effect. WAL mode is
	// the reason concurrent readers don't block writers; a misconfiguration
	// here would regress multi-client push/pull behavior.
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
	// A path whose parent dir doesn't exist should bubble up an open error
	// (sqlite won't mkdir). Guards against silently falling back to memory.
	_, err := Open(filepath.Join(t.TempDir(), "nope", "does-not-exist", "ltm.db"))
	if err == nil {
		t.Fatal("expected Open to error on bogus path, got nil")
	}
}
