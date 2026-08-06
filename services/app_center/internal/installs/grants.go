// Per-agent App grants (app_center.agent_apps CRUD).
//
// The default-agent grant on install (§21#7) covers the common case;
// these functions are the API for everything else: granting an app
// to a non-default agent, revoking, and listing the grants on an
// install (for the Settings → Agent → "which apps can I use?" page).

package installs

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/biumind/biumind/services/app_center/internal/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// AgentGrant is one row in app_center.agent_apps.
type AgentGrant struct {
	AgentID   uuid.UUID
	InstallID string
	Enabled   bool
	AddedAt   time.Time
}

// GrantAgent gives an agent access to an installation. Idempotent
// (re-granting an already-enabled grant is a no-op). Calls Authz
// with action=app:grant_agent on the Installation resource so the
// central policy can refuse cross-user grants etc.
//
// The events row is written inside the same transaction as the
// agent_apps INSERT so audit + state stay consistent.
func (in *Installer) GrantAgent(ctx context.Context, installID string, agentID uuid.UUID, callerUserID, callerOrgID string, callerRoles []string) (*AgentGrant, error) {
	row, err := in.Get(ctx, installID)
	if err != nil {
		return nil, err
	}

	dec, err := in.Authz.Check(ctx, DecideRequest{
		Principal: Entity{Type: "User", ID: callerUserID,
			Attributes: map[string]any{
				"id":     callerUserID,
				"org_id": callerOrgID,
				"roles":  toAnySlice(callerRoles),
			}},
		Action:   "app:grant_agent",
		Resource: row.cedarEntity(),
	})
	if err != nil {
		return nil, fmt.Errorf("grant: authz: %w", err)
	}
	if dec.Decision != "ALLOW" {
		return nil, fmt.Errorf("%w: %s", ErrPermissionDenied, dec.Reason)
	}

	tx, err := in.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("grant: begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	now := time.Now().UTC()
	_, err = tx.Exec(ctx, `
		INSERT INTO app_center.agent_apps (agent_id, install_id, enabled, added_at)
		VALUES ($1, $2, true, $3)
		ON CONFLICT (agent_id, install_id) DO UPDATE
		   SET enabled = true
	`, agentID, installID, now)
	if err != nil {
		return nil, fmt.Errorf("grant: upsert: %w", err)
	}

	if err := events.Write(ctx, tx, events.Event{
		ScopeKind: "install",
		ScopeID:   installID,
		ActorType: events.ActorUser,
		ActorID:   callerUserID,
		Type:      events.AppPermissionsChanged,
		Payload: map[string]any{
			"action":     "agent_granted",
			"agent_id":   agentID.String(),
			"identifier": row.Identifier,
		},
	}); err != nil {
		return nil, fmt.Errorf("grant: events: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("grant: commit: %w", err)
	}
	return &AgentGrant{AgentID: agentID, InstallID: installID, Enabled: true, AddedAt: now}, nil
}

// RevokeAgent removes (or disables) an agent's grant. We DELETE
// rather than soft-disable because the grant is per-user-explicit
// and a "deleted" grant with enabled=false would still surface in
// the UI as "this agent has access but it's off" — confusing.
// Hard delete keeps the model simple.
func (in *Installer) RevokeAgent(ctx context.Context, installID string, agentID uuid.UUID, callerUserID, callerOrgID string, callerRoles []string) error {
	row, err := in.Get(ctx, installID)
	if err != nil {
		return err
	}

	dec, err := in.Authz.Check(ctx, DecideRequest{
		Principal: Entity{Type: "User", ID: callerUserID,
			Attributes: map[string]any{
				"id":     callerUserID,
				"org_id": callerOrgID,
				"roles":  toAnySlice(callerRoles),
			}},
		Action:   "app:revoke_agent",
		Resource: row.cedarEntity(),
	})
	if err != nil {
		return fmt.Errorf("revoke: authz: %w", err)
	}
	if dec.Decision != "ALLOW" {
		return fmt.Errorf("%w: %s", ErrPermissionDenied, dec.Reason)
	}

	tx, err := in.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("revoke: begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	tag, err := tx.Exec(ctx, `
		DELETE FROM app_center.agent_apps
		 WHERE agent_id = $1 AND install_id = $2
	`, agentID, installID)
	if err != nil {
		return fmt.Errorf("revoke: delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// Already not granted — silent ok (idempotent).
		return tx.Commit(ctx)
	}

	if err := events.Write(ctx, tx, events.Event{
		ScopeKind: "install",
		ScopeID:   installID,
		ActorType: events.ActorUser,
		ActorID:   callerUserID,
		Type:      events.AppPermissionsChanged,
		Payload: map[string]any{
			"action":     "agent_revoked",
			"agent_id":   agentID.String(),
			"identifier": row.Identifier,
		},
	}); err != nil {
		return fmt.Errorf("revoke: events: %w", err)
	}
	return tx.Commit(ctx)
}

// ListAgentGrants returns the agent_apps rows for one installation.
// Used by Settings → App detail → "Granted to" panel.
func (in *Installer) ListAgentGrants(ctx context.Context, installID string) ([]AgentGrant, error) {
	rows, err := in.Pool.Query(ctx, `
		SELECT agent_id, install_id, enabled, added_at
		  FROM app_center.agent_apps
		 WHERE install_id = $1
		 ORDER BY added_at
	`, installID)
	if err != nil {
		return nil, fmt.Errorf("list grants: %w", err)
	}
	defer rows.Close()

	var out []AgentGrant
	for rows.Next() {
		var g AgentGrant
		if err := rows.Scan(&g.AgentID, &g.InstallID, &g.Enabled, &g.AddedAt); err != nil {
			return nil, fmt.Errorf("list grants scan: %w", err)
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// IsGranted is a fast path: does this agent currently have a grant
// for this install? Used by Runtime apptools.LoadForAgent indirectly
// (the loader's SQL covers this); exposed here for the rare case of
// a single-pair check without a full enumeration.
func (in *Installer) IsGranted(ctx context.Context, installID string, agentID uuid.UUID) (bool, error) {
	var enabled bool
	err := in.Pool.QueryRow(ctx, `
		SELECT enabled FROM app_center.agent_apps
		 WHERE install_id = $1 AND agent_id = $2
	`, installID, agentID).Scan(&enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("is_granted: %w", err)
	}
	return enabled, nil
}
