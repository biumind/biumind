// biuapp.EventPublisher implementation backed by app_center.events.
//
// Bridges the public App-SDK surface (biuapp.EventPublisher) to the
// platform's internal outbox writer. Apps don't get to touch the DB
// directly; all custom events funnel through here so the schema and
// fanout stay in one place.

package events

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PgxPublisher writes CUSTOM events through the same outbox the
// install/uninstall/upgrade paths use. Construct once per process.
type PgxPublisher struct {
	Pool *pgxpool.Pool
}

func NewPgxPublisher(pool *pgxpool.Pool) *PgxPublisher { return &PgxPublisher{Pool: pool} }

// AppViewDataChanged writes one event row for an app-driven view
// invalidation. Empty viewIDs → invalidate the App's whole view set.
//
// We model "the App" as the actor (actor_type='system', actor_id=
// identifier) because OnTrigger / Invoke calls run as the app's own
// process — the user/agent who initiated the upstream call is captured
// by the surrounding invocation row.
func (p *PgxPublisher) AppViewDataChanged(ctx context.Context, installID, identifier string, viewIDs []string) error {
	if p == nil || p.Pool == nil {
		return fmt.Errorf("events: PgxPublisher not wired")
	}
	payload, err := json.Marshal(map[string]any{
		"install_id": installID,
		"identifier": identifier,
		"view_ids":   viewIDs,
	})
	if err != nil {
		return fmt.Errorf("events: marshal payload: %w", err)
	}
	return Write(ctx, p.Pool, Event{
		ScopeKind: "install",
		ScopeID:   installID,
		ActorType: ActorSystem,
		ActorID:   identifier,
		Type:      AppViewDataChanged,
		Payload:   payload,
	})
}

// SDKBridge is the thin biuapp.EventPublisher adapter. Held by the
// app_center main and handed into biuapp.Deps so in-process Apps can
// call PublishViewDataChanged without learning about Pgx.
type SDKBridge struct {
	Pub *PgxPublisher
	// Identifier resolver: turn an installID into the App identifier
	// for event payloads. We could embed identifier in the SDK call
	// instead, but Apps don't track install IDs vs. identifiers
	// themselves — the platform owns that mapping.
	IdentifierFor func(installID string) string
}

func (b *SDKBridge) PublishViewDataChanged(ctx context.Context, installID string, viewIDs ...string) error {
	if b == nil || b.Pub == nil {
		return nil // graceful degradation when service is stateless
	}
	identifier := ""
	if b.IdentifierFor != nil {
		identifier = b.IdentifierFor(installID)
	}
	return b.Pub.AppViewDataChanged(ctx, installID, identifier, viewIDs)
}
