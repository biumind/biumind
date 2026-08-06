// Activity Feed — user-facing event stream.
//
//	GET /v1/identity/me/activity?before=<rfc3339>&limit=<n>
//
// Returns the calling user's feed (audience_user_id = self), newest first,
// with a cursor for the next page. Emitters are in-process: handlers in
// this package call s.EmitActivity after the side-effect they describe
// succeeds (PAT create/revoke, profile changes, etc.). External services
// will get a /v1/internal/activity/event endpoint when the next emitter
// (skill install in runtime) lands — until then, identity is the only
// producer and the in-process path is enough.
package api

import (
	"context"
	"net/http"
	"strconv"
	"time"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/biumind/biumind/services/identity/internal/store"
	"github.com/google/uuid"
)

// MountActivity registers the user-facing activity routes.
func (s *Server) MountActivity(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/identity/me/activity", s.requireAuth(s.handleListActivity))
}

type activityEventOut struct {
	ID         string         `json:"id"`
	ActorID    string         `json:"actor_id"`
	Kind       string         `json:"kind"`
	TargetType string         `json:"target_type,omitempty"`
	TargetID   string         `json:"target_id,omitempty"`
	Summary    string         `json:"summary"`
	Detail     map[string]any `json:"detail,omitempty"`
	CreatedAt  string         `json:"created_at"`
}

func (s *Server) handleListActivity(w http.ResponseWriter, r *http.Request) {
	c := bauth.MustClaims(r.Context())
	userID, err := uuid.Parse(c.UserID)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "bad_user", "")
		return
	}
	q := r.URL.Query()
	before := time.Time{}
	if v := q.Get("before"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			before = t
		}
	}
	limit := 50
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	rows, err := s.Store.ListActivityEventsByUser(r.Context(), userID, before, limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	out := make([]activityEventOut, 0, len(rows))
	for _, e := range rows {
		out = append(out, activityEventOut{
			ID:         e.ID.String(),
			ActorID:    e.ActorID.String(),
			Kind:       e.Kind,
			TargetType: e.TargetType,
			TargetID:   e.TargetID,
			Summary:    e.Summary,
			Detail:     e.Detail,
			CreatedAt:  e.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	var next string
	if len(rows) == limit && len(rows) > 0 {
		next = rows[len(rows)-1].CreatedAt.UTC().Format(time.RFC3339)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"events": out,
		"next":   next,
	})
}

// EmitActivity is the in-process emitter. Errors are logged but never
// surfaced — activity logging is best-effort and must not fail the
// underlying user action.
func (s *Server) EmitActivity(ctx context.Context, in store.CreateActivityEventInput) {
	if s.Store == nil {
		return
	}
	if _, err := s.Store.CreateActivityEvent(ctx, in); err != nil {
		if s.Logger != nil {
			s.Logger.Warn("activity emit failed", "kind", in.Kind, "err", err.Error())
		}
	}
}
