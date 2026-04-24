package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// teamAPI is a tiny mock of the subset of routes the teams / invite / join
// commands touch. It records every request so tests can assert both the
// method+path+body AND the stdout/stderr of the CLI in one pass.
type teamAPI struct {
	mu       sync.Mutex
	requests []recordedReq
	teams    map[string]bool // set of team names that exist
	// canned response bodies / statuses keyed by "METHOD PATH" prefix.
	responses map[string]cannedResp
}

type cannedResp struct {
	status int
	body   string
}

func newTeamAPI() *teamAPI {
	return &teamAPI{
		teams:     map[string]bool{},
		responses: map[string]cannedResp{},
	}
}

func (a *teamAPI) set(key string, status int, body string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.responses[key] = cannedResp{status: status, body: body}
}

func (a *teamAPI) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := readBody(r)
		a.mu.Lock()
		a.requests = append(a.requests, recordedReq{
			method: r.Method,
			path:   r.URL.Path + "?" + r.URL.RawQuery,
			auth:   r.Header.Get("Authorization"),
			body:   append([]byte(nil), body...),
		})
		key := r.Method + " " + r.URL.Path
		canned, ok := a.responses[key]
		a.mu.Unlock()
		if ok {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(canned.status)
			_, _ = w.Write([]byte(canned.body))
			return
		}

		switch {
		case r.Method == "POST" && r.URL.Path == "/v1/teams":
			var in struct {
				Name string `json:"name"`
			}
			_ = json.Unmarshal(body, &in)
			a.mu.Lock()
			a.teams[in.Name] = true
			a.mu.Unlock()
			w.WriteHeader(201)
			_, _ = w.Write([]byte(`{"team":{"id":"t_` + in.Name + `","name":"` + in.Name + `","owner_id":"u_root"}}`))
		case r.Method == "GET" && r.URL.Path == "/v1/teams":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{"teams":[{"id":"t1","name":"alpha","owner_id":"u_root","created_at":"2026-04-21T12:00:00Z"}]}`))
		case r.Method == "DELETE" && strings.HasPrefix(r.URL.Path, "/v1/teams/") &&
			!strings.Contains(r.URL.Path, "/members") &&
			!strings.Contains(r.URL.Path, "/leave") &&
			!strings.Contains(r.URL.Path, "/invites"):
			w.WriteHeader(204)
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/leave"):
			w.WriteHeader(204)
		default:
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"error":"no mock for ` + r.Method + " " + r.URL.Path + `"}`))
		}
	})
}

func readBody(r *http.Request) []byte {
	b := make([]byte, 0, 1024)
	buf := make([]byte, 1024)
	for {
		n, err := r.Body.Read(buf)
		if n > 0 {
			b = append(b, buf[:n]...)
		}
		if err != nil {
			break
		}
	}
	return b
}

func setupTeamCLI(t *testing.T) (*teamAPI, *httptest.Server) {
	t.Helper()
	a := newTeamAPI()
	srv := httptest.NewServer(a.handler())
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	t.Setenv("LTM_CONFIG_DIR", dir)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("LTM_HOST", srv.URL)
	if err := os.WriteFile(filepath.Join(dir, "credentials"), []byte("test-token\n"), 0o600); err != nil {
		t.Fatalf("seed credentials: %v", err)
	}
	return a, srv
}

// ---- teams create / ls / rm ----

func TestTeams_Create_PostsJSONBody(t *testing.T) {
	a, _ := setupTeamCLI(t)
	out, _, err := run(t, nil, "teams", "create", "alpha")
	if err != nil {
		t.Fatalf("teams create: %v", err)
	}
	if !strings.Contains(out, "created team alpha") {
		t.Errorf("stdout = %q, want 'created team alpha'", out)
	}
	if len(a.requests) != 1 {
		t.Fatalf("want 1 request, got %d", len(a.requests))
	}
	req := a.requests[0]
	if req.method != "POST" || !strings.HasPrefix(req.path, "/v1/teams") {
		t.Errorf("wrong request: %+v", req)
	}
	var parsed struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(req.body, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Name != "alpha" {
		t.Errorf("body.name = %q, want alpha", parsed.Name)
	}
}

func TestTeams_Ls_RendersTable(t *testing.T) {
	setupTeamCLI(t)
	out, _, err := run(t, nil, "teams", "ls")
	if err != nil {
		t.Fatalf("teams ls: %v", err)
	}
	if !strings.Contains(out, "NAME") || !strings.Contains(out, "alpha") {
		t.Errorf("expected table with alpha, got: %q", out)
	}
}

func TestTeams_Rm_DeletesOnServer(t *testing.T) {
	a, _ := setupTeamCLI(t)
	out, _, err := run(t, nil, "teams", "rm", "alpha")
	if err != nil {
		t.Fatalf("teams rm: %v", err)
	}
	if !strings.Contains(out, "deleted team alpha") {
		t.Errorf("stdout = %q", out)
	}
	if len(a.requests) != 1 || a.requests[0].method != "DELETE" {
		t.Errorf("wrong request: %+v", a.requests)
	}
}

func TestTeams_Leave_PostsLeaveRoute(t *testing.T) {
	a, _ := setupTeamCLI(t)
	out, _, err := run(t, nil, "teams", "leave", "alpha")
	if err != nil {
		t.Fatalf("teams leave: %v", err)
	}
	if !strings.Contains(out, "left team alpha") {
		t.Errorf("stdout = %q", out)
	}
	if len(a.requests) != 1 ||
		a.requests[0].method != "POST" ||
		!strings.HasPrefix(a.requests[0].path, "/v1/teams/alpha/leave") {
		t.Errorf("wrong request: %+v", a.requests)
	}
}

// ---- invite ----

func TestInvite_PrintsURLToStdout(t *testing.T) {
	a, _ := setupTeamCLI(t)
	a.set("POST /v1/teams/alpha/invites", 201,
		`{"code":"abc123","url":"http://host/v1/invites/abc123","expires_at":"2026-05-01T00:00:00Z"}`)

	out, errOut, err := run(t, nil, "invite", "-t", "alpha")
	if err != nil {
		t.Fatalf("invite: %v", err)
	}
	if !strings.Contains(out, "http://host/v1/invites/abc123") {
		t.Errorf("stdout missing URL: %q", out)
	}
	if !strings.Contains(errOut, "expires:") {
		t.Errorf("stderr missing expiry: %q", errOut)
	}
}

func TestInvite_RequiresTeamFlag(t *testing.T) {
	setupTeamCLI(t)
	_, _, err := run(t, nil, "invite")
	if err == nil {
		t.Error("expected error when -t is missing")
	}
}

// ---- join ----

func TestJoin_FullURL_PersistsMintedToken(t *testing.T) {
	a, srv := setupTeamCLI(t)
	// Unauthenticated join — server mints a fresh token and we save it.
	a.set("POST /v1/invites/abc123/accept", 200,
		`{"token":"new-token","user":{"id":"u1","display":"c"},"team":{"id":"t1","name":"alpha"}}`)
	// Wipe the pre-seeded credentials so we exercise the "no prior auth" branch.
	dir := os.Getenv("LTM_CONFIG_DIR")
	_ = os.Remove(filepath.Join(dir, "credentials"))

	inviteURL := srv.URL + "/v1/invites/abc123"
	out, _, err := run(t, nil, "join", inviteURL)
	if err != nil {
		t.Fatalf("join: %v", err)
	}
	if !strings.Contains(out, "joined team alpha") {
		t.Errorf("stdout = %q", out)
	}
	// The new token should be on disk.
	creds, err := os.ReadFile(filepath.Join(dir, "credentials"))
	if err != nil {
		t.Fatalf("creds: %v", err)
	}
	if strings.TrimSpace(string(creds)) != "new-token" {
		t.Errorf("credentials file = %q, want new-token", creds)
	}
}

func TestJoin_ExistingBearer_NoTokenPersistence(t *testing.T) {
	a, srv := setupTeamCLI(t)
	// Server-side: existing user joins, no token minted.
	a.set("POST /v1/invites/abc123/accept", 200,
		`{"user":{"id":"u1","display":"bob"},"team":{"id":"t1","name":"alpha"}}`)

	inviteURL := srv.URL + "/v1/invites/abc123"
	out, _, err := run(t, nil, "join", inviteURL)
	if err != nil {
		t.Fatalf("join: %v", err)
	}
	if !strings.Contains(out, "joined team alpha") {
		t.Errorf("stdout = %q", out)
	}
	// The original credentials must still be there, not overwritten.
	creds, _ := os.ReadFile(filepath.Join(os.Getenv("LTM_CONFIG_DIR"), "credentials"))
	if strings.TrimSpace(string(creds)) != "test-token" {
		t.Errorf("credentials file = %q, want unchanged test-token", creds)
	}
}

func TestJoin_BareCode_UsesConfiguredHost(t *testing.T) {
	// A bare code (not a full URL) should resolve the host from the
	// existing config rather than failing. The server-mock accepts the
	// code and returns a no-token response so we don't tangle with
	// credentials persistence here.
	a, _ := setupTeamCLI(t)
	a.set("POST /v1/invites/bare123/accept", 200,
		`{"user":{"id":"u1","display":"bob"},"team":{"id":"t1","name":"alpha"}}`)

	if _, _, err := run(t, nil, "join", "bare123"); err != nil {
		t.Fatalf("join bare code: %v", err)
	}
	if len(a.requests) != 1 {
		t.Fatalf("want 1 request, got %d", len(a.requests))
	}
	if a.requests[0].path != "/v1/invites/bare123/accept?" {
		t.Errorf("wrong path: %q", a.requests[0].path)
	}
}

func TestJoin_ExpiredInviteSurfaces410(t *testing.T) {
	a, srv := setupTeamCLI(t)
	a.set("POST /v1/invites/gone/accept", 410, `{"error":"invite expired or already consumed"}`)

	_, _, err := run(t, nil, "join", srv.URL+"/v1/invites/gone")
	if err == nil {
		t.Fatal("expected error on 410 invite")
	}
	if !strings.Contains(err.Error(), "expired") && !strings.Contains(err.Error(), "consumed") {
		t.Errorf("error = %v, expected expired/consumed message", err)
	}
}

// ---- -t flag on push / ls ----

func TestPush_TeamFlag_AddsTeamQuery(t *testing.T) {
	a, _ := setupTeamCLI(t)
	// Override POST /v1/packets so our team=alpha push is accepted without the
	// full validator path of the default mock.
	a.set("POST /v1/packets", 201, `{"id":"`+sampleID+`"}`)

	f := filepath.Join(t.TempDir(), "packet.json")
	if err := os.WriteFile(f, samplePacket(sampleID), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, nil, "push", f, "-t", "alpha"); err != nil {
		t.Fatalf("push -t: %v", err)
	}
	if len(a.requests) != 1 {
		t.Fatalf("want 1 request, got %d", len(a.requests))
	}
	req := a.requests[0]
	if !strings.Contains(req.path, "team=alpha") {
		t.Errorf("expected ?team=alpha in path, got: %q", req.path)
	}
}

func TestLs_TeamFlag_AddsTeamQuery(t *testing.T) {
	a, _ := setupTeamCLI(t)
	a.set("GET /v1/packets", 200, `{"packets":[]}`)

	if _, _, err := run(t, nil, "ls", "-t", "alpha"); err != nil {
		t.Fatalf("ls -t: %v", err)
	}
	if len(a.requests) != 1 {
		t.Fatalf("want 1 request, got %d", len(a.requests))
	}
	if !strings.Contains(a.requests[0].path, "team=alpha") {
		t.Errorf("expected ?team=alpha in path, got: %q", a.requests[0].path)
	}
}
