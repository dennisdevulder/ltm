package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// ---- test harness ----

// fakeAPI is a tiny in-memory mock of the ltm server's /v1/packets endpoints.
// It lets integration tests exercise every CLI command end-to-end without a
// real server, without touching a real DB, and without real auth.
type fakeAPI struct {
	mu       sync.Mutex
	packets  map[string]json.RawMessage
	requests []recordedReq
	// test knobs
	statusOverride int
	bodyOverride   string
}

type recordedReq struct {
	method string
	path   string
	auth   string
	body   []byte
}

func newFakeAPI() *fakeAPI {
	return &fakeAPI{packets: map[string]json.RawMessage{}}
}

func (f *fakeAPI) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.requests = append(f.requests, recordedReq{
			method: r.Method,
			path:   r.URL.Path + "?" + r.URL.RawQuery,
			auth:   r.Header.Get("Authorization"),
			body:   append([]byte(nil), body...),
		})
		override := f.statusOverride
		overrideBody := f.bodyOverride
		f.mu.Unlock()
		if override != 0 {
			w.WriteHeader(override)
			w.Write([]byte(overrideBody))
			return
		}

		switch {
		case r.Method == "POST" && r.URL.Path == "/v1/packets":
			var parsed struct {
				ID string `json:"id"`
			}
			_ = json.Unmarshal(body, &parsed)
			if parsed.ID == "" {
				w.WriteHeader(400)
				w.Write([]byte(`{"error":"missing id"}`))
				return
			}
			f.mu.Lock()
			f.packets[parsed.ID] = body
			f.mu.Unlock()
			w.WriteHeader(201)
			w.Write([]byte(`{"id":"` + parsed.ID + `"}`))
		case r.Method == "GET" && r.URL.Path == "/v1/packets":
			f.mu.Lock()
			type row struct {
				ID        string `json:"id"`
				CreatedAt string `json:"created_at"`
				Goal      string `json:"goal"`
			}
			out := struct {
				Packets []row `json:"packets"`
			}{}
			for id, p := range f.packets {
				var tmp struct {
					CreatedAt string `json:"created_at"`
					Goal      string `json:"goal"`
				}
				_ = json.Unmarshal(p, &tmp)
				out.Packets = append(out.Packets, row{ID: id, CreatedAt: tmp.CreatedAt, Goal: tmp.Goal})
			}
			f.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(out)
		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/v1/packets/"):
			id := strings.TrimPrefix(r.URL.Path, "/v1/packets/")
			f.mu.Lock()
			p, ok := f.packets[id]
			f.mu.Unlock()
			if !ok {
				w.WriteHeader(404)
				w.Write([]byte(`{"error":"not found"}`))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write(p)
		case r.Method == "DELETE" && strings.HasPrefix(r.URL.Path, "/v1/packets/"):
			id := strings.TrimPrefix(r.URL.Path, "/v1/packets/")
			f.mu.Lock()
			_, ok := f.packets[id]
			delete(f.packets, id)
			f.mu.Unlock()
			if !ok {
				w.WriteHeader(404)
				w.Write([]byte(`{"error":"not found"}`))
				return
			}
			w.WriteHeader(204)
		default:
			w.WriteHeader(404)
		}
	})
}

// setupCLI spins up a fake API, isolates config/creds to a tempdir, and
// returns the fake + a cleanup callback.
func setupCLI(t *testing.T) (*fakeAPI, *httptest.Server) {
	t.Helper()
	api := newFakeAPI()
	srv := httptest.NewServer(api.handler())
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	t.Setenv("LTM_CONFIG_DIR", dir)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("LTM_HOST", srv.URL)
	if err := os.WriteFile(filepath.Join(dir, "credentials"), []byte("test-token\n"), 0o600); err != nil {
		t.Fatalf("seed credentials: %v", err)
	}
	return api, srv
}

// run executes the given subcommand with its args against the root command
// and returns captured stdout, stderr, and any error. stdin is taken from
// the provided reader (use nil for no stdin). All three are wired through
// cobra's SetIn/SetOut/SetErr — but push reads directly from os.Stdin when
// passed '-', so run also temporarily redirects os.Stdin/os.Stdout/os.Stderr.
func run(t *testing.T, stdin io.Reader, args ...string) (string, string, error) {
	t.Helper()
	root := NewRootCmd()
	// Cobra routes fmt.Fprintln(os.Stdout/Stderr) outside its own writers —
	// the CLI code uses fmt.Println a lot — so we swap the real FDs for pipes.
	origOut, origErr, origIn := os.Stdout, os.Stderr, os.Stdin
	defer func() {
		os.Stdout = origOut
		os.Stderr = origErr
		os.Stdin = origIn
	}()

	outR, outW, _ := os.Pipe()
	errR, errW, _ := os.Pipe()
	os.Stdout = outW
	os.Stderr = errW

	var inR *os.File
	if stdin != nil {
		inR2, inW, _ := os.Pipe()
		go func() {
			defer inW.Close()
			io.Copy(inW, stdin)
		}()
		inR = inR2
		os.Stdin = inR
	}

	root.SetArgs(args)
	root.SetOut(outW)
	root.SetErr(errW)

	// Drain pipes concurrently so large writes don't deadlock.
	outBuf, errBuf := &bytes.Buffer{}, &bytes.Buffer{}
	doneOut, doneErr := make(chan struct{}), make(chan struct{})
	go func() { io.Copy(outBuf, outR); close(doneOut) }()
	go func() { io.Copy(errBuf, errR); close(doneErr) }()

	err := root.Execute()

	outW.Close()
	errW.Close()
	<-doneOut
	<-doneErr
	if inR != nil {
		inR.Close()
	}
	return outBuf.String(), errBuf.String(), err
}

// sample packet generators
const sampleID = "01JABCDEF0123456789ABCDEFG"

func samplePacket(id string) []byte {
	return []byte(`{"ltm_version":"0.2","id":"` + id +
		`","created_at":"2026-04-21T12:00:00Z","goal":"ship it","next_step":"merge"}`)
}

func samplePacketWithSecret() []byte {
	return []byte(`{"ltm_version":"0.2","id":"` + sampleID +
		`","created_at":"2026-04-21T12:00:00Z","goal":"ship /Users/alice/secret","next_step":"merge"}`)
}

// ---- push ----

func TestPush_FromFile(t *testing.T) {
	api, _ := setupCLI(t)
	f := filepath.Join(t.TempDir(), "packet.json")
	if err := os.WriteFile(f, samplePacket(sampleID), 0o600); err != nil {
		t.Fatal(err)
	}

	out, _, err := run(t, nil, "push", f)
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	if !strings.Contains(out, sampleID) {
		t.Errorf("expected push to echo packet id, got: %q", out)
	}
	// Fake API should have stored it.
	if _, ok := api.packets[sampleID]; !ok {
		t.Errorf("packet not stored on fake api")
	}
	// And the request should have carried the bearer token.
	if len(api.requests) != 1 {
		t.Fatalf("want 1 request, got %d", len(api.requests))
	}
	if api.requests[0].auth != "Bearer test-token" {
		t.Errorf("auth = %q, want Bearer test-token", api.requests[0].auth)
	}
}

func TestPush_FromStdin(t *testing.T) {
	setupCLI(t)
	out, _, err := run(t, bytes.NewReader(samplePacket(sampleID)), "push", "-")
	if err != nil {
		t.Fatalf("push -: %v", err)
	}
	if !strings.Contains(out, sampleID) {
		t.Errorf("expected id on stdout, got: %q", out)
	}
}

func TestPush_RejectsInvalidPacket(t *testing.T) {
	setupCLI(t)
	f := filepath.Join(t.TempDir(), "bad.json")
	// Missing goal → schema violation.
	bad := []byte(`{"ltm_version":"0.2","id":"` + sampleID +
		`","created_at":"2026-04-21T12:00:00Z","next_step":"n"}`)
	if err := os.WriteFile(f, bad, 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err := run(t, nil, "push", f)
	if err == nil || !strings.Contains(err.Error(), "rejected") {
		t.Errorf("expected 'rejected' error, got: %v", err)
	}
}

func TestPush_BlocksUnredactedSecret(t *testing.T) {
	setupCLI(t)
	f := filepath.Join(t.TempDir(), "secret.json")
	if err := os.WriteFile(f, samplePacketWithSecret(), 0o600); err != nil {
		t.Fatal(err)
	}
	_, stderr, err := run(t, nil, "push", f)
	if err == nil {
		t.Fatal("expected redaction failure")
	}
	if !strings.Contains(stderr, "redactable") {
		t.Errorf("expected redaction warning on stderr, got: %q", stderr)
	}
	if !strings.Contains(err.Error(), "redaction failed") {
		t.Errorf("expected 'redaction failed' error, got: %v", err)
	}
}

func TestPush_AllowUnredactedBypasses(t *testing.T) {
	api, _ := setupCLI(t)
	f := filepath.Join(t.TempDir(), "secret.json")
	if err := os.WriteFile(f, samplePacketWithSecret(), 0o600); err != nil {
		t.Fatal(err)
	}
	out, _, err := run(t, nil, "push", "--allow-unredacted", f)
	if err != nil {
		t.Fatalf("push --allow-unredacted: %v", err)
	}
	if !strings.Contains(out, sampleID) {
		t.Errorf("expected id on stdout, got: %q", out)
	}
	if _, ok := api.packets[sampleID]; !ok {
		t.Error("packet should have been stored despite secret")
	}
}

func TestPush_ServerErrorSurfaces(t *testing.T) {
	api, _ := setupCLI(t)
	api.statusOverride = 500
	api.bodyOverride = `{"error":"db down"}`

	f := filepath.Join(t.TempDir(), "p.json")
	os.WriteFile(f, samplePacket(sampleID), 0o600)

	_, _, err := run(t, nil, "push", f)
	if err == nil {
		t.Fatal("expected error when server returns 500")
	}
	if !strings.Contains(err.Error(), "db down") {
		t.Errorf("expected 'db down' in error, got: %v", err)
	}
}

// ---- pull ----

func TestPull_WritesBodyToStdout(t *testing.T) {
	api, _ := setupCLI(t)
	api.packets[sampleID] = samplePacket(sampleID)

	out, _, err := run(t, nil, "pull", sampleID)
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	if !strings.Contains(out, `"ltm_version":"0.2"`) {
		t.Errorf("expected packet JSON on stdout, got: %q", out)
	}
}

func TestPull_NotFound(t *testing.T) {
	setupCLI(t)
	_, _, err := run(t, nil, "pull", "01JBADBADBAD000000000000000")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 404 error, got: %v", err)
	}
}

// ---- ls ----

func TestLs_Human(t *testing.T) {
	api, _ := setupCLI(t)
	api.packets[sampleID] = samplePacket(sampleID)

	out, _, err := run(t, nil, "ls")
	if err != nil {
		t.Fatalf("ls: %v", err)
	}
	// Human output has a header row.
	if !strings.Contains(out, "ID") || !strings.Contains(out, "GOAL") {
		t.Errorf("expected ls header in output, got: %q", out)
	}
	if !strings.Contains(out, sampleID) {
		t.Errorf("expected packet id listed, got: %q", out)
	}
}

func TestLs_JSON(t *testing.T) {
	api, _ := setupCLI(t)
	api.packets[sampleID] = samplePacket(sampleID)

	out, _, err := run(t, nil, "ls", "--json")
	if err != nil {
		t.Fatalf("ls --json: %v", err)
	}
	var parsed struct {
		Packets []any `json:"packets"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("ls --json output not valid JSON: %v\n%s", err, out)
	}
	if len(parsed.Packets) != 1 {
		t.Errorf("expected 1 packet, got %d", len(parsed.Packets))
	}
}

func TestLs_LimitReachesServer(t *testing.T) {
	api, _ := setupCLI(t)
	if _, _, err := run(t, nil, "ls", "--limit", "7"); err != nil {
		t.Fatalf("ls: %v", err)
	}
	if len(api.requests) != 1 {
		t.Fatalf("want 1 request, got %d", len(api.requests))
	}
	if !strings.Contains(api.requests[0].path, "limit=7") {
		t.Errorf("expected limit=7 in query, got %q", api.requests[0].path)
	}
}

// ---- show ----

func TestShow_Human(t *testing.T) {
	api, _ := setupCLI(t)
	// A richer packet so every section of the pretty-printer fires.
	api.packets[sampleID] = []byte(`{
		"ltm_version":"0.2","id":"` + sampleID + `",
		"created_at":"2026-04-21T12:00:00Z","goal":"ship it",
		"next_step":"merge","constraints":["no blocking I/O"],
		"decisions":[{"what":"use DAG","why":"simple","locked":true}],
		"attempts":[{"tried":"x","outcome":"succeeded","learned":"done"}],
		"open_questions":["next version?"],
		"project":{"name":"ltm","ref":"main"},
		"tags":["v0.2","docs"]
	}`)
	out, _, err := run(t, nil, "show", sampleID)
	if err != nil {
		t.Fatalf("show: %v", err)
	}
	wantSubstrings := []string{
		"ID", sampleID,
		"Spec       v0.2",
		"Project    ltm",
		"Tags       v0.2, docs",
		"Goal",
		"ship it",
		"Constraints",
		"no blocking I/O",
		"Decisions",
		"use DAG",
		"[locked]",
		"Attempts",
		"learned: done",
		"Open questions",
		"next version?",
		"Next step",
		"merge",
	}
	for _, want := range wantSubstrings {
		if !strings.Contains(out, want) {
			t.Errorf("show output missing %q, full output:\n%s", want, out)
		}
	}
}

func TestShow_JSON(t *testing.T) {
	api, _ := setupCLI(t)
	api.packets[sampleID] = samplePacket(sampleID)

	out, _, err := run(t, nil, "show", "--json", sampleID)
	if err != nil {
		t.Fatalf("show --json: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("show --json not valid JSON: %v\n%s", err, out)
	}
	if got["id"] != sampleID {
		t.Errorf("id = %v, want %s", got["id"], sampleID)
	}
}

// ---- rm ----

func TestRm_DeletesFromServer(t *testing.T) {
	api, _ := setupCLI(t)
	api.packets[sampleID] = samplePacket(sampleID)

	out, _, err := run(t, nil, "rm", sampleID)
	if err != nil {
		t.Fatalf("rm: %v", err)
	}
	if !strings.Contains(out, "deleted") {
		t.Errorf("expected 'deleted' confirmation, got: %q", out)
	}
	if _, ok := api.packets[sampleID]; ok {
		t.Error("packet still on server after rm")
	}
}

func TestRm_NotFound(t *testing.T) {
	setupCLI(t)
	_, _, err := run(t, nil, "rm", "01JBADBADBAD000000000000000")
	if err == nil {
		t.Error("expected error for unknown id")
	}
}

// ---- resume (with --no-copy so we don't touch clipboard) ----

func TestResume_WithID_WritesBlockToStdout(t *testing.T) {
	api, _ := setupCLI(t)
	api.packets[sampleID] = samplePacket(sampleID)

	out, _, err := run(t, nil, "resume", "--no-copy", sampleID)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	wantSubstrings := []string{
		"# Resume context",
		"## Goal",
		"ship it",
		"## Your first action",
		"merge",
		"## Reminder",
		"(spec v0.2)",
	}
	for _, want := range wantSubstrings {
		if !strings.Contains(out, want) {
			t.Errorf("resume missing %q, got:\n%s", want, out)
		}
	}
}

func TestResume_PacketNotFound(t *testing.T) {
	setupCLI(t)
	_, _, err := run(t, nil, "resume", "--no-copy", "01JBADBADBAD000000000000000")
	if err == nil {
		t.Error("expected error for unknown id")
	}
}

// ---- config ----

func TestConfig_SetGetUnset(t *testing.T) {
	withIsolatedCLIState(t)

	if _, _, err := run(t, nil, "config", "set", "host", "http://example.com"); err != nil {
		t.Fatalf("config set: %v", err)
	}
	out, _, err := run(t, nil, "config", "get", "host")
	if err != nil {
		t.Fatalf("config get: %v", err)
	}
	if strings.TrimSpace(out) != "http://example.com" {
		t.Errorf("config get host = %q, want http://example.com", strings.TrimSpace(out))
	}

	if _, _, err := run(t, nil, "config", "unset", "host"); err != nil {
		t.Fatalf("config unset: %v", err)
	}
	out, _, _ = run(t, nil, "config", "get", "host")
	if strings.TrimSpace(out) != "" {
		t.Errorf("config get host after unset = %q, want empty", strings.TrimSpace(out))
	}
}

func TestConfig_List(t *testing.T) {
	withIsolatedCLIState(t)
	if _, _, err := run(t, nil, "config", "set", "host", "http://example.com"); err != nil {
		t.Fatal(err)
	}
	out, _, err := run(t, nil, "config", "list")
	if err != nil {
		t.Fatalf("config list: %v", err)
	}
	if !strings.Contains(out, "host = http://example.com") {
		t.Errorf("config list missing host line, got: %q", out)
	}
	// Stable ordering — 'editor' should come before 'host'.
	if strings.Index(out, "editor") > strings.Index(out, "host") {
		t.Errorf("config list keys out of order: %q", out)
	}
}

func TestConfig_Path(t *testing.T) {
	dir := withIsolatedCLIState(t)
	out, _, err := run(t, nil, "config", "path")
	if err != nil {
		t.Fatalf("config path: %v", err)
	}
	want := filepath.Join(dir, "config.toml")
	if strings.TrimSpace(out) != want {
		t.Errorf("config path = %q, want %q", strings.TrimSpace(out), want)
	}
}

func TestConfig_UnknownKey(t *testing.T) {
	withIsolatedCLIState(t)
	_, _, err := run(t, nil, "config", "set", "nonexistent", "v")
	if err == nil || !strings.Contains(err.Error(), "unknown config key") {
		t.Errorf("expected unknown key error, got: %v", err)
	}
}

// ---- auth ----

func TestAuth_HappyPath(t *testing.T) {
	dir := withIsolatedCLIState(t)

	var healthHits, authHits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/healthz":
			healthHits++
			w.WriteHeader(200)
		case r.URL.Path == "/v1/packets":
			authHits++
			if r.Header.Get("Authorization") != "Bearer good-token" {
				w.WriteHeader(401)
				w.Write([]byte("bad token"))
				return
			}
			w.WriteHeader(200)
			w.Write([]byte(`{"packets":[]}`))
		}
	}))
	defer srv.Close()

	out, _, err := run(t, nil, "auth", srv.URL, "good-token")
	if err != nil {
		t.Fatalf("auth: %v", err)
	}
	if !strings.Contains(out, "authenticated") {
		t.Errorf("expected success message, got: %q", out)
	}
	if healthHits == 0 || authHits == 0 {
		t.Errorf("expected both health + auth probes, got health=%d auth=%d", healthHits, authHits)
	}
	// credentials file should exist with 0600.
	info, err := os.Stat(filepath.Join(dir, "credentials"))
	if err != nil {
		t.Fatalf("credentials not saved: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("credentials mode = %o, want 600", mode)
	}
	// config should have host set.
	out, _, _ = run(t, nil, "config", "get", "host")
	if strings.TrimSpace(out) != srv.URL {
		t.Errorf("config host = %q, want %q", strings.TrimSpace(out), srv.URL)
	}
}

func TestAuth_ServerUnreachable(t *testing.T) {
	withIsolatedCLIState(t)
	_, _, err := run(t, nil, "auth", "http://127.0.0.1:1", "anything")
	if err == nil || !strings.Contains(err.Error(), "server unreachable") {
		t.Errorf("expected unreachable error, got: %v", err)
	}
}

func TestAuth_Unhealthy(t *testing.T) {
	withIsolatedCLIState(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
	}))
	defer srv.Close()
	_, _, err := run(t, nil, "auth", srv.URL, "tk")
	if err == nil || !strings.Contains(err.Error(), "health response") {
		t.Errorf("expected health error, got: %v", err)
	}
}

func TestAuth_TokenRejected(t *testing.T) {
	withIsolatedCLIState(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/healthz" {
			w.WriteHeader(200)
			return
		}
		w.WriteHeader(401)
		w.Write([]byte("bad token"))
	}))
	defer srv.Close()
	_, _, err := run(t, nil, "auth", srv.URL, "bad")
	if err == nil || !strings.Contains(err.Error(), "token rejected") {
		t.Errorf("expected token-rejected error, got: %v", err)
	}
}

func TestConfig_Edit_UsesEDITOR(t *testing.T) {
	// config edit shells out to $EDITOR (or `cfg.Editor`, or vi). We point
	// it at 'true' — the POSIX no-op — so the subprocess succeeds without
	// actually opening anything. This covers the RunE path on 'edit'.
	withIsolatedCLIState(t)
	t.Setenv("EDITOR", "true")
	if _, _, err := run(t, nil, "config", "edit"); err != nil {
		t.Errorf("config edit: %v", err)
	}
}

func TestConfig_Edit_EditorFails(t *testing.T) {
	// When the editor exits non-zero, the command must surface the error.
	withIsolatedCLIState(t)
	t.Setenv("EDITOR", "false")
	_, _, err := run(t, nil, "config", "edit")
	if err == nil {
		t.Error("expected error when editor exits non-zero")
	}
}

func TestConfig_BoolParsing(t *testing.T) {
	// 'lenient' is the only bool key. Accept true/false/yes/no/0/1 and
	// reject unknown values.
	withIsolatedCLIState(t)
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"true", "true", true},
		{"yes", "true", true},
		{"on", "true", true},
		{"1", "true", true},
		{"false", "false", true},
		{"no", "false", true},
		{"off", "false", true},
		{"0", "false", true},
		{"banana", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			_, _, err := run(t, nil, "config", "set", "lenient", tc.in)
			if tc.ok && err != nil {
				t.Errorf("unexpected error for %q: %v", tc.in, err)
			}
			if !tc.ok && err == nil {
				t.Errorf("expected error for %q", tc.in)
			}
			if tc.ok {
				out, _, _ := run(t, nil, "config", "get", "lenient")
				if strings.TrimSpace(out) != tc.want {
					t.Errorf("get lenient after set %q = %q, want %q",
						tc.in, strings.TrimSpace(out), tc.want)
				}
			}
		})
	}
}

func TestAuth_Whoami(t *testing.T) {
	dir := withIsolatedCLIState(t)
	os.WriteFile(filepath.Join(dir, "credentials"), []byte("abc-token\n"), 0o600)
	os.WriteFile(filepath.Join(dir, "config.toml"), []byte(`host = "http://example"`+"\n"), 0o600)

	out, _, err := run(t, nil, "auth", "whoami")
	if err != nil {
		t.Fatalf("whoami: %v", err)
	}
	var parsed struct {
		Host       string `json:"host"`
		TokenHash8 string `json:"token_hash8"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("whoami output not JSON: %v\n%s", err, out)
	}
	if parsed.Host != "http://example" {
		t.Errorf("host = %q, want http://example", parsed.Host)
	}
	if len(parsed.TokenHash8) != 8 {
		t.Errorf("token_hash8 = %q, want 8 chars", parsed.TokenHash8)
	}
}
