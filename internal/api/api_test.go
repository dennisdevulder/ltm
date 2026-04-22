package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dennisdevulder/ltm/internal/auth"
	"github.com/dennisdevulder/ltm/internal/packet"
	"github.com/dennisdevulder/ltm/internal/store"
	ltmschema "github.com/dennisdevulder/ltm/schema"
)

const (
	testToken   = "test-token-abc123"
	validID     = "01JABCDEF0123456789ABCDEFG"
	validGoal   = "test goal"
	validNext   = "test next step"
	validCreate = "2026-04-21T12:00:00Z"
)

// newTestServer wires up a Store + Server + httptest.Server and seeds one
// valid bearer token. Returned URL is the base URL (no path).
func newTestServer(t *testing.T) (baseURL string, s *store.Store) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "ltm.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	if err := st.PutTokenHash(context.Background(), auth.HashToken(testToken), "test"); err != nil {
		t.Fatalf("seed token: %v", err)
	}

	srv := New(st, log.New(io.Discard, "", 0))
	ts := httptest.NewServer(srv.Handler())

	t.Cleanup(func() {
		ts.Close()
		_ = st.Close()
	})
	return ts.URL, st
}

func validPacketJSON(t *testing.T, id string) []byte {
	t.Helper()
	p := map[string]any{
		"ltm_version": "0.1",
		"id":          id,
		"created_at":  validCreate,
		"goal":        validGoal,
		"next_step":   validNext,
	}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func request(t *testing.T, method, url string, body []byte, withAuth bool) *http.Response {
	t.Helper()
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, url, r)
	if err != nil {
		t.Fatal(err)
	}
	if withAuth {
		req.Header.Set("Authorization", "Bearer "+testToken)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func decodeJSON(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	defer resp.Body.Close()
	var m map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return m
}

func TestHealthz_NoAuthRequired(t *testing.T) {
	url, _ := newTestServer(t)
	resp := request(t, "GET", url+"/v1/healthz", nil, false)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	body := decodeJSON(t, resp)
	if body["ok"] != true {
		t.Errorf("healthz body.ok = %v, want true", body["ok"])
	}
	if body["version"] != ltmschema.Current {
		t.Errorf("healthz version = %v, want %q — must match ltmschema.Current",
			body["version"], ltmschema.Current)
	}
}

func TestAuth_MissingBearerReturns401(t *testing.T) {
	url, _ := newTestServer(t)
	resp := request(t, "GET", url+"/v1/packets", nil, false)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestAuth_MalformedBearerReturns401(t *testing.T) {
	url, _ := newTestServer(t)
	req, _ := http.NewRequest("GET", url+"/v1/packets", nil)
	req.Header.Set("Authorization", "NotBearer xyz")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 for non-Bearer scheme", resp.StatusCode)
	}
}

func TestAuth_UnknownTokenReturns401(t *testing.T) {
	url, _ := newTestServer(t)
	req, _ := http.NewRequest("GET", url+"/v1/packets", nil)
	req.Header.Set("Authorization", "Bearer nope-not-a-real-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestCreatePacket_HappyPath(t *testing.T) {
	url, _ := newTestServer(t)
	body := validPacketJSON(t, validID)
	resp := request(t, "POST", url+"/v1/packets", body, true)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("status = %d, want 201", resp.StatusCode)
	}
	out := decodeJSON(t, resp)
	if out["id"] != validID {
		t.Errorf("response id = %v, want %q", out["id"], validID)
	}
}

func TestCreatePacket_InvalidJSONReturns400(t *testing.T) {
	url, _ := newTestServer(t)
	resp := request(t, "POST", url+"/v1/packets", []byte(`{bad json`), true)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestCreatePacket_SchemaViolationReturns400(t *testing.T) {
	url, _ := newTestServer(t)
	// Missing required next_step.
	body := []byte(`{"ltm_version":"0.1","id":"` + validID + `","created_at":"` + validCreate + `","goal":"g"}`)
	resp := request(t, "POST", url+"/v1/packets", body, true)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestCreatePacket_OversizeReturns400(t *testing.T) {
	url, _ := newTestServer(t)
	// 40KB body exceeds the 32KB server limit.
	body := []byte(`{"ltm_version":"0.1","id":"` + validID + `","created_at":"` + validCreate +
		`","goal":"` + strings.Repeat("x", 40*1024) + `","next_step":"n"}`)
	resp := request(t, "POST", url+"/v1/packets", body, true)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestGetPacket_HappyPath(t *testing.T) {
	url, _ := newTestServer(t)
	body := validPacketJSON(t, validID)
	resp := request(t, "POST", url+"/v1/packets", body, true)
	resp.Body.Close()

	resp = request(t, "GET", url+"/v1/packets/"+validID, nil, true)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	got, _ := io.ReadAll(resp.Body)

	// Server stores canonical encoding; round-tripping through Parse should succeed.
	p, err := packet.Parse(got)
	if err != nil {
		t.Fatalf("returned body not parseable: %v\nbody: %s", err, got)
	}
	if p.ID != validID || p.Goal != validGoal {
		t.Errorf("parsed packet mismatch: got id=%q goal=%q", p.ID, p.Goal)
	}
}

func TestGetPacket_NonExistentReturns404(t *testing.T) {
	url, _ := newTestServer(t)
	resp := request(t, "GET", url+"/v1/packets/does-not-exist", nil, true)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestGetPacket_DeletedReturns404(t *testing.T) {
	url, _ := newTestServer(t)
	body := validPacketJSON(t, validID)
	_ = request(t, "POST", url+"/v1/packets", body, true).Body.Close()
	_ = request(t, "DELETE", url+"/v1/packets/"+validID, nil, true).Body.Close()

	resp := request(t, "GET", url+"/v1/packets/"+validID, nil, true)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404 after delete", resp.StatusCode)
	}
}

func TestListPackets_ReturnsCreated(t *testing.T) {
	url, _ := newTestServer(t)
	// Create two packets with distinct IDs.
	for _, id := range []string{"01JAAAAAAAAAAAAAAAAAAAAAAA", "01JBBBBBBBBBBBBBBBBBBBBBBB"} {
		body := validPacketJSON(t, id)
		_ = request(t, "POST", url+"/v1/packets", body, true).Body.Close()
	}

	resp := request(t, "GET", url+"/v1/packets", nil, true)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	out := decodeJSON(t, resp)
	packets, ok := out["packets"].([]any)
	if !ok {
		t.Fatalf("packets field missing or wrong type: %T", out["packets"])
	}
	if len(packets) != 2 {
		t.Errorf("got %d packets, want 2", len(packets))
	}
}

func TestListPackets_RespectsLimitQuery(t *testing.T) {
	url, _ := newTestServer(t)
	ids := []string{
		"01JAAAAAAAAAAAAAAAAAAAAAAA",
		"01JBBBBBBBBBBBBBBBBBBBBBBB",
		"01JCCCCCCCCCCCCCCCCCCCCCCC",
	}
	for _, id := range ids {
		_ = request(t, "POST", url+"/v1/packets", validPacketJSON(t, id), true).Body.Close()
	}

	resp := request(t, "GET", url+"/v1/packets?limit=2", nil, true)
	defer resp.Body.Close()
	out := decodeJSON(t, resp)
	packets, _ := out["packets"].([]any)
	if len(packets) != 2 {
		t.Errorf("limit=2: got %d packets, want 2", len(packets))
	}
}

func TestDeletePacket_ReturnsNoContent(t *testing.T) {
	url, _ := newTestServer(t)
	_ = request(t, "POST", url+"/v1/packets", validPacketJSON(t, validID), true).Body.Close()

	resp := request(t, "DELETE", url+"/v1/packets/"+validID, nil, true)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("status = %d, want 204", resp.StatusCode)
	}
}

func TestDeletePacket_NonExistentReturns404(t *testing.T) {
	url, _ := newTestServer(t)
	resp := request(t, "DELETE", url+"/v1/packets/never-existed", nil, true)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestDeletePacket_IdempotentSecondCallReturns404(t *testing.T) {
	url, _ := newTestServer(t)
	_ = request(t, "POST", url+"/v1/packets", validPacketJSON(t, validID), true).Body.Close()

	_ = request(t, "DELETE", url+"/v1/packets/"+validID, nil, true).Body.Close()
	resp := request(t, "DELETE", url+"/v1/packets/"+validID, nil, true)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("second Delete: status = %d, want 404", resp.StatusCode)
	}
}

func TestCreatePacket_ResurrectsSoftDeleted(t *testing.T) {
	// End-to-end multi-device scenario: client A pushes, client B deletes,
	// client A re-pushes same id. Re-push must succeed (undelete) and result
	// must be fetchable.
	url, _ := newTestServer(t)
	_ = request(t, "POST", url+"/v1/packets", validPacketJSON(t, validID), true).Body.Close()
	_ = request(t, "DELETE", url+"/v1/packets/"+validID, nil, true).Body.Close()

	resp := request(t, "POST", url+"/v1/packets", validPacketJSON(t, validID), true)
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("re-push status = %d, want 201", resp.StatusCode)
	}
	resp.Body.Close()

	resp = request(t, "GET", url+"/v1/packets/"+validID, nil, true)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Get after resurrection: status = %d, want 200", resp.StatusCode)
	}
}

func TestNew_NilLoggerDefaults(t *testing.T) {
	// New(nil) must substitute log.Default() instead of panicking — otherwise
	// 'ltm server' would crash on any callpath that skipped wiring a logger.
	dbPath := filepath.Join(t.TempDir(), "ltm.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s := New(st, nil)
	if s.Logger == nil {
		t.Error("expected New to substitute a non-nil logger when given nil")
	}
}

func TestListPackets_IgnoresMalformedLimit(t *testing.T) {
	// A non-numeric limit should not error — it just falls back to the default.
	url, _ := newTestServer(t)
	_ = request(t, "POST", url+"/v1/packets", validPacketJSON(t, validID), true).Body.Close()

	resp := request(t, "GET", url+"/v1/packets?limit=not-a-number", nil, true)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 (malformed limit should be ignored)", resp.StatusCode)
	}
}

func TestShutdown_ClosesStore(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "ltm.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	srv := New(st, log.New(io.Discard, "", 0))
	if err := srv.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	// Subsequent DB ops should fail because the connection is closed.
	if _, err := st.ListPackets(context.Background(), 10); err == nil {
		t.Error("expected store operation to fail after Shutdown, got nil")
	}
}

func TestCreatePacket_StoresCanonicalForm(t *testing.T) {
	// Whatever indentation/key-order the client sends, the server must persist
	// canonical form. Send a minified packet; expect the retrieved body to
	// round-trip through Parse and match.
	url, _ := newTestServer(t)
	minified := validPacketJSON(t, validID)
	// Also include extra whitespace in the raw request that should be normalized away.
	padded := append([]byte("   "), minified...)
	padded = append(padded, []byte("\n\n")...)

	resp := request(t, "POST", url+"/v1/packets", padded, true)
	resp.Body.Close()

	resp = request(t, "GET", url+"/v1/packets/"+validID, nil, true)
	defer resp.Body.Close()
	got, _ := io.ReadAll(resp.Body)

	p, err := packet.Parse(got)
	if err != nil {
		t.Fatalf("canonical body not parseable: %v\nbody: %s", err, got)
	}
	if p.ID != validID {
		t.Errorf("parsed id = %q, want %q", p.ID, validID)
	}
}

