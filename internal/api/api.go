package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/dennisdevulder/ltm/internal/auth"
	"github.com/dennisdevulder/ltm/internal/packet"
	"github.com/dennisdevulder/ltm/internal/store"
)

type Server struct {
	Store  *store.Store
	Logger *log.Logger
}

func New(s *store.Store, logger *log.Logger) *Server {
	if logger == nil {
		logger = log.Default()
	}
	return &Server{Store: s, Logger: logger}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /v1/healthz", s.healthz)

	mux.Handle("POST /v1/packets", s.auth(http.HandlerFunc(s.createPacket)))
	mux.Handle("GET /v1/packets", s.auth(http.HandlerFunc(s.listPackets)))
	mux.Handle("GET /v1/packets/{id}", s.auth(http.HandlerFunc(s.getPacket)))
	mux.Handle("DELETE /v1/packets/{id}", s.auth(http.HandlerFunc(s.deletePacket)))

	return withLogging(s.Logger, mux)
}

// ---- middleware ----

func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if !strings.HasPrefix(h, prefix) {
			writeErr(w, http.StatusUnauthorized, "missing bearer token")
			return
		}
		tok := strings.TrimPrefix(h, prefix)
		ok, err := s.Store.TokenExists(r.Context(), auth.HashToken(tok))
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "auth lookup failed")
			return
		}
		if !ok {
			writeErr(w, http.StatusUnauthorized, "invalid token")
			return
		}
		next.ServeHTTP(w, r)
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

// ---- handlers ----

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "version": "0.1"})
}

func (s *Server) createPacket(w http.ResponseWriter, r *http.Request) {
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
	// Re-encode into canonical form before storing.
	canonical, err := p.Encode()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "encode: "+err.Error())
		return
	}
	if err := s.Store.PutPacket(r.Context(), p.ID, p.CreatedAt, p.Goal, canonical); err != nil {
		writeErr(w, http.StatusInternalServerError, "store: "+err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": p.ID})
}

func (s *Server) listPackets(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if q := r.URL.Query().Get("limit"); q != "" {
		if n, err := strconv.Atoi(q); err == nil {
			limit = n
		}
	}
	rows, err := s.Store.ListPackets(r.Context(), limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
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

func (s *Server) getPacket(w http.ResponseWriter, r *http.Request) {
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
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(row.Body)
}

func (s *Server) deletePacket(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
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
