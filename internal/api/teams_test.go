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
	"testing"
	"time"

	"github.com/dennisdevulder/ltm/internal/auth"
	"github.com/dennisdevulder/ltm/internal/store"
)

// teamTestEnv spins up a server with a clock we can fast-forward and two
// pre-seeded users (alice the owner, bob an outsider). The second bearer
// token lets tests assert cross-user authz without a second httptest server.
type teamTestEnv struct {
	baseURL string
	store   *store.Store
	server  *Server
	now     time.Time

	aliceID    string
	aliceToken string
	bobID      string
	bobToken   string
}

func newTeamEnv(t *testing.T) *teamTestEnv {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "ltm.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	ctx := context.Background()
	alice := store.NewULID()
	bob := store.NewULID()
	if err := st.PutUser(ctx, alice, "", "alice"); err != nil {
		t.Fatal(err)
	}
	if err := st.PutUser(ctx, bob, "", "bob"); err != nil {
		t.Fatal(err)
	}
	const aliceTok, bobTok = "alice-token", "bob-token"
	if err := st.PutTokenHashForUser(ctx, auth.HashToken(aliceTok), "alice", alice); err != nil {
		t.Fatal(err)
	}
	if err := st.PutTokenHashForUser(ctx, auth.HashToken(bobTok), "bob", bob); err != nil {
		t.Fatal(err)
	}

	env := &teamTestEnv{
		store:      st,
		now:        time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC),
		aliceID:    alice,
		aliceToken: aliceTok,
		bobID:      bob,
		bobToken:   bobTok,
	}
	srv := New(st, log.New(io.Discard, "", 0))
	srv.Now = func() time.Time { return env.now }
	env.server = srv

	ts := httptest.NewServer(srv.Handler())
	env.baseURL = ts.URL
	t.Cleanup(func() {
		ts.Close()
		_ = st.Close()
	})
	return env
}

// do sends a JSON request with the given bearer. authToken may be empty for
// unauthenticated requests.
func (e *teamTestEnv) do(t *testing.T, method, path string, body any, authToken string) *http.Response {
	t.Helper()
	var buf io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		buf = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, e.baseURL+path, buf)
	if err != nil {
		t.Fatal(err)
	}
	if buf != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if authToken != "" {
		req.Header.Set("Authorization", "Bearer "+authToken)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// asJSON reads the response body into a map and closes it.
func asJSON(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	defer resp.Body.Close()
	var m map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return m
}

// ---- team CRUD ----

func TestTeams_Create_HappyPath(t *testing.T) {
	e := newTeamEnv(t)
	resp := e.do(t, "POST", "/v1/teams", map[string]string{"name": "alpha"}, e.aliceToken)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	body := asJSON(t, resp)
	team, _ := body["team"].(map[string]any)
	if team["name"] != "alpha" {
		t.Errorf("team.name = %v, want alpha", team["name"])
	}
	if team["owner_id"] != e.aliceID {
		t.Errorf("team.owner_id = %v, want alice (%s)", team["owner_id"], e.aliceID)
	}
}

func TestTeams_Create_RejectsInvalidName(t *testing.T) {
	e := newTeamEnv(t)
	resp := e.do(t, "POST", "/v1/teams", map[string]string{"name": "Has Spaces"}, e.aliceToken)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestTeams_Create_DuplicateNameIs409(t *testing.T) {
	e := newTeamEnv(t)
	_ = e.do(t, "POST", "/v1/teams", map[string]string{"name": "alpha"}, e.aliceToken).Body.Close()
	resp := e.do(t, "POST", "/v1/teams", map[string]string{"name": "alpha"}, e.bobToken)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("status = %d, want 409", resp.StatusCode)
	}
}

func TestTeams_List_OnlyYours(t *testing.T) {
	e := newTeamEnv(t)
	_ = e.do(t, "POST", "/v1/teams", map[string]string{"name": "alpha"}, e.aliceToken).Body.Close()
	_ = e.do(t, "POST", "/v1/teams", map[string]string{"name": "beta"}, e.bobToken).Body.Close()

	resp := e.do(t, "GET", "/v1/teams", nil, e.aliceToken)
	body := asJSON(t, resp)
	teams, _ := body["teams"].([]any)
	if len(teams) != 1 {
		t.Fatalf("alice should see 1 team, got %d: %+v", len(teams), teams)
	}
	if teams[0].(map[string]any)["name"] != "alpha" {
		t.Errorf("wrong team visible: %+v", teams[0])
	}
}

func TestTeams_Delete_NonOwnerIs403(t *testing.T) {
	e := newTeamEnv(t)
	_ = e.do(t, "POST", "/v1/teams", map[string]string{"name": "alpha"}, e.aliceToken).Body.Close()
	// Add bob as a non-owner member.
	team, _ := e.store.GetTeamByName(context.Background(), "alpha")
	_ = e.store.AddTeamMember(context.Background(), team.ID, e.bobID, "member")

	resp := e.do(t, "DELETE", "/v1/teams/alpha", nil, e.bobToken)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("non-owner delete: status = %d, want 403", resp.StatusCode)
	}
}

func TestTeams_Delete_OwnerSucceedsAndRemovesPackets(t *testing.T) {
	e := newTeamEnv(t)
	_ = e.do(t, "POST", "/v1/teams", map[string]string{"name": "alpha"}, e.aliceToken).Body.Close()

	// Push a packet into the team.
	body := validPacketJSON(t, validID)
	resp := e.do(t, "POST", "/v1/packets?team=alpha", json.RawMessage(body), e.aliceToken)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("team push status = %d, want 201, body: %s", resp.StatusCode, mustBody(resp))
	}
	resp.Body.Close()

	// Owner deletes the team.
	resp = e.do(t, "DELETE", "/v1/teams/alpha", nil, e.aliceToken)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete team: status = %d, want 204", resp.StatusCode)
	}

	// The packet should now be unreadable.
	resp = e.do(t, "GET", "/v1/packets/"+validID, nil, e.aliceToken)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("after team delete: get packet status = %d, want 404", resp.StatusCode)
	}
}

// ---- packet scope + authz ----

func TestPackets_PushPersonalVsTeam_AreSeparate(t *testing.T) {
	e := newTeamEnv(t)
	_ = e.do(t, "POST", "/v1/teams", map[string]string{"name": "alpha"}, e.aliceToken).Body.Close()

	personalID := "01JAAAAAAAAAAAAAAAAAAAAAAA"
	teamID := "01JBBBBBBBBBBBBBBBBBBBBBBB"
	_ = e.do(t, "POST", "/v1/packets", json.RawMessage(validPacketJSON(t, personalID)), e.aliceToken).Body.Close()
	_ = e.do(t, "POST", "/v1/packets?team=alpha", json.RawMessage(validPacketJSON(t, teamID)), e.aliceToken).Body.Close()

	// ltm ls — personal only, the team packet must not leak.
	body := asJSON(t, e.do(t, "GET", "/v1/packets", nil, e.aliceToken))
	got := packetIDs(body)
	if !contains(got, personalID) || contains(got, teamID) {
		t.Errorf("personal listing leaked team packet or missed personal: %+v", got)
	}

	// ltm ls -t alpha — team only.
	body = asJSON(t, e.do(t, "GET", "/v1/packets?team=alpha", nil, e.aliceToken))
	got = packetIDs(body)
	if contains(got, personalID) || !contains(got, teamID) {
		t.Errorf("team listing leaked personal packet or missed team: %+v", got)
	}
}

func TestPackets_NonMemberCannotReadTeamScope(t *testing.T) {
	e := newTeamEnv(t)
	_ = e.do(t, "POST", "/v1/teams", map[string]string{"name": "alpha"}, e.aliceToken).Body.Close()
	resp := e.do(t, "POST", "/v1/packets?team=alpha", json.RawMessage(validPacketJSON(t, validID)), e.aliceToken)
	resp.Body.Close()

	// Bob (non-member) listing → 403.
	resp = e.do(t, "GET", "/v1/packets?team=alpha", nil, e.bobToken)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("non-member list: status = %d, want 403", resp.StatusCode)
	}
}

func TestPackets_NonMemberPushTo403UsesTeamExists(t *testing.T) {
	// A non-member pushing with a valid team name that exists must not leak
	// existence: issue spec asks for 404, not 403.
	e := newTeamEnv(t)
	_ = e.do(t, "POST", "/v1/teams", map[string]string{"name": "alpha"}, e.aliceToken).Body.Close()
	resp := e.do(t, "POST", "/v1/packets?team=alpha", json.RawMessage(validPacketJSON(t, validID)), e.bobToken)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("non-member push: status = %d, want 404", resp.StatusCode)
	}
}

func TestPackets_GetTeamScope_NonMemberIs404(t *testing.T) {
	e := newTeamEnv(t)
	_ = e.do(t, "POST", "/v1/teams", map[string]string{"name": "alpha"}, e.aliceToken).Body.Close()
	_ = e.do(t, "POST", "/v1/packets?team=alpha", json.RawMessage(validPacketJSON(t, validID)), e.aliceToken).Body.Close()

	resp := e.do(t, "GET", "/v1/packets/"+validID, nil, e.bobToken)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("non-member get: status = %d, want 404", resp.StatusCode)
	}
}

func TestPackets_DeleteTeamPacket_TeamOwnerAllowed(t *testing.T) {
	e := newTeamEnv(t)
	_ = e.do(t, "POST", "/v1/teams", map[string]string{"name": "alpha"}, e.aliceToken).Body.Close()
	// Add bob as member.
	team, _ := e.store.GetTeamByName(context.Background(), "alpha")
	_ = e.store.AddTeamMember(context.Background(), team.ID, e.bobID, "member")
	// Bob pushes into the team.
	_ = e.do(t, "POST", "/v1/packets?team=alpha", json.RawMessage(validPacketJSON(t, validID)), e.bobToken).Body.Close()
	// Alice (team owner) deletes bob's packet → allowed.
	resp := e.do(t, "DELETE", "/v1/packets/"+validID, nil, e.aliceToken)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("team-owner delete: status = %d, want 204", resp.StatusCode)
	}
}

// ---- invites / join ----

func TestInvites_AcceptUnauthenticatedMintsTokenAndJoins(t *testing.T) {
	e := newTeamEnv(t)
	_ = e.do(t, "POST", "/v1/teams", map[string]string{"name": "alpha"}, e.aliceToken).Body.Close()

	resp := e.do(t, "POST", "/v1/teams/alpha/invites", map[string]any{}, e.aliceToken)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create invite: status = %d, want 201", resp.StatusCode)
	}
	invBody := asJSON(t, resp)
	code, _ := invBody["code"].(string)
	if code == "" {
		t.Fatalf("missing code in invite response: %+v", invBody)
	}

	// Unauthenticated accept — server mints a fresh user + token.
	resp = e.do(t, "POST", "/v1/invites/"+code+"/accept", map[string]any{"display": "carol"}, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("accept: status = %d, body: %s", resp.StatusCode, mustBody(resp))
	}
	body := asJSON(t, resp)
	tok, _ := body["token"].(string)
	if tok == "" {
		t.Errorf("expected minted token in body, got: %+v", body)
	}

	// Use the new token to list team packets — should succeed.
	resp = e.do(t, "GET", "/v1/packets?team=alpha", nil, tok)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("newly-authed user listing team: status = %d, want 200", resp.StatusCode)
	}
}

func TestInvites_AcceptAsExistingUserReturnsNoToken(t *testing.T) {
	e := newTeamEnv(t)
	_ = e.do(t, "POST", "/v1/teams", map[string]string{"name": "alpha"}, e.aliceToken).Body.Close()
	resp := e.do(t, "POST", "/v1/teams/alpha/invites", map[string]any{}, e.aliceToken)
	invBody := asJSON(t, resp)
	code := invBody["code"].(string)

	// Bob redeems with his own bearer — keeps his existing identity, no new token.
	resp = e.do(t, "POST", "/v1/invites/"+code+"/accept", map[string]any{}, e.bobToken)
	body := asJSON(t, resp)
	if _, ok := body["token"]; ok {
		t.Error("existing user redeem: token should not be returned")
	}
	role, err := e.store.TeamMembership(context.Background(),
		mustGetTeamID(t, e.store, "alpha"), e.bobID)
	if err != nil {
		t.Fatal(err)
	}
	if role == "" {
		t.Error("bob should now be a member of alpha")
	}
}

func TestInvites_SecondRedeemIs410(t *testing.T) {
	e := newTeamEnv(t)
	_ = e.do(t, "POST", "/v1/teams", map[string]string{"name": "alpha"}, e.aliceToken).Body.Close()
	resp := e.do(t, "POST", "/v1/teams/alpha/invites", map[string]any{}, e.aliceToken)
	code := asJSON(t, resp)["code"].(string)

	// First redeem — ok.
	_ = e.do(t, "POST", "/v1/invites/"+code+"/accept", map[string]any{"display": "c1"}, "").Body.Close()
	// Second redeem — 410.
	resp = e.do(t, "POST", "/v1/invites/"+code+"/accept", map[string]any{"display": "c2"}, "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusGone {
		t.Errorf("second redeem: status = %d, want 410", resp.StatusCode)
	}
}

func TestInvites_ExpiredAcceptLeavesNoOrphanRows(t *testing.T) {
	// Repeated unauthenticated accepts against an expired/invalid code
	// must not fill the users + tokens tables with orphan rows — the
	// handler has to check invite validity before minting.
	e := newTeamEnv(t)
	_ = e.do(t, "POST", "/v1/teams", map[string]string{"name": "alpha"}, e.aliceToken).Body.Close()
	resp := e.do(t, "POST", "/v1/teams/alpha/invites", map[string]any{}, e.aliceToken)
	code := asJSON(t, resp)["code"].(string)
	e.now = e.now.Add(8 * 24 * time.Hour) // TTL blown

	ctx := context.Background()
	usersBefore, _ := e.store.CountUsers(ctx)
	tokensBefore, _ := e.store.CountTokens(ctx)

	for i := 0; i < 5; i++ {
		resp := e.do(t, "POST", "/v1/invites/"+code+"/accept", map[string]any{}, "")
		if resp.StatusCode != http.StatusGone {
			t.Fatalf("status = %d, want 410", resp.StatusCode)
		}
		resp.Body.Close()
	}

	usersAfter, _ := e.store.CountUsers(ctx)
	tokensAfter, _ := e.store.CountTokens(ctx)
	if usersAfter != usersBefore {
		t.Errorf("users: before=%d after=%d — expired accept leaked rows", usersBefore, usersAfter)
	}
	if tokensAfter != tokensBefore {
		t.Errorf("tokens: before=%d after=%d — expired accept leaked rows", tokensBefore, tokensAfter)
	}
}

func TestInvites_ExpiredReturns410(t *testing.T) {
	e := newTeamEnv(t)
	_ = e.do(t, "POST", "/v1/teams", map[string]string{"name": "alpha"}, e.aliceToken).Body.Close()
	resp := e.do(t, "POST", "/v1/teams/alpha/invites", map[string]any{}, e.aliceToken)
	code := asJSON(t, resp)["code"].(string)

	// Fast-forward 8 days past the 7-day TTL.
	e.now = e.now.Add(8 * 24 * time.Hour)

	resp = e.do(t, "POST", "/v1/invites/"+code+"/accept", map[string]any{}, "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusGone {
		t.Errorf("expired redeem: status = %d, want 410", resp.StatusCode)
	}
}

// ---- members ----

func TestTeams_MembersListRequiresMembership(t *testing.T) {
	e := newTeamEnv(t)
	_ = e.do(t, "POST", "/v1/teams", map[string]string{"name": "alpha"}, e.aliceToken).Body.Close()
	resp := e.do(t, "GET", "/v1/teams/alpha/members", nil, e.bobToken)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("non-member members list: status = %d, want 403", resp.StatusCode)
	}
}

func TestTeams_LeaveSucceeds(t *testing.T) {
	e := newTeamEnv(t)
	_ = e.do(t, "POST", "/v1/teams", map[string]string{"name": "alpha"}, e.aliceToken).Body.Close()
	team, _ := e.store.GetTeamByName(context.Background(), "alpha")
	_ = e.store.AddTeamMember(context.Background(), team.ID, e.bobID, "member")

	resp := e.do(t, "POST", "/v1/teams/alpha/leave", map[string]any{}, e.bobToken)
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("leave: status = %d, want 204", resp.StatusCode)
	}
	resp.Body.Close()

	role, _ := e.store.TeamMembership(context.Background(), team.ID, e.bobID)
	if role != "" {
		t.Errorf("after leave: role = %q, want empty", role)
	}
}

func TestTeams_OwnerCannotLeave(t *testing.T) {
	e := newTeamEnv(t)
	_ = e.do(t, "POST", "/v1/teams", map[string]string{"name": "alpha"}, e.aliceToken).Body.Close()
	resp := e.do(t, "POST", "/v1/teams/alpha/leave", map[string]any{}, e.aliceToken)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("owner leave: status = %d, want 400", resp.StatusCode)
	}
}

// ---- helpers ----

func packetIDs(body map[string]any) []string {
	packets, _ := body["packets"].([]any)
	out := make([]string, 0, len(packets))
	for _, p := range packets {
		out = append(out, p.(map[string]any)["id"].(string))
	}
	return out
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func mustBody(resp *http.Response) string {
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	return string(b)
}

func mustGetTeamID(t *testing.T, st *store.Store, name string) string {
	t.Helper()
	team, err := st.GetTeamByName(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	return team.ID
}
