// Package events writes typed App Center mutation events to
// app_center.events.
//
// Every state-changing operation across app_center.* must call Write
// in the same transaction as the underlying mutation (invariant I4).
// The LISTEN/NOTIFY trigger and outbox poller (see migration 00001)
// both consume from this table, so a missed event = a missed Realtime
// fanout = a stale UI.
//
// We don't expose pgx directly to callers — Write takes a Tx so the
// caller controls transaction lifetime and rollback semantics.
package events

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
)

// Type is the canonical app_center event taxonomy. Adding new values
// requires updating the proto AG-UI registry (M10.2) so client-side
// dispatch stays in sync.
type Type string

const (
	// Catalogue lifecycle.
	AppPublished  Type = "app.published"
	AppDeprecated Type = "app.deprecated"
	AppSuspended  Type = "app.suspended"

	// Per-tenant install lifecycle.
	AppInstalled          Type = "app.installed"
	AppUninstalled        Type = "app.uninstalled"
	AppUpgraded           Type = "app.upgraded"
	AppPermissionsChanged Type = "app.permissions_changed"
	AppEnabledChanged     Type = "app.enabled_changed"
	AppConfigUpdated      Type = "app.config_updated"

	// Runtime activity.
	AppActionInvoked   Type = "app.action_invoked"
	AppTriggerFired    Type = "app.trigger_fired"
	AppViewDataChanged Type = "app.view_data_changed" // App-pushed view invalidation (M17)

	// Sidebar.
	SidebarLayoutChanged Type = "sidebar.layout_changed"

	// Radar — keyword-rule hits (P2). The payload carries the install
	// id (so the client knows which app's badge to bump) plus the hit
	// id and severity for in-place rendering without a refetch.
	RadarHitFired Type = "radar.hit_fired"
)

// ActorType identifies who caused the event. Stays a small closed
// enum so audit dashboards can bucket cleanly.
type ActorType string

const (
	ActorUser      ActorType = "user"
	ActorAgent     ActorType = "agent"
	ActorSystem    ActorType = "system"
	ActorAdmin     ActorType = "admin"
	ActorScheduler ActorType = "scheduler"
	ActorWebhook   ActorType = "webhook"
)

// Event is the value handed to Write. ScopeKind+ScopeID together
// build the scope string ("install:<uuid>", "user:<uuid>", etc.) so
// callers don't have to remember the format. Payload is any JSON-
// marshalable value; it gets serialised inside Write.
type Event struct {
	// Scope key = ScopeKind:ScopeID. Use known kinds:
	//   "install" → installation id
	//   "app"     → app catalogue id
	//   "user"    → user id (sidebar / non-install events)
	//   "org"     → org id
	ScopeKind string
	ScopeID   string

	ActorType ActorType
	ActorID   string

	Type    Type
	Payload any
}

// DBExecer is the minimum surface Write needs. Both *pgxpool.Pool and
// pgx.Tx satisfy it, so callers can pass whichever they hold.
type DBExecer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Write inserts one event row. Returns an error if marshaling or the
// SQL exec fails; the caller is responsible for transaction rollback
// on error (we don't roll back here so callers can keep batching).
func Write(ctx context.Context, db DBExecer, e Event) error {
	if e.ScopeKind == "" || e.ScopeID == "" {
		return fmt.Errorf("events: scope kind and id required")
	}
	if e.Type == "" {
		return fmt.Errorf("events: type required")
	}
	if e.ActorType == "" {
		return fmt.Errorf("events: actor_type required")
	}

	scope := e.ScopeKind + ":" + e.ScopeID

	pl, err := encodePayload(e.Payload)
	if err != nil {
		return fmt.Errorf("events: marshal payload: %w", err)
	}

	_, err = db.Exec(ctx, `
		INSERT INTO app_center.events
			(scope, actor_type, actor_id, event_type, payload)
		VALUES ($1, $2, $3, $4, $5)
	`, scope, string(e.ActorType), e.ActorID, string(e.Type), pl)
	if err != nil {
		return fmt.Errorf("events: insert: %w", err)
	}
	return nil
}

func encodePayload(p any) ([]byte, error) {
	if p == nil {
		return []byte(`{}`), nil
	}
	// Allow callers to pass a pre-serialised []byte / json.RawMessage
	// without us re-marshaling (avoids `[byte]` JSON encoding).
	switch v := p.(type) {
	case []byte:
		if len(v) == 0 {
			return []byte(`{}`), nil
		}
		return v, nil
	case json.RawMessage:
		if len(v) == 0 {
			return []byte(`{}`), nil
		}
		return []byte(v), nil
	}
	return json.Marshal(p)
}
