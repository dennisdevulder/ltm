package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/dennisdevulder/ltm/internal/auth"
	"github.com/dennisdevulder/ltm/internal/packet"
	"github.com/dennisdevulder/ltm/internal/store"
	ltmschema "github.com/dennisdevulder/ltm/schema"
)

// InviteTTL is how long an invite code is valid after it's minted.
const InviteTTL = 7 * 24 * time.Hour

// teamNameRe restricts team names to something safe in URLs and table output.
// Lowercase letters, digits, hyphens, underscores. Length 1–40.
var teamNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,39}$`)

type Server struct {
	Store  *store.Store
	Logger *log.Logger
	// ExternalURL is the server's externally-visible base URL (e.g.
	// "https://ltm.example.com"). Used when minting invite URLs so the
	// printed link points at the correct host. Optional — when empty the
	// invite response falls back to using the request's Host header.
	ExternalURL string
	// Now returns the current time. Tests swap this to fast-forward through
	// the 7-day invite TTL. Defaults to time.Now.
	Now func() time.Time
}

func New(s *store.Store, logger *log.Logger) *Server {
	if logger == nil {
		logger = log.Default()
	}
	return &Server{Store: s, Logger: logger, Now: time.Now}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /v1/healthz", s.healthz)

	mux.Handle("POST /v1/packets", s.auth(http.HandlerFunc(s.createPacket)))
	mux.Handle("GET /v1/packets", s.auth(http.HandlerFunc(s.listPackets)))
	mux.Handle("GET /v1/packets/{id}", s.auth(http.HandlerFunc(s.getPacket)))
	mux.Handle("DELETE /v1/packets/{id}", s.auth(http.HandlerFunc(s.deletePacket)))

	mux.Handle("POST /v1/teams", s.auth(http.HandlerFunc(s.createTeam)))
	mux.Handle("GET /v1/teams", s.auth(http.HandlerFunc(s.listTeams)))
	mux.Handle("DELETE /v1/teams/{name}", s.auth(http.HandlerFunc(s.deleteTeam)))
	mux.Handle("GET /v1/teams/{name}/members", s.auth(http.HandlerFunc(s.listTeamMembers)))
	mux.Handle("DELETE /v1/teams/{name}/members/{user_id}", s.auth(http.HandlerFunc(s.removeTeamMember)))
	mux.Handle("POST /v1/teams/{name}/leave", s.auth(http.HandlerFunc(s.leaveTeam)))
	mux.Handle("POST /v1/teams/{name}/invites", s.auth(http.HandlerFunc(s.createInvite)))

	// Only route that accepts no bearer — the invite code itself is auth.
	mux.HandleFunc("POST /v1/invites/{code}/accept", s.acceptInvite)

	return withLogging(s.Logger, mux)
}

// ---- middleware ----

type ctxKey int

const userIDKey ctxKey = iota

func userIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(userIDKey).(string)
	return v
}

func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if !strings.HasPrefix(h, prefix) {
			writeErr(w, http.StatusUnauthorized, "missing bearer token")
			return
		}
		tok := strings.TrimPrefix(h, prefix)
		hash := auth.HashToken(tok)
		ok, err := s.Store.TokenExists(r.Context(), hash)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "auth lookup failed")
			return
		}
		if !ok {
			writeErr(w, http.StatusUnauthorized, "invalid token")
			return
		}
		uid, err := s.Store.UserForToken(r.Context(), hash)
		if err != nil {
			// Token exists but isn't bound to a user — the server is in a
			// migration-broken state. Treat as 401 so handlers never run
			// without a user context.
			writeErr(w, http.StatusUnauthorized, "token not bound to user")
			return
		}
		ctx := context.WithValue(r.Context(), userIDKey, uid)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func withLogging(logger *log.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &recorder{ResponseWriter: w, status: 200}
		next.ServeHTTP(rw, r)
		logger.Printf("%s %s → %d (%s)", r.Method, r.URL.Path, rw.status, time.Since(start))
	})
}

type recorder struct {
	http.ResponseWriter
	status int
}

func (r *recorder) WriteHeader(code int) { r.status = code; r.ResponseWriter.WriteHeader(code) }

// ---- handlers: health ----

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "version": ltmschema.Current})
}

// ---- handlers: packets ----

func (s *Server) createPacket(w http.ResponseWriter, r *http.Request) {
	uid := userIDFromContext(r.Context())
	teamName := r.URL.Query().Get("team")

	var teamID string
	if teamName != "" {
		t, err := s.Store.GetTeamByName(r.Context(), teamName)
		if err != nil {
			// Hide existence from non-members: 404, not 403.
			writeErr(w, http.StatusNotFound, "team not found")
			return
		}
		role, err := s.Store.TeamMembership(r.Context(), t.ID, uid)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if role == "" {
			writeErr(w, http.StatusNotFound, "team not found")
			return
		}
		teamID = t.ID
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, packet.MaxPacketBytes+1024))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	p, err := packet.Parse(body)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	canonical, err := p.Encode()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "encode: "+err.Error())
		return
	}
	if err := s.Store.PutPacket(r.Context(), p.ID, p.CreatedAt, p.Goal, canonical, uid, teamID); err != nil {
		writeErr(w, http.StatusInternalServerError, "store: "+err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": p.ID})
}

func (s *Server) listPackets(w http.ResponseWriter, r *http.Request) {
	uid := userIDFromContext(r.Context())
	limit := 50
	if q := r.URL.Query().Get("limit"); q != "" {
		if n, atoiErr := strconv.Atoi(q); atoiErr == nil {
			limit = n
		}
	}

	rows, status, err := s.fetchPacketsInScope(r.Context(), uid, r.URL.Query().Get("team"), limit)
	if err != nil {
		writeErr(w, status, err.Error())
		return
	}
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		items = append(items, map[string]any{
			"id":         row.ID,
			"created_at": row.CreatedAt.Format(time.RFC3339),
			"goal":       row.Goal,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"packets": items})
}

// fetchPacketsInScope returns the packets the caller is allowed to see for
// the given scope. An empty team means personal scope. Membership is
// enforced here so the caller only has to worry about rendering.
func (s *Server) fetchPacketsInScope(ctx context.Context, uid, team string, limit int) ([]store.PacketRow, int, error) {
	if team == "" {
		rows, err := s.Store.ListPacketsForOwner(ctx, uid, limit)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return rows, http.StatusOK, nil
	}
	t, err := s.Store.GetTeamByName(ctx, team)
	if err != nil {
		return nil, http.StatusForbidden, errors.New("forbidden")
	}
	role, err := s.Store.TeamMembership(ctx, t.ID, uid)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	if role == "" {
		return nil, http.StatusForbidden, errors.New("forbidden")
	}
	rows, err := s.Store.ListPacketsForTeam(ctx, t.ID, limit)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	return rows, http.StatusOK, nil
}

func (s *Server) getPacket(w http.ResponseWriter, r *http.Request) {
	uid := userIDFromContext(r.Context())
	id := r.PathValue("id")
	row, err := s.Store.GetPacket(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !s.canReadPacket(r.Context(), row, uid) {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(row.Body)
}

func (s *Server) deletePacket(w http.ResponseWriter, r *http.Request) {
	uid := userIDFromContext(r.Context())
	id := r.PathValue("id")
	row, err := s.Store.GetPacket(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !s.canDeletePacket(r.Context(), row, uid) {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	if err := s.Store.DeletePacket(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// canReadPacket — personal: owner must match; team: caller must be member.
func (s *Server) canReadPacket(ctx context.Context, row *store.PacketRow, uid string) bool {
	if row.TeamID == "" {
		return row.OwnerID == uid
	}
	role, err := s.Store.TeamMembership(ctx, row.TeamID, uid)
	return err == nil && role != ""
}

// canDeletePacket — personal: owner. Team: team owner OR packet creator.
func (s *Server) canDeletePacket(ctx context.Context, row *store.PacketRow, uid string) bool {
	if row.TeamID == "" {
		return row.OwnerID == uid
	}
	if row.OwnerID == uid {
		return true
	}
	team, err := s.Store.GetTeamByID(ctx, row.TeamID)
	if err != nil {
		return false
	}
	return team.OwnerID == uid
}

// ---- handlers: teams ----

type createTeamBody struct {
	Name string `json:"name"`
}

func (s *Server) createTeam(w http.ResponseWriter, r *http.Request) {
	uid := userIDFromContext(r.Context())
	var in createTeamBody
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	name := strings.TrimSpace(in.Name)
	if !teamNameRe.MatchString(name) {
		writeErr(w, http.StatusBadRequest, "team name must match ^[a-z0-9][a-z0-9_-]{0,39}$")
		return
	}
	id := store.NewULID()
	if err := s.Store.CreateTeam(r.Context(), id, name, uid); err != nil {
		if errors.Is(err, store.ErrNameTaken) {
			writeErr(w, http.StatusConflict, "team name already taken")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"team": map[string]any{
			"id":       id,
			"name":     name,
			"owner_id": uid,
		},
	})
}

func (s *Server) listTeams(w http.ResponseWriter, r *http.Request) {
	uid := userIDFromContext(r.Context())
	teams, err := s.Store.ListTeamsForUser(r.Context(), uid)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]any, 0, len(teams))
	for _, t := range teams {
		out = append(out, map[string]any{
			"id":         t.ID,
			"name":       t.Name,
			"owner_id":   t.OwnerID,
			"created_at": t.CreatedAt.Format(time.RFC3339),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"teams": out})
}

func (s *Server) deleteTeam(w http.ResponseWriter, r *http.Request) {
	uid := userIDFromContext(r.Context())
	name := r.PathValue("name")
	team, err := s.Store.GetTeamByName(r.Context(), name)
	if err != nil {
		writeErr(w, http.StatusNotFound, "team not found")
		return
	}
	if team.OwnerID != uid {
		writeErr(w, http.StatusForbidden, "only the team owner can delete a team")
		return
	}
	if err := s.Store.DeleteTeam(r.Context(), team.ID); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listTeamMembers(w http.ResponseWriter, r *http.Request) {
	uid := userIDFromContext(r.Context())
	name := r.PathValue("name")
	team, err := s.Store.GetTeamByName(r.Context(), name)
	if err != nil {
		writeErr(w, http.StatusNotFound, "team not found")
		return
	}
	role, err := s.Store.TeamMembership(r.Context(), team.ID, uid)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if role == "" {
		writeErr(w, http.StatusForbidden, "not a team member")
		return
	}
	members, err := s.Store.ListTeamMembers(r.Context(), team.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]any, 0, len(members))
	for _, m := range members {
		item := map[string]any{
			"user_id":   m.UserID,
			"display":   m.Display,
			"role":      m.Role,
			"joined_at": m.JoinedAt.Format(time.RFC3339),
		}
		if m.Email != "" {
			item["email"] = m.Email
		}
		out = append(out, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": out})
}

func (s *Server) removeTeamMember(w http.ResponseWriter, r *http.Request) {
	uid := userIDFromContext(r.Context())
	name := r.PathValue("name")
	target := r.PathValue("user_id")
	team, err := s.Store.GetTeamByName(r.Context(), name)
	if err != nil {
		writeErr(w, http.StatusNotFound, "team not found")
		return
	}
	if team.OwnerID != uid {
		writeErr(w, http.StatusForbidden, "only the team owner can remove members")
		return
	}
	if target == team.OwnerID {
		writeErr(w, http.StatusBadRequest, "owner cannot be removed; transfer ownership or delete the team")
		return
	}
	if err := s.Store.RemoveTeamMember(r.Context(), team.ID, target); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not a member")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) leaveTeam(w http.ResponseWriter, r *http.Request) {
	uid := userIDFromContext(r.Context())
	name := r.PathValue("name")
	team, err := s.Store.GetTeamByName(r.Context(), name)
	if err != nil {
		writeErr(w, http.StatusNotFound, "team not found")
		return
	}
	if team.OwnerID == uid {
		writeErr(w, http.StatusBadRequest, "owner cannot leave; transfer ownership or delete the team")
		return
	}
	if err := s.Store.RemoveTeamMember(r.Context(), team.ID, uid); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not a member")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- handlers: invites ----

func (s *Server) createInvite(w http.ResponseWriter, r *http.Request) {
	uid := userIDFromContext(r.Context())
	name := r.PathValue("name")
	team, err := s.Store.GetTeamByName(r.Context(), name)
	if err != nil {
		writeErr(w, http.StatusNotFound, "team not found")
		return
	}
	role, err := s.Store.TeamMembership(r.Context(), team.ID, uid)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if role == "" {
		writeErr(w, http.StatusForbidden, "not a team member")
		return
	}
	code, err := newInviteCode()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	now := s.now()
	expires := now.Add(InviteTTL)
	if err := s.Store.CreateInvite(r.Context(), code, team.ID, uid, expires); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"code":       code,
		"url":        s.inviteURL(r, code),
		"expires_at": expires.UTC().Format(time.RFC3339),
	})
}

// acceptInvite: the only unauthenticated write endpoint. The invite code is
// itself authentication. Behaviours:
//   - Caller also presents a valid bearer → existing user joins the team.
//   - Unauthenticated caller → server mints a fresh user + token and returns
//     the token in the body so `ltm join <url>` works on a clean machine.
//
// The invite is validated (non-atomically) before any users or tokens are
// minted, so repeated calls to /accept with a bad code don't fill the DB
// with orphan rows. The authoritative atomic check is still in ConsumeInvite
// — the pre-check just guards against unauthenticated DB growth.
func (s *Server) acceptInvite(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")

	// Optional bearer resolves to an existing user.
	existingUID := ""
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		tok := strings.TrimPrefix(h, "Bearer ")
		hash := auth.HashToken(tok)
		if uid, err := s.Store.UserForToken(r.Context(), hash); err == nil {
			existingUID = uid
		}
	}

	var displayName string
	if r.Body != nil {
		var in struct {
			Display string `json:"display"`
		}
		_ = json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&in)
		displayName = strings.TrimSpace(in.Display)
	}

	// Pre-check: is the invite even redeemable? We still rely on
	// ConsumeInvite below for atomic single-use semantics, but this stops
	// unauthenticated callers from minting users just to hit a 410.
	if existingUID == "" {
		if err := s.Store.CheckInviteRedeemable(r.Context(), code, s.now()); err != nil {
			if errors.Is(err, store.ErrInviteGone) {
				writeErr(w, http.StatusGone, "invite expired or already consumed")
				return
			}
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	var (
		uid        string
		tokenPlain string
	)
	if existingUID != "" {
		uid = existingUID
	} else {
		newUID := store.NewULID()
		if displayName == "" {
			displayName = "invited-" + newUID[len(newUID)-6:]
		}
		if err := s.Store.PutUser(r.Context(), newUID, "", displayName); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		tokenPlain = packet.RandomToken()
		if err := s.Store.PutTokenHashForUser(r.Context(), auth.HashToken(tokenPlain), displayName, newUID); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		uid = newUID
	}

	inv, err := s.Store.ConsumeInvite(r.Context(), code, uid, s.now())
	if err != nil {
		if errors.Is(err, store.ErrInviteGone) {
			writeErr(w, http.StatusGone, "invite expired or already consumed")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.Store.AddTeamMember(r.Context(), inv.TeamID, uid, "member"); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	team, err := s.Store.GetTeamByID(r.Context(), inv.TeamID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	user, err := s.Store.GetUser(r.Context(), uid)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	resp := map[string]any{
		"user": map[string]any{
			"id":      user.ID,
			"display": user.Display,
		},
		"team": map[string]any{
			"id":   team.ID,
			"name": team.Name,
		},
	}
	if tokenPlain != "" {
		resp["token"] = tokenPlain
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// inviteURL returns the public URL a recipient clicks / pastes into
// `ltm join`. Prefers the configured ExternalURL; falls back to the request's
// scheme+Host so self-hosted servers behind a reverse proxy still get a
// useful link if ExternalURL is left unset.
func (s *Server) inviteURL(r *http.Request, code string) string {
	base := strings.TrimRight(s.ExternalURL, "/")
	if base == "" {
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		base = scheme + "://" + r.Host
	}
	return base + "/v1/invites/" + code
}

// ---- id/code helpers ----

func newInviteCode() (string, error) {
	// 24 bytes of randomness → 48 hex chars. URL-safe, short-lived,
	// single-use, so plaintext storage is acceptable.
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// ---- helpers ----

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}

// Shutdown gracefully closes the underlying store.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.Store.Close()
}
