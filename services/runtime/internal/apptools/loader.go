// Package apptools is the bridge between App Center installations
// and the Runtime Agent's tool registry.
//
// Each App declares actions in its manifest. When a user installs an
// App and grants it to one of their agents, those actions become
// callable tools on that agent's tool fleet, namespaced as
// `<identifier>.<action>`.
//
// At Run() time the agent calls LoadForAgent to materialise the set
// of (install, manifest, granted_actions) tuples for the current
// (user, agent) pair, then RegisterTools clones each action into a
// Tool with the closure that bottoms out in biuapp.Registry.Invoke.
//
// This package OWNS no state — it is a stateless query + bind layer.
// The DB is the source of truth for "what's installed and granted";
// the in-process biuapp.Registry is the source of truth for "what's
// the App's actual code". Both must be consistent (M3.2 wires the
// runtime daemon to register the same 5 bundled apps app_center has).

package apptools

import (
	"context"
	"errors"
	"fmt"

	"github.com/biumind/biumind/packages/go-sdk/biu/biuapp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Loader runs the SQL queries to resolve installed+granted apps for
// a given (user, agent) pair. It needs only a pgx pool plus the
// biuapp.Registry that holds the actual App objects.
type Loader struct {
	Pool     *pgxpool.Pool
	Registry *biuapp.Registry
}

// LoadInput captures the per-run identity context. All fields are
// required: missing user_id/agent_id means we can't safely scope
// installs (better to surface zero apps than to leak someone else's).
type LoadInput struct {
	UserID  uuid.UUID
	OrgID   string // optional; when present, org-scope installs are also loaded
	AgentID uuid.UUID
}

// LoadedApp is one (install, manifest) pair the loader resolved.
// AvailableActions is the manifest.actions[] passed straight through
// — the Runtime tool factory uses it to build per-action Tool entries.
type LoadedApp struct {
	InstallID  string
	Identifier string
	Version    string
	Scope      string // "user" | "org"
	ScopeID    string
	Manifest   biuapp.Manifest
	// AvailableActions is a copy of Manifest.Actions, kept here so
	// callers iterate without re-extracting.
	AvailableActions []biuapp.ActionSpec
}

// Loaded is the result of LoadForAgent.
type Loaded struct {
	Apps []LoadedApp
	// MissingFromRegistry — installs whose identifier isn't in the
	// in-process biuapp.Registry. Logged as a warning by the agent
	// so missing org/marketplace apps are visible in dashboards
	// without crashing the run.
	MissingFromRegistry []string
}

// LoadForAgent resolves the (user, agent) → installs intersection.
// Returns an empty Loaded when nothing is granted (agent gets the
// stock tool fleet only — read/write/edit/grep/glob/bash/...).
func (l *Loader) LoadForAgent(ctx context.Context, in LoadInput) (*Loaded, error) {
	if l == nil || l.Pool == nil || l.Registry == nil {
		return nil, errors.New("apptools: Loader not wired")
	}
	if in.UserID == uuid.Nil || in.AgentID == uuid.Nil {
		return nil, errors.New("apptools: user_id / agent_id required")
	}

	// Single SQL: join installations × agent_apps, filter on enabled
	// flags on both sides + scope (user-private OR caller's org). The
	// agent_apps row IS the grant — no row = no access. Decision
	// §21#7: install creates a default-agent row automatically;
	// other agents need explicit grants.
	rows, err := l.Pool.Query(ctx, `
		SELECT i.id, i.identifier, i.version, i.scope, i.scope_id
		  FROM app_center.installations i
		  JOIN app_center.agent_apps a ON a.install_id = i.id
		 WHERE a.agent_id = $1
		   AND a.enabled = true
		   AND i.enabled = true
		   AND (
		         (i.scope = 'user' AND i.scope_id = $2)
		         OR
		         (i.scope = 'org'  AND i.scope_id::text = $3)
		       )
		 ORDER BY i.identifier
	`, in.AgentID, in.UserID, in.OrgID)
	if err != nil {
		return nil, fmt.Errorf("apptools: query: %w", err)
	}
	defer rows.Close()

	out := &Loaded{}
	for rows.Next() {
		var la LoadedApp
		if err := rows.Scan(&la.InstallID, &la.Identifier, &la.Version,
			&la.Scope, &la.ScopeID); err != nil {
			return nil, fmt.Errorf("apptools: scan: %w", err)
		}
		// Resolve manifest from in-process Registry. If the App isn't
		// registered (e.g. an org-private app whose binary isn't in
		// this runtime build), record the miss and skip — do NOT fail
		// the load. This degrades gracefully when an org-only app
		// hasn't shipped yet.
		app, ok := l.Registry.Get(la.Identifier)
		if !ok {
			out.MissingFromRegistry = append(out.MissingFromRegistry, la.Identifier)
			continue
		}
		la.Manifest = app.Manifest()
		la.AvailableActions = la.Manifest.Actions
		out.Apps = append(out.Apps, la)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("apptools: rows iter: %w", err)
	}
	return out, nil
}

// EnsureDefaultAgentGrantTx inserts a row into agent_apps so the
// user's default agent can call the freshly installed App without
// an extra click (decision §21#7).
//
// Idempotent: ON CONFLICT does nothing — the install path can call
// this without first checking; replays of failed installs don't
// double-insert.
//
// We expose only the *Tx variant because the install path always
// runs inside a transaction; non-transactional grants (the Grant API
// landing in M3.3) write through the pool directly using the same
// SQL. Centralising both behind one helper would force a generic
// interface that adds nothing — better to inline the SQL twice and
// keep both call sites compiler-checked against pgx.
func EnsureDefaultAgentGrantTx(ctx context.Context, tx pgx.Tx, installID string, defaultAgentID uuid.UUID) error {
	if defaultAgentID == uuid.Nil {
		// No default agent configured — skip silently. The user can
		// grant manually from the App detail page.
		return nil
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO app_center.agent_apps (agent_id, install_id, enabled)
		VALUES ($1, $2, true)
		ON CONFLICT (agent_id, install_id) DO NOTHING
	`, defaultAgentID, installID)
	if err != nil {
		return fmt.Errorf("apptools: grant default agent: %w", err)
	}
	return nil
}
