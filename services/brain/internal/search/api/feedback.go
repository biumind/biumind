// Per-result feedback handling for the unified search page.
//
//	POST   /v1/search/feedback   { query, page_id, signal, rank?, source? }
//	   ↳ upsert (user, query_lower, page) → signal
//	     · same signal again on the same key  → no-op (200)
//	     · different signal                   → flip
//
//	DELETE /v1/search/feedback   { query, page_id }
//	   ↳ remove the user's existing verdict (UI thumbs toggle off)
//
// Both routes share the same auth middleware as POST /v1/search and
// run on the bm25 store's pgx pool — keeping data + transport
// adjacent so feedback shipping never accidentally races a search.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func (s *Server) mountFeedback(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/search/feedback", s.requireAuth(s.handlePostFeedback))
	mux.HandleFunc("DELETE /v1/search/feedback", s.requireAuth(s.handleDeleteFeedback))
	mux.HandleFunc("GET /v1/search/feedback", s.requireAuth(s.handleListFeedback))
}

// ─── wire types ────────────────────────────────────────────────

type feedbackReq struct {
	Query     string         `json:"query"`
	PageID    string         `json:"page_id"`
	ProjectID string         `json:"project_id,omitempty"`
	Signal    string         `json:"signal"` // "up" | "down"
	Rank      int            `json:"rank,omitempty"`
	Meta      map[string]any `json:"meta,omitempty"`
}

type deleteFeedbackReq struct {
	Query  string `json:"query"`
	PageID string `json:"page_id"`
}

// ─── handlers ──────────────────────────────────────────────────

func (s *Server) handlePostFeedback(w http.ResponseWriter, r *http.Request) {
	var req feedbackReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	q := strings.TrimSpace(strings.ToLower(req.Query))
	if q == "" {
		writeErr(w, http.StatusBadRequest, "missing_query", "")
		return
	}
	pageID, err := uuid.Parse(req.PageID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_page_id", err.Error())
		return
	}
	if req.Signal != "up" && req.Signal != "down" {
		writeErr(w, http.StatusBadRequest, "bad_signal",
			`signal must be "up" or "down"`)
		return
	}
	var projID *uuid.UUID
	if req.ProjectID != "" {
		v, err := uuid.Parse(req.ProjectID)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "bad_project_id", err.Error())
			return
		}
		projID = &v
	}

	uid, ok := userIDFromCtx(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "bad_user", "")
		return
	}
	pool := s.feedbackPool()
	if pool == nil {
		writeErr(w, http.StatusServiceUnavailable,
			"no_pool", "search backend not configured")
		return
	}

	meta := req.Meta
	if meta == nil {
		meta = map[string]any{}
	}
	metaJSON, _ := json.Marshal(meta)

	// Upsert on the unique (user, query_lower, page) key. Same signal
	// → DO NOTHING (idempotent). Different signal → DO UPDATE flips
	// the row + bumps updated_at + refreshes rank/meta with the new
	// click context.
	if _, err := pool.Exec(r.Context(), `
		INSERT INTO brain.search_feedback
		    (user_id, project_id, query_lower, page_id, rank, signal, meta)
		VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb)
		ON CONFLICT (user_id, query_lower, page_id) DO UPDATE
		   SET signal = EXCLUDED.signal,
		       rank = EXCLUDED.rank,
		       meta = EXCLUDED.meta,
		       updated_at = now()
		   WHERE brain.search_feedback.signal IS DISTINCT FROM EXCLUDED.signal
		      OR brain.search_feedback.rank   IS DISTINCT FROM EXCLUDED.rank
	`, uid, projID, q, pageID, req.Rank, req.Signal, metaJSON); err != nil {
		s.Logger.Warn("search_feedback upsert failed", "err", err)
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"signal":  req.Signal,
		"page_id": pageID.String(),
	})
}

func (s *Server) handleDeleteFeedback(w http.ResponseWriter, r *http.Request) {
	var req deleteFeedbackReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	q := strings.TrimSpace(strings.ToLower(req.Query))
	if q == "" {
		writeErr(w, http.StatusBadRequest, "missing_query", "")
		return
	}
	pageID, err := uuid.Parse(req.PageID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_page_id", err.Error())
		return
	}
	uid, ok := userIDFromCtx(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "bad_user", "")
		return
	}
	pool := s.feedbackPool()
	if pool == nil {
		writeErr(w, http.StatusServiceUnavailable,
			"no_pool", "search backend not configured")
		return
	}
	tag, err := pool.Exec(r.Context(), `
		DELETE FROM brain.search_feedback
		 WHERE user_id = $1 AND query_lower = $2 AND page_id = $3
	`, uid, q, pageID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"deleted": tag.RowsAffected(),
	})
}

// ─── helpers ───────────────────────────────────────────────────

// feedbackPool returns the pgx pool used by feedback writes. We
// reuse the pool the BM25 searcher already holds — there's no
// separate database connection layer for feedback, just a different
// table on the same brain DB.
func (s *Server) feedbackPool() *pgxpool.Pool {
	if s == nil || s.BM25 == nil {
		return nil
	}
	return s.BM25.Pool
}

// handleListFeedback returns the user's existing verdicts for one query.
//
//   GET /v1/search/feedback?query=<q>
//     → {"verdicts": [{"page_id":"<uuid>", "signal":"up"|"down"}, …]}
//
// Used by the search UI on result render: optimistic thumbs need to
// know what the user previously chose for the same (query, page)
// pair so the buttons render in their correct state on first paint.
//
// We don't filter by page_ids here — the response is bounded by the
// distinct page count for one (user, query), which is small in
// practice (≤ result limit ≈ 30). Returning the lot lets the client
// hydrate any subset without a second request when the list re-ranks.
func (s *Server) handleListFeedback(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("query")))
	if q == "" {
		writeErr(w, http.StatusBadRequest, "missing_query", "")
		return
	}
	uid, ok := userIDFromCtx(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "bad_user", "")
		return
	}
	pool := s.feedbackPool()
	if pool == nil {
		writeErr(w, http.StatusServiceUnavailable,
			"no_pool", "search backend not configured")
		return
	}
	rows, err := pool.Query(r.Context(), `
		SELECT page_id, signal
		  FROM brain.search_feedback
		 WHERE user_id = $1 AND query_lower = $2
	`, uid, q)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	defer rows.Close()
	verdicts := make([]map[string]any, 0)
	for rows.Next() {
		var (
			pid    uuid.UUID
			signal string
		)
		if err := rows.Scan(&pid, &signal); err != nil {
			writeErr(w, http.StatusInternalServerError, "internal", err.Error())
			return
		}
		verdicts = append(verdicts, map[string]any{
			"page_id": pid.String(),
			"signal":  signal,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"query":    q,
		"verdicts": verdicts,
	})
}

// userIDFromCtx extracts the JWT subject as a UUID. Returns (Nil, false)
// when the claim is absent or malformed; handlers translate that into
// a 401. We define this here (rather than in api.go) because api.go's
// handleSearch parses the user id inline; future refactor can hoist.
func userIDFromCtx(r *http.Request) (uuid.UUID, bool) {
	claims := bauth.MustClaims(r.Context())
	uid, err := uuid.Parse(claims.UserID)
	if err != nil {
		return uuid.Nil, false
	}
	return uid, true
}

// ── Convenience: ListFeedbackForUser is exposed for tests + future
// admin endpoints. Not mounted as a route yet.

type FeedbackRow struct {
	UserID      uuid.UUID
	ProjectID   *uuid.UUID
	QueryLower  string
	PageID      uuid.UUID
	Rank        int
	Signal      string
}

// ListFeedbackForUser is unused by HTTP today but lives here so future
// admin / training-pipeline pulls can call it directly without
// reimplementing the SQL.
func (s *Server) ListFeedbackForUser(ctx context.Context, userID uuid.UUID, limit int) ([]FeedbackRow, error) {
	pool := s.feedbackPool()
	if pool == nil {
		return nil, errors.New("search backend not configured")
	}
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := pool.Query(ctx, `
		SELECT user_id, project_id, query_lower, page_id, rank, signal
		  FROM brain.search_feedback
		 WHERE user_id = $1
		 ORDER BY updated_at DESC
		 LIMIT $2
	`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FeedbackRow
	for rows.Next() {
		var r FeedbackRow
		if err := rows.Scan(
			&r.UserID, &r.ProjectID, &r.QueryLower,
			&r.PageID, &r.Rank, &r.Signal); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// silence unused-import lint when only one of bauth / pgx symbols
// is referenced in some build tags. They're used by handlePost/Delete
// + pool methods at the package level; this is a no-op at runtime.
var (
	_ = bauth.MustClaims
	_ = pgx.ErrNoRows
)
