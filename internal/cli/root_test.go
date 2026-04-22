package cli

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---- errFromResponse ----

func mkResp(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestErrFromResponse_Success(t *testing.T) {
	cases := []int{200, 201, 204, 299}
	for _, code := range cases {
		if err := errFromResponse(mkResp(code, "anything")); err != nil {
			t.Errorf("status %d should be success, got: %v", code, err)
		}
	}
}

func TestErrFromResponse_Non2xx_PlainText(t *testing.T) {
	err := errFromResponse(mkResp(500, "internal goof"))
	if err == nil {
		t.Fatal("expected error for 500, got nil")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("expected status code in message, got: %v", err)
	}
	if !strings.Contains(err.Error(), "internal goof") {
		t.Errorf("expected body echoed in message, got: %v", err)
	}
}

func TestErrFromResponse_JSONErrorField(t *testing.T) {
	// A JSON body with an "error" field surfaces the message verbatim —
	// more useful than echoing the raw JSON.
	err := errFromResponse(mkResp(400, `{"error":"bad shape","code":"E42"}`))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "bad shape") {
		t.Errorf("expected 'error' field extracted, got: %v", err)
	}
	if strings.Contains(err.Error(), "code") {
		t.Errorf("unparsed JSON should not leak into error, got: %v", err)
	}
}

func TestErrFromResponse_JSONWithoutErrorField(t *testing.T) {
	// JSON body but no "error" key — fall back to the trimmed raw body.
	err := errFromResponse(mkResp(422, `{"message":"no error key here"}`))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "message") {
		t.Errorf("expected raw JSON to appear when 'error' is absent, got: %v", err)
	}
}

func TestErrFromResponse_TrimsWhitespace(t *testing.T) {
	err := errFromResponse(mkResp(503, "\n\n  service down  \n"))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if strings.Contains(err.Error(), "\n") {
		t.Errorf("whitespace not trimmed: %v", err)
	}
}

func TestErrFromResponse_Non2xxEmptyBody(t *testing.T) {
	// An empty body must not cause a nil-pointer / panic and should still
	// produce a usable error with just the status code.
	err := errFromResponse(mkResp(418, ""))
	if err == nil {
		t.Fatal("expected error for 418 with empty body, got nil")
	}
	if !strings.Contains(err.Error(), "418") {
		t.Errorf("expected status code in error, got: %v", err)
	}
}

// ---- newClient ----

// withIsolatedCLIState points config + credentials at a per-test tempdir and
// returns the dir so tests can seed files into it. Every test that touches
// newClient must call this or it'll race against the real user's config.
func withIsolatedCLIState(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("LTM_CONFIG_DIR", dir)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("LTM_HOST", "") // don't inherit host from ambient env
	return dir
}

func seedCreds(t *testing.T, dir, token string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "credentials"), []byte(token+"\n"), 0o600); err != nil {
		t.Fatalf("seed credentials: %v", err)
	}
}

func seedConfig(t *testing.T, dir, toml string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(toml), 0o600); err != nil {
		t.Fatalf("seed config: %v", err)
	}
}

func TestNewClient_MissingHost(t *testing.T) {
	dir := withIsolatedCLIState(t)
	// No config file → Host is empty → should error with a hint.
	seedCreds(t, dir, "tk")
	if _, err := newClient(); err == nil || !strings.Contains(err.Error(), "no host configured") {
		t.Errorf("expected 'no host configured' error, got: %v", err)
	}
}

func TestNewClient_MissingToken(t *testing.T) {
	withIsolatedCLIState(t)
	seedConfig(t, os.Getenv("LTM_CONFIG_DIR"), `host = "http://localhost:8080"`+"\n")
	// credentials file absent → LoadToken errors → newClient surfaces it.
	if _, err := newClient(); err == nil || !strings.Contains(err.Error(), "not authenticated") {
		t.Errorf("expected 'not authenticated' error, got: %v", err)
	}
}

func TestNewClient_EnvHostOverride(t *testing.T) {
	dir := withIsolatedCLIState(t)
	seedCreds(t, dir, "tk")
	t.Setenv("LTM_HOST", "http://env-host.example")

	cl, err := newClient()
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	if cl.host != "http://env-host.example" {
		t.Errorf("host = %q, want LTM_HOST override", cl.host)
	}
	if cl.token != "tk" {
		t.Errorf("token = %q, want %q", cl.token, "tk")
	}
}

func TestClient_Do_SetsAuthHeader(t *testing.T) {
	dir := withIsolatedCLIState(t)
	seedCreds(t, dir, "secret-token")

	var gotAuth, gotCT string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotCT = r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(200)
	}))
	defer srv.Close()
	t.Setenv("LTM_HOST", srv.URL)

	cl, err := newClient()
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}

	// POST with body — Content-Type should be set.
	resp, err := cl.do("POST", "/v1/packets", []byte(`{"x":1}`))
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	resp.Body.Close()

	if gotAuth != "Bearer secret-token" {
		t.Errorf("Authorization = %q, want Bearer secret-token", gotAuth)
	}
	if gotCT != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", gotCT)
	}
	if !bytes.Equal(gotBody, []byte(`{"x":1}`)) {
		t.Errorf("body = %q, want %q", gotBody, `{"x":1}`)
	}
}

func TestClient_Do_NoContentTypeOnNilBody(t *testing.T) {
	// do() only sets Content-Type when body is non-nil. GET requests should
	// go out without it to avoid spurious 'application/json' on empty reads.
	dir := withIsolatedCLIState(t)
	seedCreds(t, dir, "tk")

	var gotCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCT = r.Header.Get("Content-Type")
		w.WriteHeader(200)
	}))
	defer srv.Close()
	t.Setenv("LTM_HOST", srv.URL)

	cl, err := newClient()
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	resp, err := cl.do("GET", "/v1/packets", nil)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	resp.Body.Close()
	if gotCT != "" {
		t.Errorf("Content-Type = %q, want empty for nil body", gotCT)
	}
}

func TestClient_Do_BadMethod(t *testing.T) {
	dir := withIsolatedCLIState(t)
	seedCreds(t, dir, "tk")
	t.Setenv("LTM_HOST", "http://x")

	cl, err := newClient()
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	// Space in a method name is invalid per http.NewRequest.
	if _, err := cl.do("BAD METHOD", "/", nil); err == nil {
		t.Error("expected error for invalid method, got nil")
	}
}

// ---- NewRootCmd ----

func TestNewRootCmd_WiresSubcommands(t *testing.T) {
	// The root command must expose every user-facing subcommand. Missing a
	// wire-up would silently hide a command from `--help` and from users.
	root := NewRootCmd()
	want := []string{
		"config", "auth", "push", "pull", "ls", "show", "rm", "resume", "update", "server",
	}
	got := map[string]bool{}
	for _, sub := range root.Commands() {
		got[sub.Name()] = true
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("root command missing %q subcommand", w)
		}
	}
}

func TestNewRootCmd_VersionFlag(t *testing.T) {
	// --version should be wired through cobra; exercising it also makes
	// sure Version is a non-empty string.
	if Version == "" {
		t.Error("Version must not be empty")
	}
	root := NewRootCmd()
	if root.Version != Version {
		t.Errorf("root.Version = %q, want %q", root.Version, Version)
	}
}
