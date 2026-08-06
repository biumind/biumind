// Activity events — user-facing feed (distinct from audit.events).
//
// Reads are scoped to a single audience (user OR workspace). Writes accept
// optional audience fields so emitters that produce both per-user and
// workspace-wide events can dual-set them.
package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// ActivityEvent is one row of identity.activity_events.
type ActivityEvent struct {
	ID                  uuid.UUID
	ActorID             uuid.UUID
	AudienceUserID      *uuid.UUID
	AudienceWorkspaceID *uuid.UUID
	Kind                string
	TargetType          string
	TargetID            string
	Summary             string
	Detail              map[string]any
	CreatedAt           time.Time
}

// CreateActivityEventInput is what emitters fill in.
type CreateActivityEventInput struct {
	ActorID             uuid.UUID
	AudienceUserID      *uuid.UUID
	AudienceWorkspaceID *uuid.UUID
	Kind                string
	TargetType          string
	TargetID            string
	Summary             string
	Detail              map[string]any
}

// CreateActivityEvent appends a row. Detail is JSON-encoded by us so callers
// can pass any map without coupling to pgx jsonb conventions.
func (s *Store) CreateActivityEvent(ctx context.Context, in CreateActivityEventInput) (*ActivityEvent, error) {
	var detailBytes []byte
	if in.Detail != nil {
		b, err := json.Marshal(in.Detail)
		if err != nil {
			return nil, err
		}
		detailBytes = b
	}
	var ev ActivityEvent
	err := s.pool.QueryRow(ctx, `
		INSERT INTO identity.activity_events
			(actor_id, audience_user_id, audience_workspace_id, kind,
			 target_type, target_id, summary, detail)
		VALUES ($1, $2, $3, $4,
		        NULLIF($5, ''), NULLIF($6, ''), $7, $8)
		RETURNING id, created_at
	`,
		in.ActorID, in.AudienceUserID, in.AudienceWorkspaceID, in.Kind,
		in.TargetType, in.TargetID, in.Summary, detailBytes,
	).Scan(&ev.ID, &ev.CreatedAt)
	if err != nil {
		return nil, err
	}
	ev.ActorID = in.ActorID
	ev.AudienceUserID = in.AudienceUserID
	ev.AudienceWorkspaceID = in.AudienceWorkspaceID
	ev.Kind = in.Kind
	ev.TargetType = in.TargetType
	ev.TargetID = in.TargetID
	ev.Summary = in.Summary
	ev.Detail = in.Detail
	return &ev, nil
}

// ListActivityEventsByUser returns the user's feed, newest first. Cursor is
// the `created_at` of the last item from the previous page (RFC3339); pass
// time.Time{} (or now) on the first call to get the most recent slice.
func (s *Store) ListActivityEventsByUser(ctx context.Context, userID uuid.UUID, before time.Time, limit int) ([]*ActivityEvent, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if before.IsZero() {
		before = time.Now().UTC().Add(time.Minute) // include "just now" rows
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, actor_id, audience_user_id, audience_workspace_id,
		       kind, COALESCE(target_type, ''), COALESCE(target_id, ''),
		       summary, COALESCE(detail::text, ''), created_at
		FROM identity.activity_events
		WHERE audience_user_id = $1 AND created_at < $2
		ORDER BY created_at DESC
		LIMIT $3
	`, userID, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ActivityEvent
	for rows.Next() {
		var e ActivityEvent
		var detailRaw string
		if err := rows.Scan(
			&e.ID, &e.ActorID, &e.AudienceUserID, &e.AudienceWorkspaceID,
			&e.Kind, &e.TargetType, &e.TargetID, &e.Summary, &detailRaw, &e.CreatedAt,
		); err != nil {
			return nil, err
		}
		if detailRaw != "" {
			_ = json.Unmarshal([]byte(detailRaw), &e.Detail)
		}
		out = append(out, &e)
	}
	return out, rows.Err()
}
