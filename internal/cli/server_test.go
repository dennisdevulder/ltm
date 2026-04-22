package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dennisdevulder/ltm/internal/auth"
	"github.com/dennisdevulder/ltm/internal/store"
)

// ---- defaultDBPath ----

func TestDefaultDBPath_EnvOverride(t *testing.T) {
	t.Setenv("LTM_DB", "/tmp/my-custom.db")
	if got := defaultDBPath(); got != "/tmp/my-custom.db" {
		t.Errorf("defaultDBPath = %q, want %q (LTM_DB override)", got, "/tmp/my-custom.db")
	}
}

func TestDefaultDBPath_HomeFallback(t *testing.T) {
	t.Setenv("LTM_DB", "")
	got := defaultDBPath()
	if !strings.HasSuffix(got, filepath.Join(".local", "share", "ltm", "ltm.db")) {
		t.Errorf("defaultDBPath = %q, want suffix .local/share/ltm/ltm.db", got)
	}
}

// ---- server init ----

// extractToken digs the token out of captured output. 'server init' prints
// documentation to stderr and the bare token to stdout.
func extractToken(stdout string) string {
	// The token is the only 48-char alnum line.
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if len(line) == 48 && isAlnum(line) {
			return line
		}
	}
	return ""
}

func isAlnum(s string) bool {
	for _, r := range s {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')) {
			return false
		}
	}
	return true
}

func TestServer_Init_CreatesDBAndToken(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "srv.db")

	stdout, stderr, err := run(t, nil, "server", "init", "--db", dbPath)
	if err != nil {
		t.Fatalf("server init: %v", err)
	}

	if _, err := os.Stat(dbPath); err != nil {
		t.Errorf("db file not created at %s: %v", dbPath, err)
	}

	tok := extractToken(stdout)
	if tok == "" {
		t.Fatalf("no token found in stdout:\n%s", stdout)
	}

	// Documentation lines should go to stderr so the token on stdout is
	// safe to pipe into a config file without mixing in messages.
	for _, want := range []string{"ltm server initialized", "root token"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr missing %q:\n%s", want, stderr)
		}
	}

	// Token hash should exist in the store.
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer st.Close()
	ok, err := st.TokenExists(context.Background(), auth.HashToken(tok))
	if err != nil {
		t.Fatalf("TokenExists: %v", err)
	}
	if !ok {
		t.Error("issued token not recognised by store")
	}
}

func TestServer_IssueToken_LabelRequired(t *testing.T) {
	// cobra.ExactArgs(1) on issue-token rejects calls without a label.
	_, _, err := run(t, nil, "server", "issue-token")
	if err == nil {
		t.Error("expected error when issue-token called without label")
	}
}

func TestServer_IssueToken_AddsNewTokenToStore(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "srv.db")

	// Need to init first so the DB schema exists.
	if _, _, err := run(t, nil, "server", "init", "--db", dbPath); err != nil {
		t.Fatalf("init: %v", err)
	}

	stdout, stderr, err := run(t, nil, "server", "issue-token", "--db", dbPath, "alice")
	if err != nil {
		t.Fatalf("issue-token: %v", err)
	}

	if !strings.Contains(stderr, `for "alice"`) {
		t.Errorf("expected label in stderr message, got: %q", stderr)
	}

	tok := extractToken(stdout)
	if tok == "" {
		t.Fatalf("no token found in stdout:\n%s", stdout)
	}

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ok, err := st.TokenExists(context.Background(), auth.HashToken(tok))
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("issued token not recognised by store")
	}
}

func TestServer_IssueToken_WithoutInitFails(t *testing.T) {
	// Running issue-token against a nonexistent DB should fail cleanly —
	// without panicking — because the db directory isn't guaranteed to exist.
	badPath := filepath.Join(t.TempDir(), "nested", "dir", "srv.db")
	_, _, err := run(t, nil, "server", "issue-token", "--db", badPath, "ghost")
	if err == nil {
		t.Error("expected error when DB parent dir does not exist")
	}
}
