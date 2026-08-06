// REST surface for page relatedness.
//
//   GET /v1/wiki/pages/{id}/related?limit=20
//
// Returns the top-K related pages with score + per-signal breakdown.
// Single endpoint for now — sidebars / "see also" panels in the
// Flutter client and the MCP wiki.related_pages tool both consume it.
package relevance

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	wikistore "github.com/biumind/biumind/services/brain/internal/wiki/store"
	"github.com/google/uuid"
)

type Server struct {
	Store    *Store
	Wiki     *wikistore.Store
	Verifier *bauth.Verifier
	Logger   *slog.Logger
}

func NewServer(s *Store, w *wikistore.Store, v *bauth.Verifier, l *slog.Logger) *Server {
	return &Server{Store: s, Wiki: w, Verifier: v, Logger: l}
}

func (s *Server) Mount(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/wiki/pages/{id}/related", s.requireAuth(s.handleRelated))
}

type relatedOut struct {
	PageID  string             `json:"page_id"`
	Title   string             `json:"title"`
	Score   float32            `json:"score"`
	Signals map[string]float32 `json:"signals"`
}

func (s *Server) handleRelated(w http.ResponseWriter, r *http.Request) {
	pageID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_id", "")
		return
	}
	page, err := s.Wiki.GetPage(r.Context(), pageID)
	if err != nil {
		if errors.Is(err, wikistore.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", "page")
			return
		}
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	uid := mustUserID(r)
	proj, err := s.Wiki.GetProject(r.Context(), page.ProjectID)
	if err != nil || proj.OwnerID != uid {
		// Indistinguishable 404 to avoid existence leaks across tenants.
		writeErr(w, http.StatusNotFound, "not_found", "page")
		return
	}

	limit := 20
	if v := r.URL.Query().Get("limit"); v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil && n > 0 {
			limit = n
		}
	}
	rows, err := s.Store.ListRelated(r.Context(), pageID, limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	out := make([]relatedOut, 0, len(rows))
	for _, ro := range rows {
		out = append(out, relatedOut{
			PageID:  ro.OtherPageID.String(),
			Title:   ro.Title,
			Score:   ro.Score,
			Signals: ro.Signals,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"page_id": pageID.String(),
		"related": out,
		"server_time": time.Now().UTC().Format(time.RFC3339),
	})
}

// ─── auth helpers ──────────────────────────────────────────────

func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if len(auth) < 8 || auth[:7] != "Bearer " {
			writeErr(w, http.StatusUnauthorized, "missing_bearer", "")
			return
		}
		claims, err := s.Verifier.Verify(auth[7:])
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "invalid_token", err.Error())
			return
		}
		next(w, r.WithContext(bauth.WithClaims(r.Context(), claims)))
	}
}

func mustUserID(r *http.Request) uuid.UUID {
	c := bauth.MustClaims(r.Context())
	uid, _ := uuid.Parse(c.UserID)
	return uid
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{"code": code, "message": msg},
	})
}
