// HTTP surface for review_items.
//
//	GET    /v1/wiki/projects/{pid}/reviews                    list (filter by ?kind, ?status)
//	POST   /v1/wiki/reviews/{id}/resolve                      mark resolved
//	POST   /v1/wiki/reviews/{id}/dismiss                      mark dismissed
//
// The status-mutation endpoints take no body — the action is encoded
// in the URL. We could collapse to a single POST /reviews/{id}/status
// with `{status: "resolved" | "dismissed"}`, but per-action URLs
// surface intent clearly in API logs and let CSRF middleware (when
// added later) treat each action with its own policy.
package reviews

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
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
	// Semantic drives /reviews/scan family=semantic (nil ⇒ 503).
	// Injected post-construction once reviewsStore + signer exist
	// (main.go relay+jwt block), same SetSemantic shape the old
	// wiki/lint server used.
	Semantic *SemanticRunner
	// Lint drives /reviews/scan family=structural (nil ⇒ 503). Always
	// wired in main.go — the scan endpoint works even when the periodic
	// 12h worker is disabled (LINT_INTERVAL_HOURS=0).
	Lint *LintWorker
}

func NewServer(s *Store, w *wikistore.Store, v *bauth.Verifier, l *slog.Logger) *Server {
	return &Server{Store: s, Wiki: w, Verifier: v, Logger: l}
}

// SetSemantic injects the semantic lint runner after construction
// (main.go builds it once model-relay + JWT are configured).
func (s *Server) SetSemantic(sem *SemanticRunner) { s.Semantic = sem }

// SetLint injects the lint worker so /reviews/scan family=structural
// can trigger an on-demand re-scan.
func (s *Server) SetLint(lw *LintWorker) { s.Lint = lw }

func (s *Server) Mount(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/wiki/projects/{pid}/reviews", s.requireAuth(s.handleList))
	mux.HandleFunc("GET /v1/wiki/projects/{pid}/reviews/summary",
		s.requireAuth(s.handleSummary))
	mux.HandleFunc("POST /v1/wiki/projects/{pid}/reviews/scan",
		s.requireAuth(s.handleScan))
	mux.HandleFunc("POST /v1/wiki/reviews/{id}/resolve", s.requireAuth(s.handleResolve))
	mux.HandleFunc("POST /v1/wiki/reviews/{id}/dismiss", s.requireAuth(s.handleDismiss))
	mux.HandleFunc("POST /v1/wiki/reviews/{id}/delete-page",
		s.requireAuth(s.handleDeletePageAction))
	mux.HandleFunc("POST /v1/wiki/pages/{id}/merge", s.requireAuth(s.handleMerge))
}

// ─── Wire types ────────────────────────────────────────────────

type itemOut struct {
	ID          string         `json:"id"`
	ProjectID   string         `json:"project_id"`
	Kind        string         `json:"kind"`
	Status      string         `json:"status"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	PageIDs     []string       `json:"page_ids"`
	Payload     map[string]any `json:"payload"`
	ResolvedAt  *string        `json:"resolved_at,omitempty"`
	CreatedAt   string         `json:"created_at"`
	UpdatedAt   string         `json:"updated_at"`
}

func itemJSON(i *Item) itemOut {
	out := itemOut{
		ID: i.ID.String(), ProjectID: i.ProjectID.String(),
		Kind: i.Kind, Status: i.Status,
		Title: i.Title, Description: i.Description,
		Payload:   i.Payload,
		PageIDs:   make([]string, 0, len(i.PageIDs)),
		CreatedAt: i.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt: i.UpdatedAt.UTC().Format(time.RFC3339),
	}
	for _, p := range i.PageIDs {
		out.PageIDs = append(out.PageIDs, p.String())
	}
	if i.ResolvedAt != nil {
		v := i.ResolvedAt.UTC().Format(time.RFC3339)
		out.ResolvedAt = &v
	}
	return out
}

// ─── Handlers ──────────────────────────────────────────────────

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	pid, err := uuid.Parse(r.PathValue("pid"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_project_id", "")
		return
	}
	if !s.ownsProject(w, r, pid) {
		return
	}
	q := r.URL.Query()
	kind := q.Get("kind")
	status := q.Get("status")
	if status == "" {
		// Default to open — what the UI almost always wants
		status = StatusOpen
	}
	limit := 100
	if v := q.Get("limit"); v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil && n > 0 {
			limit = n
		}
	}
	items, err := s.Store.List(r.Context(), ListInput{
		ProjectID: pid, Kind: kind, Status: status, Limit: limit,
	})
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	out := make([]itemOut, 0, len(items))
	for _, it := range items {
		out = append(out, itemJSON(it))
	}
	writeJSON(w, http.StatusOK, map[string]any{"reviews": out})
}

func (s *Server) handleResolve(w http.ResponseWriter, r *http.Request) {
	s.transitionStatus(w, r, StatusResolved)
}

func (s *Server) handleDismiss(w http.ResponseWriter, r *http.Request) {
	s.transitionStatus(w, r, StatusDismissed)
}

func (s *Server) transitionStatus(w http.ResponseWriter, r *http.Request, status string) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_id", "")
		return
	}
	it, err := s.Store.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	uid := mustUserID(r)
	if it.OwnerID != uid {
		// Indistinguishable 404 to avoid existence leaks.
		writeErr(w, http.StatusNotFound, "not_found", "")
		return
	}
	if err := s.Store.SetStatus(r.Context(), id, status); err != nil {
		if errors.Is(err, ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		// "review already X" — surface as 409 for clear UX.
		writeErr(w, http.StatusConflict, "conflict", err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted,
		map[string]any{"id": id.String(), "status": status})
}

// ─── Summary ───────────────────────────────────────────────────

type ruleCountOut struct {
	Kind   string `json:"kind"`
	RuleID string `json:"rule_id"`
	Count  int    `json:"count"`
}

// handleSummary returns one open-finding histogram per project. Powers
// the cleanup dashboard's summary cards — the UI then drills via the
// regular list endpoint with a kind/rule filter.
func (s *Server) handleSummary(w http.ResponseWriter, r *http.Request) {
	pid, err := uuid.Parse(r.PathValue("pid"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_project_id", "")
		return
	}
	if !s.ownsProject(w, r, pid) {
		return
	}
	rows, err := s.Store.CountOpenByRule(r.Context(), pid)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	out := make([]ruleCountOut, 0, len(rows))
	for _, rc := range rows {
		out = append(out, ruleCountOut{
			Kind:   rc.Kind,
			RuleID: rc.RuleID,
			Count:  rc.Count,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"counts": out})
}

// ─── Cleanup actions ───────────────────────────────────────────

// handleDeletePageAction soft-deletes a page referenced by a review
// (taking the FIRST page id from review_items.page_ids) AND marks the
// review resolved in one shot. Used by the cleanup dashboard for
// rules where "fix" really means "delete":
//
//	empty_page / untitled_page / stub_page / orphaned_page
//
// dead_wikilink and stale_page have no obvious "delete" action — the
// cleanup UI shouldn't expose this button for those rules.
func (s *Server) handleDeletePageAction(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_id", "")
		return
	}
	it, err := s.Store.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	uid := mustUserID(r)
	if it.OwnerID != uid {
		writeErr(w, http.StatusNotFound, "not_found", "")
		return
	}
	if len(it.PageIDs) == 0 {
		writeErr(w, http.StatusBadRequest, "no_page_ids",
			"this review has no associated page")
		return
	}
	pageID := it.PageIDs[0]
	if err := s.Wiki.SoftDeletePage(r.Context(), pageID, uid.String()); err != nil {
		writeErr(w, http.StatusInternalServerError, "delete_failed", err.Error())
		return
	}
	// Best-effort: resolve the review. Failure here doesn't roll back
	// the page delete — the review row will just stay open and the
	// next list refresh will surface a stale "delete this" button on
	// an already-deleted page (the UI ignores it gracefully).
	if rerr := s.Store.SetStatus(r.Context(), id, StatusResolved); rerr != nil {
		s.Logger.Warn("cleanup: auto-resolve review failed",
			"review_id", id, "err", rerr)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"review_id": id.String(),
		"page_id":   pageID.String(),
		"deleted":   true,
	})
}

// ─── Merge ─────────────────────────────────────────────────────

type mergeReq struct {
	FromID string `json:"from_id"` // duplicate page (the one to fold in)
}

// handleMerge folds `from_id` into `{id}` (the canonical page in the URL).
// On success any open dedup review for the pair is auto-resolved so the
// queue stays clean. Both pages must be owned by the calling user.
func (s *Server) handleMerge(w http.ResponseWriter, r *http.Request) {
	canonicalID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_id", "")
		return
	}
	var req mergeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	duplicateID, err := uuid.Parse(req.FromID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_from_id", err.Error())
		return
	}
	uid := mustUserID(r)

	// Both pages: fetch + ownership check via project ownership.
	canonical, err := s.Wiki.GetPage(r.Context(), canonicalID)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "canonical")
		return
	}
	if !s.ownsProject(w, r, canonical.ProjectID) {
		return
	}
	duplicate, err := s.Wiki.GetPage(r.Context(), duplicateID)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "duplicate")
		return
	}
	if duplicate.ProjectID != canonical.ProjectID {
		writeErr(w, http.StatusBadRequest, "cross_project_merge",
			"both pages must live in the same project")
		return
	}

	if err := s.Wiki.MergePages(r.Context(),
		canonicalID, duplicateID, uid.String()); err != nil {
		writeErr(w, http.StatusInternalServerError, "merge_failed", err.Error())
		return
	}

	// Best-effort: auto-resolve any open dedup review for this pair.
	// Failure here is logged but doesn't fail the merge — the merge
	// itself is the source of truth; the review queue is just UX.
	dedupKey := DedupKeyForPair(canonicalID, duplicateID)
	if it := s.findReviewByKey(r.Context(), dedupKey); it != nil &&
		it.Status == StatusOpen {
		if rerr := s.Store.SetStatus(r.Context(), it.ID, StatusResolved); rerr != nil {
			s.Logger.Warn("merge: auto-resolve review failed",
				"review_id", it.ID, "err", rerr)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"canonical_id": canonicalID.String(),
		"duplicate_id": duplicateID.String(),
		"merged":       true,
	})
}

// findReviewByKey is best-effort lookup; nil on any error since the
// merge happy-path already succeeded. Nil-safe on missing Store so
// tests can drive the auth/validation paths without setting one up.
func (s *Server) findReviewByKey(ctx context.Context, key string) *Item {
	if s.Store == nil {
		return nil
	}
	id, err := s.Store.IDByDedupeKey(ctx, key)
	if err != nil || id == uuid.Nil {
		return nil
	}
	it, err := s.Store.Get(ctx, id)
	if err != nil {
		return nil
	}
	return it
}

// ─── Scan (on-demand lint trigger) ─────────────────────────────

// handleScan replaces the deleted wiki/lint /lint/run + /lint/semantic
// pair. Body {family: "structural"|"semantic"} (defaults structural).
// structural runs synchronously via LintWorker.ScanProject and returns
// the count of newly-created findings; semantic fires a background
// goroutine and returns 202 immediately. nil Lint/Semantic ⇒ 503.
func (s *Server) handleScan(w http.ResponseWriter, r *http.Request) {
	pid, err := uuid.Parse(r.PathValue("pid"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_project_id", "")
		return
	}
	if !s.ownsProject(w, r, pid) {
		return
	}
	var req scanReq
	_ = json.NewDecoder(r.Body).Decode(&req)
	family := strings.ToLower(strings.TrimSpace(req.Family))
	if family == "" {
		family = "structural"
	}
	uid := mustUserID(r)
	switch family {
	case "semantic":
		if s.Semantic == nil {
			writeErr(w, http.StatusServiceUnavailable, "semantic_unavailable",
				"语义 lint 未启用（MODEL_RELAY_URL / JWT_SECRET 未配置）")
			return
		}
		go func() {
			if err := s.Semantic.Run(context.Background(), pid, uid); err != nil {
				s.Logger.Warn("semantic lint scan failed",
					"project_id", pid, "err", err)
			}
		}()
		writeJSON(w, http.StatusAccepted, map[string]any{
			"queued": true,
			"kind":   "semantic",
			"note":   "语义 lint 后台处理中，完成后在「审查」队列查看",
		})
	default: // structural
		if s.Lint == nil {
			writeErr(w, http.StatusServiceUnavailable, "structural_unavailable",
				"结构 lint 未启用")
			return
		}
		added, _ := s.Lint.ScanProject(r.Context(), pid, uid)
		writeJSON(w, http.StatusOK, map[string]any{
			"kind":           "structural",
			"findings_added": added,
		})
	}
}

type scanReq struct {
	Family string `json:"family"`
}

// ─── Auth helpers ──────────────────────────────────────────────

func (s *Server) ownsProject(w http.ResponseWriter, r *http.Request, pid uuid.UUID) bool {
	uid := mustUserID(r)
	proj, err := s.Wiki.GetProject(r.Context(), pid)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "project")
		return false
	}
	if proj.OwnerID != uid {
		writeErr(w, http.StatusForbidden, "forbidden", "")
		return false
	}
	return true
}

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
