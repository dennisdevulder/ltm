package auth

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The sha256 of the literal string "test-token-123", hex-encoded.
// Computed with: printf "test-token-123" | shasum -a 256
const expectedTestTokenHash = "19b6b086eebb807f54e6327309dec0ff347a6c3c30bf3bb396f167513eba3475"

func TestHashToken_Deterministic(t *testing.T) {
	h1 := HashToken("secret")
	h2 := HashToken("secret")
	if h1 != h2 {
		t.Errorf("HashToken not deterministic: %q vs %q", h1, h2)
	}
	if len(h1) != 64 {
		t.Errorf("HashToken returned %d hex chars, want 64", len(h1))
	}
}

func TestHashToken_DifferentInputsDifferentHashes(t *testing.T) {
	if HashToken("a") == HashToken("b") {
		t.Error("HashToken collided on distinct inputs")
	}
	// One-char difference should produce completely different hash.
	if HashToken("secret") == HashToken("secrez") {
		t.Error("HashToken collided on near-identical inputs")
	}
}

func TestHashToken_EmptyString(t *testing.T) {
	// sha256 of empty string is well-known:
	want := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if got := HashToken(""); got != want {
		t.Errorf("HashToken(\"\") = %q, want %q", got, want)
	}
}

func withIsolatedAuthDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("LTM_CONFIG_DIR", dir)
	t.Setenv("XDG_CONFIG_HOME", "")
	return dir
}

func TestCredentialsPath_PointsInsideConfigDir(t *testing.T) {
	dir := withIsolatedAuthDir(t)
	got, err := CredentialsPath()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "credentials")
	if got != want {
		t.Errorf("CredentialsPath() = %q, want %q", got, want)
	}
}

func TestSaveLoadToken_Roundtrip(t *testing.T) {
	dir := withIsolatedAuthDir(t)
	const token = "tok_abc_123_very_secret"

	if err := SaveToken(token); err != nil {
		t.Fatalf("SaveToken: %v", err)
	}
	got, err := LoadToken()
	if err != nil {
		t.Fatalf("LoadToken: %v", err)
	}
	if got != token {
		t.Errorf("LoadToken = %q, want %q", got, token)
	}

	// File permissions must be 0600 so other users on the box can't read the token.
	info, err := os.Stat(filepath.Join(dir, "credentials"))
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("credentials file mode = %o, want 600", mode)
	}
}

func TestLoadToken_TrimsTrailingWhitespace(t *testing.T) {
	dir := withIsolatedAuthDir(t)
	// Simulate a credentials file with tricky trailing content that commonly
	// appears when users paste through clipboards or editors.
	contents := "my-token-xyz  \r\n\n  "
	if err := os.WriteFile(filepath.Join(dir, "credentials"), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadToken()
	if err != nil {
		t.Fatal(err)
	}
	if got != "my-token-xyz" {
		t.Errorf("LoadToken = %q, want %q", got, "my-token-xyz")
	}
}

func TestLoadToken_TrimsLeadingWhitespace(t *testing.T) {
	dir := withIsolatedAuthDir(t)
	// Real tokens never contain whitespace, so trimming both sides prevents
	// silent auth failures when a user pastes a token with a stray leading space.
	if err := os.WriteFile(filepath.Join(dir, "credentials"), []byte("  my-token-xyz\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadToken()
	if err != nil {
		t.Fatal(err)
	}
	if got != "my-token-xyz" {
		t.Errorf("LoadToken = %q, want %q", got, "my-token-xyz")
	}
}

func TestLoadToken_TrimsMixedWhitespace(t *testing.T) {
	dir := withIsolatedAuthDir(t)
	// Tabs, CR, LF, and spaces on both sides should all be stripped.
	if err := os.WriteFile(filepath.Join(dir, "credentials"), []byte("\t\r\n my-token-xyz \t\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadToken()
	if err != nil {
		t.Fatal(err)
	}
	if got != "my-token-xyz" {
		t.Errorf("LoadToken = %q, want %q", got, "my-token-xyz")
	}
}

func TestLoadToken_EmptyFileErrors(t *testing.T) {
	dir := withIsolatedAuthDir(t)
	if err := os.WriteFile(filepath.Join(dir, "credentials"), []byte("\n\n  \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadToken()
	if err == nil {
		t.Fatal("expected error on whitespace-only credentials file, got nil")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("expected error message to mention 'empty', got: %v", err)
	}
}

func TestLoadToken_MissingFile(t *testing.T) {
	withIsolatedAuthDir(t)
	_, err := LoadToken()
	if err == nil {
		t.Fatal("expected error when credentials file is absent, got nil")
	}
	// os.ReadFile surfaces the underlying fs error; caller differentiates via os.IsNotExist.
	if !os.IsNotExist(err) {
		t.Errorf("expected os.IsNotExist to recognize the error, got: %v", err)
	}
}

func TestSaveToken_CreatesMissingDir(t *testing.T) {
	// Point at a temp dir that doesn't exist yet; SaveToken should create it.
	parent := t.TempDir()
	missing := filepath.Join(parent, "does", "not", "exist")
	t.Setenv("LTM_CONFIG_DIR", missing)
	t.Setenv("XDG_CONFIG_HOME", "")

	if err := SaveToken("tk"); err != nil {
		t.Fatalf("SaveToken should create nested dirs, got: %v", err)
	}
	if _, err := os.Stat(filepath.Join(missing, "credentials")); err != nil {
		t.Errorf("credentials file not written to expected path: %v", err)
	}
}

func TestCredentialsPath_LTMConfigDirWins(t *testing.T) {
	// LTM_CONFIG_DIR takes precedence over XDG_CONFIG_HOME. Flipping these
	// would silently relocate credentials for anyone using both.
	t.Setenv("LTM_CONFIG_DIR", "/tmp/ltm-explicit")
	t.Setenv("XDG_CONFIG_HOME", "/tmp/xdg-should-be-ignored")
	got, err := CredentialsPath()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join("/tmp/ltm-explicit", "credentials")
	if got != want {
		t.Errorf("CredentialsPath = %q, want %q", got, want)
	}
}

func TestCredentialsPath_XDGConfigHomeFallback(t *testing.T) {
	// With LTM_CONFIG_DIR unset, XDG_CONFIG_HOME + "/ltm" is the next hop.
	t.Setenv("LTM_CONFIG_DIR", "")
	t.Setenv("XDG_CONFIG_HOME", "/tmp/xdg-home")
	got, err := CredentialsPath()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join("/tmp/xdg-home", "ltm", "credentials")
	if got != want {
		t.Errorf("CredentialsPath = %q, want %q", got, want)
	}
}

func TestCredentialsPath_HomeFallback(t *testing.T) {
	// Neither env var set → ~/.config/ltm/credentials.
	t.Setenv("LTM_CONFIG_DIR", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	got, err := CredentialsPath()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(got, filepath.Join(".config", "ltm", "credentials")) {
		t.Errorf("CredentialsPath = %q, want suffix .config/ltm/credentials", got)
	}
}

func TestHashToken_MatchesKnownValue(t *testing.T) {
	// Lock in a known hash so accidental changes to HashToken (e.g. switching
	// algorithms) are caught immediately.
	if got := HashToken("test-token-123"); got != expectedTestTokenHash {
		t.Errorf("HashToken(\"test-token-123\") = %q, want %q", got, expectedTestTokenHash)
	}
}
