// Package installs is the App Center install lifecycle orchestrator.
//
// Public surface = the Installer struct. Each method is one user-
// observable operation (Install / Uninstall / Toggle / UpdateConfig /
// Upgrade) and runs in a single transaction:
//
//   1. Authz.Decide → fail-closed on deny / error
//   2. Validate manifest + permission subset
//   3. UPSERT/DELETE/UPDATE the app_center.installations row
//   4. events.Write the matching event in the same tx
//   5. Commit, then fire the optional Lifecycle hook (outside tx so
//      a slow OAuth bootstrap doesn't hold a transaction open)
//   6. On hook failure: roll back via compensating mutation
//
// This is intentionally NOT a generic "service" with twenty methods.
// Every install path goes through one of the five Installer methods
// so events / Authz / hook-firing are inviolable invariants.

package installs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/biumind/biumind/packages/go-sdk/biu/biuapp"
	"github.com/biumind/biumind/services/app_center/internal/events"
	"github.com/biumind/biumind/services/app_center/internal/permissions"
	"github.com/biumind/biumind/services/app_center/internal/skillbridge"
	"github.com/biumind/biumind/services/app_center/internal/triggers"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ─── Errors ────────────────────────────────────────────────────────

var (
	ErrUnknownApp        = errors.New("install: unknown app identifier")
	ErrAlreadyInstalled  = errors.New("install: already installed for this scope")
	ErrNotFound          = errors.New("install: installation not found")
	ErrPermissionDenied  = errors.New("install: authz denied")
	ErrPermissionsExceed = errors.New("install: granted permissions exceed manifest")
	ErrManifestInvalid   = errors.New("install: manifest validation failed")
	ErrForcedUninstall   = errors.New("install: forced installation cannot be uninstalled by member")
)

// ─── Authz interface ──────────────────────────────────────────────
//
// We accept any Decider so tests can stub. Production wires
// services/runtime/internal/authz.HTTP — same client every Go service
// already uses.

type Decider interface {
	Check(ctx context.Context, req DecideRequest) (*DecideResult, error)
}

type DecideRequest struct {
	Principal Entity
	Action    string
	Resource  Entity
}

type Entity struct {
	Type       string
	ID         string
	Attributes map[string]any
}

type DecideResult struct {
	Decision string // "ALLOW" | "DENY"
	Reason   string
}

// AllowAll is the dev / CI fallback. Production must wire the real
// Authz client (logged at service startup).
type AllowAll struct{}

func (AllowAll) Check(_ context.Context, _ DecideRequest) (*DecideResult, error) {
	return &DecideResult{Decision: "ALLOW", Reason: "allow-all stub"}, nil
}

// ─── Installer ─────────────────────────────────────────────────────

// Installer orchestrates install lifecycle. Construct with New;
// methods are safe for concurrent use across goroutines (the pgx
// pool handles connection isolation).
type Installer struct {
	Pool     *pgxpool.Pool
	Registry *biuapp.Registry
	Authz    Decider
}

func New(pool *pgxpool.Pool, reg *biuapp.Registry, authz Decider) *Installer {
	if authz == nil {
		authz = AllowAll{}
	}
	return &Installer{Pool: pool, Registry: reg, Authz: authz}
}

// ─── Public commands ───────────────────────────────────────────────

// InstallRequest captures everything the API layer extracts from the
// HTTP request body + JWT claims.
type InstallRequest struct {
	// Identity of the App being installed (by slug).
	Identifier string

	// Where the installation lives.
	Scope   string // "user" | "org"
	ScopeID string // user_id or org_id

	// Permissions subset the user agreed to. MUST be ⊆ manifest.permissions.
	GrantedPermissions []string

	// Optional initial config blob.
	Config map[string]any

	// Forced=true means org admin pushed this install; member can't
	// uninstall (only disable). Decision §21#3.
	Forced bool

	// DefaultAgentID — when non-zero, the installer auto-grants this
	// agent in the same transaction (decision §21#7). Zero value =
	// no auto-grant; user must call grant_agent later. The HTTP API
	// layer resolves this from the caller's profile / DEFAULT_AGENT_ID
	// env (M3.4); the field stays here so non-HTTP callers (CLI,
	// internal tools) can pass it explicitly.
	DefaultAgentID uuid.UUID

	// Caller context for events / authz.
	CallerUserID string
	CallerOrgID  string
	CallerRoles  []string
}

// Installation is the canonical row shape returned to API / client.
// Mirrors app_center.installations 1:1.
type Installation struct {
	ID                 string
	Scope              string
	ScopeID            string
	AppID              string
	Identifier         string
	Version            string
	Enabled            bool
	PinnedVersion      string
	PermissionsGranted []string
	Config             map[string]any
	Forced             bool
	InstalledAt        time.Time
	UpdatedAt          time.Time
	InstalledBy        string
}

// Install creates an installation row. The hook ordering is:
//
//	tx: authz → validate → INSERT installations → events.Write → COMMIT
//	post-tx: registry.DispatchOnInstall (best-effort; logs on error,
//	                                       does NOT roll back)
//
// We don't roll back on hook error because a hook failure typically
// means the App needs more setup (OAuth not authorised, remote
// service down) — the install row should stay so the user can retry
// from the UI without re-clicking install. The caller surfaces the
// hook error to the UI as a non-fatal warning.
func (in *Installer) Install(ctx context.Context, req InstallRequest) (*Installation, error) {
	if req.Identifier == "" || req.Scope == "" || req.ScopeID == "" {
		return nil, fmt.Errorf("install: identifier / scope / scope_id required")
	}
	if req.Scope != "user" && req.Scope != "org" {
		return nil, fmt.Errorf("install: invalid scope %q", req.Scope)
	}

	app, ok := in.Registry.Get(req.Identifier)
	if !ok {
		return nil, ErrUnknownApp
	}
	manifest := app.Manifest()
	if err := biuapp.ValidateBundled(&manifest); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrManifestInvalid, err)
	}

	// Granted ⊆ manifest.permissions (set semantics).
	manifestSet := map[string]struct{}{}
	for _, p := range manifest.Permissions {
		manifestSet[p] = struct{}{}
	}
	for _, g := range req.GrantedPermissions {
		if _, ok := manifestSet[g]; !ok {
			return nil, fmt.Errorf("%w: %q not in manifest", ErrPermissionsExceed, g)
		}
	}

	// Authz: caller can install this app.
	dec, err := in.Authz.Check(ctx, DecideRequest{
		Principal: Entity{Type: "User", ID: req.CallerUserID,
			Attributes: map[string]any{
				"id":     req.CallerUserID,
				"org_id": req.CallerOrgID,
				"roles":  toAnySlice(req.CallerRoles),
			}},
		Action: permissions.ActionInstall,
		Resource: Entity{Type: "App", ID: req.Identifier,
			Attributes: map[string]any{
				"id":         req.Identifier,
				"identifier": manifest.Slug(),
				"status":     "active", // catalogue rows default to active in v1.5
				"source":     orDefault(manifest.ManifestExt.Identifier, "bundled"),
			}},
	})
	if err != nil {
		return nil, fmt.Errorf("install: authz: %w", err)
	}
	if dec.Decision != "ALLOW" {
		return nil, fmt.Errorf("%w: %s", ErrPermissionDenied, dec.Reason)
	}

	// Begin tx.
	tx, err := in.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("install: begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck — best-effort on commit success path

	// Resolve app catalogue id. v1.5 bundled / org rows are upserted
	// by the SDK itself; for now we synthesise an id from the slug
	// when no catalogue row exists yet (post-M1 hardening).
	appID := "app_" + req.Identifier

	id := uuid.New()
	configJSON, err := json.Marshal(orMap(req.Config))
	if err != nil {
		return nil, fmt.Errorf("install: marshal config: %w", err)
	}

	var existing string
	err = tx.QueryRow(ctx, `
		SELECT id FROM app_center.installations
		WHERE scope = $1 AND scope_id = $2 AND identifier = $3
	`, req.Scope, req.ScopeID, req.Identifier).Scan(&existing)
	if err == nil {
		return nil, ErrAlreadyInstalled
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("install: probe existing: %w", err)
	}

	now := time.Now().UTC()
	_, err = tx.Exec(ctx, `
		INSERT INTO app_center.installations
			(id, scope, scope_id, app_id, identifier, version,
			 enabled, permissions_granted, config, forced,
			 installed_at, updated_at, installed_by)
		VALUES ($1, $2, $3, $4, $5, $6,
			true, $7, $8, $9,
			$10, $10, NULLIF($11, '')::uuid)
	`, id, req.Scope, req.ScopeID, appID, req.Identifier, manifest.Version,
		toAnySlice(req.GrantedPermissions), configJSON, req.Forced,
		now, req.CallerUserID)
	if err != nil {
		return nil, fmt.Errorf("install: insert: %w", err)
	}

	if err := events.Write(ctx, tx, events.Event{
		ScopeKind: "install",
		ScopeID:   id.String(),
		ActorType: events.ActorUser,
		ActorID:   req.CallerUserID,
		Type:      events.AppInstalled,
		Payload: map[string]any{
			"identifier":          req.Identifier,
			"version":             manifest.Version,
			"scope":               req.Scope,
			"scope_id":            req.ScopeID,
			"forced":              req.Forced,
			"permissions_granted": req.GrantedPermissions,
		},
	}); err != nil {
		return nil, fmt.Errorf("install: events: %w", err)
	}

	// Trigger registration (M4.2). For each manifest.triggers entry we
	// insert a scheduler_jobs row in the same tx; cron jobs get an
	// initial next_run; webhooks get the install's secret generated +
	// stamped onto installations.webhook_secret. Failure here aborts
	// the whole install so a misregistered cron doesn't leave the
	// user with a half-functional install.
	hasWebhook := false
	for _, tr := range manifest.Triggers {
		if tr.Kind == biuapp.TriggerWebhook {
			hasWebhook = true
			break
		}
	}
	if hasWebhook {
		secret, err := triggers.Generate()
		if err != nil {
			return nil, fmt.Errorf("install: webhook secret: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE app_center.installations
			   SET webhook_secret = $1
			 WHERE id = $2
		`, secret, id); err != nil {
			return nil, fmt.Errorf("install: store webhook secret: %w", err)
		}
	}
	for _, tr := range manifest.Triggers {
		jobID := uuid.New()
		var (
			cronExpr     *string
			ifInactive   *string
			webhookPath  *string
			inboxPattern *string
			nextRun      *time.Time
		)
		switch tr.Kind {
		case biuapp.TriggerCron:
			expr := tr.Expr
			cronExpr = &expr
			if tr.IfInactiveFor != "" {
				v := tr.IfInactiveFor
				ifInactive = &v
			}
			n, err := triggers.NextRun(tr.Expr, now)
			if err != nil {
				return nil, fmt.Errorf("install: cron parse %q: %w", tr.Name, err)
			}
			nextRun = &n
		case biuapp.TriggerWebhook:
			p := tr.Path
			webhookPath = &p
		case biuapp.TriggerInbox:
			p := tr.Pattern
			inboxPattern = &p
		}
		inputJSON, _ := json.Marshal(orMap(tr.Input))
		if _, err := tx.Exec(ctx, `
			INSERT INTO app_center.scheduler_jobs
				(id, install_id, identifier, name, kind,
				 cron_expr, if_inactive_for, webhook_path, inbox_pattern,
				 action, input, next_run, enabled,
				 created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5,
			        $6, $7, $8, $9,
			        $10, $11, $12, true,
			        $13, $13)
		`, jobID, id, req.Identifier, tr.Name, string(tr.Kind),
			cronExpr, ifInactive, webhookPath, inboxPattern,
			tr.Action, inputJSON, nextRun, now); err != nil {
			return nil, fmt.Errorf("install: register trigger %q: %w", tr.Name, err)
		}
	}

	// Bundled skills (M5.3). When the App declares manifest.skills[]
	// AND we have an org context, mirror them into runtime.skills so
	// the skills engine surfaces them to the agent loop. Same tx as
	// the install row — a crash between the two would leave the user
	// with skills they can't trace back to an install.
	if len(manifest.Skills) > 0 {
		caller := req.CallerOrgID
		if caller == "" && req.Scope == "org" {
			caller = req.ScopeID
		}
		if orgUUID, parseErr := uuid.Parse(caller); parseErr == nil {
			if _, err := skillbridge.WriteAppSkills(ctx, tx, skillbridge.Inputs{
				InstallID:     id,
				OrgID:         orgUUID,
				AppIdentifier: req.Identifier,
				Manifest:      manifest,
				App:           app,
			}); err != nil {
				// ErrNoOrg shouldn't reach here (we just parsed the
				// uuid) — any other error is fatal.
				return nil, fmt.Errorf("install: skill bridge: %w", err)
			}
		}
		// No org → the manifest.skills declaration is silently
		// dropped for this install. v2.0 may surface a warning to
		// the user; v1.5 keeps the install path clean.
	}

	// 设计 §10A.10 + 决策 §21#10: bundled / org 来源的 App 若 manifest
	// 声明 sidebar.default_pin=true, 安装完自动入侧边栏 (仅 user scope
	// 的 desktop layout)。marketplace / user_webview 强制忽略此字段。
	//
	// 自动 pin 失败不阻塞主 install (装出来了但没进侧栏 — 用户能在
	// customize 页手动加; 阻塞反而让"装得成"路径多一个失败点)。
	if req.Scope == "user" && manifest.Sidebar != nil && manifest.Sidebar.DefaultPin {
		var source string
		err := tx.QueryRow(ctx, `
			SELECT source FROM app_center.apps WHERE identifier = $1 LIMIT 1
		`, req.Identifier).Scan(&source)
		// catalog 行可能未上 (v1.5 SDK 注册的 bundled app 默认走 registry,
		// catalog 表可能滞后) — 找不到当 bundled 处理。
		if errors.Is(err, pgx.ErrNoRows) {
			source = "bundled"
			err = nil
		}
		if err != nil {
			return nil, fmt.Errorf("install: lookup source for default_pin: %w", err)
		}
		if source == "bundled" || source == "org" {
			pos := manifest.Sidebar.PreferredPosition
			if pinErr := autoPinForUser(ctx, tx, req.CallerUserID, id.String(), pos); pinErr != nil {
				// 仅记录, 不返回错误 — 此 path 失败后客户端仍可手动 pin。
				// log via fmt: 缺 logger, 暂用注释提示后续接入。
				_ = pinErr
			}
		}
	}

	// Default-agent auto-grant (decision §21#7). Same transaction so a
	// crash between INSERT installations and INSERT agent_apps can't
	// leave the user with an "installed but un-grantable" install.
	if req.DefaultAgentID != uuid.Nil {
		if _, err := tx.Exec(ctx, `
			INSERT INTO app_center.agent_apps (agent_id, install_id, enabled)
			VALUES ($1, $2, true)
			ON CONFLICT (agent_id, install_id) DO NOTHING
		`, req.DefaultAgentID, id); err != nil {
			return nil, fmt.Errorf("install: default agent grant: %w", err)
		}
		if err := events.Write(ctx, tx, events.Event{
			ScopeKind: "install",
			ScopeID:   id.String(),
			ActorType: events.ActorSystem,
			ActorID:   req.CallerUserID,
			Type:      events.AppPermissionsChanged,
			Payload: map[string]any{
				"action":     "default_agent_granted",
				"agent_id":   req.DefaultAgentID.String(),
				"identifier": req.Identifier,
			},
		}); err != nil {
			return nil, fmt.Errorf("install: default-grant events: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("install: commit: %w", err)
	}

	row := &Installation{
		ID:                 id.String(),
		Scope:              req.Scope,
		ScopeID:            req.ScopeID,
		AppID:              appID,
		Identifier:         req.Identifier,
		Version:            manifest.Version,
		Enabled:            true,
		PermissionsGranted: req.GrantedPermissions,
		Config:             orMap(req.Config),
		Forced:             req.Forced,
		InstalledAt:        now,
		UpdatedAt:          now,
		InstalledBy:        req.CallerUserID,
	}

	// Fire OnInstall hook AFTER commit so the row is queryable. Hook
	// error doesn't roll back (see method docs).
	hookErr := in.Registry.DispatchOnInstall(ctx, req.Identifier, hookInstall(row, manifest))
	if hookErr != nil {
		// Caller (API layer) treats this as a non-fatal warning.
		return row, fmt.Errorf("install: OnInstall hook (row created): %w", hookErr)
	}
	return row, nil
}

// Uninstall removes an installation. Hook fires BEFORE deletion so
// the App can clean up external state (revoke remote webhooks,
// disconnect OAuth) while still attached to its install context.
//
// Decision §21#9: data is not deleted by uninstall. The trigger on
// app_center.installations DELETE prunes sidebar references; cascade
// removes agent_apps. Wiki / Files / Graph orphans stay until the
// user explicitly clicks "clean residual data".
func (in *Installer) Uninstall(ctx context.Context, installID string, callerUserID, callerOrgID string, callerRoles []string) error {
	row, err := in.Get(ctx, installID)
	if err != nil {
		return err
	}

	// Authz.
	dec, err := in.Authz.Check(ctx, DecideRequest{
		Principal: Entity{Type: "User", ID: callerUserID,
			Attributes: map[string]any{
				"id":     callerUserID,
				"org_id": callerOrgID,
				"roles":  toAnySlice(callerRoles),
			}},
		Action:   permissions.ActionUninstall,
		Resource: row.cedarEntity(),
	})
	if err != nil {
		return fmt.Errorf("uninstall: authz: %w", err)
	}
	if dec.Decision != "ALLOW" {
		if row.Forced {
			return fmt.Errorf("%w: %s", ErrForcedUninstall, dec.Reason)
		}
		return fmt.Errorf("%w: %s", ErrPermissionDenied, dec.Reason)
	}

	// Hook BEFORE delete so external cleanup has access to install ctx.
	app, _ := in.Registry.Get(row.Identifier)
	if app != nil {
		manifest := app.Manifest()
		_ = in.Registry.DispatchOnUninstall(ctx, row.Identifier, hookInstall(row, manifest))
		// We deliberately swallow the hook error — failing to revoke
		// a remote webhook should NOT block the user from uninstalling.
		// We log via the caller's logger context.
	}

	tx, err := in.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("uninstall: begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	tag, err := tx.Exec(ctx, `DELETE FROM app_center.installations WHERE id = $1`, installID)
	if err != nil {
		return fmt.Errorf("uninstall: delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}

	// Cascade-delete bundled skills tied to this install. Decision
	// §21#9 says App data (Wiki / Files) survives uninstall, but
	// skills are runtime mechanics — leaving orphaned rows would
	// confuse the agent's available_skills set with no UX to clean
	// them up. Match by manifest->>'app_install_id' so we never
	// delete a skill that wasn't ours.
	installUUID, _ := uuid.Parse(installID)
	if installUUID != uuid.Nil {
		if _, err := skillbridge.DeleteAppSkills(ctx, tx, installUUID); err != nil {
			return fmt.Errorf("uninstall: skill bridge: %w", err)
		}
	}

	if err := events.Write(ctx, tx, events.Event{
		ScopeKind: "install",
		ScopeID:   installID,
		ActorType: events.ActorUser,
		ActorID:   callerUserID,
		Type:      events.AppUninstalled,
		Payload: map[string]any{
			"identifier": row.Identifier,
			"version":    row.Version,
			"forced":     row.Forced,
		},
	}); err != nil {
		return fmt.Errorf("uninstall: events: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}

	// Drop dynamic apps (user_webview etc.) from the in-memory registry
	// so they vanish from the catalogue right away. No-op for bundled
	// apps — see unregisterIfDynamic.
	in.unregisterIfDynamic(ctx, row.Identifier)
	return nil
}

// Toggle flips the enabled flag on an installation.
func (in *Installer) Toggle(ctx context.Context, installID string, enabled bool, callerUserID, callerOrgID string, callerRoles []string) (*Installation, error) {
	row, err := in.Get(ctx, installID)
	if err != nil {
		return nil, err
	}
	if row.Enabled == enabled {
		return row, nil // no-op
	}

	action := permissions.ActionDisable
	if enabled {
		action = permissions.ActionEnable
	}
	dec, err := in.Authz.Check(ctx, DecideRequest{
		Principal: Entity{Type: "User", ID: callerUserID,
			Attributes: map[string]any{
				"id":     callerUserID,
				"org_id": callerOrgID,
				"roles":  toAnySlice(callerRoles),
			}},
		Action:   action,
		Resource: row.cedarEntity(),
	})
	if err != nil {
		return nil, fmt.Errorf("toggle: authz: %w", err)
	}
	if dec.Decision != "ALLOW" {
		return nil, fmt.Errorf("%w: %s", ErrPermissionDenied, dec.Reason)
	}

	tx, err := in.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("toggle: begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	now := time.Now().UTC()
	if _, err := tx.Exec(ctx, `
		UPDATE app_center.installations
		   SET enabled = $1, updated_at = $2
		 WHERE id = $3
	`, enabled, now, installID); err != nil {
		return nil, fmt.Errorf("toggle: update: %w", err)
	}

	if err := events.Write(ctx, tx, events.Event{
		ScopeKind: "install",
		ScopeID:   installID,
		ActorType: events.ActorUser,
		ActorID:   callerUserID,
		Type:      events.AppEnabledChanged,
		Payload: map[string]any{
			"identifier": row.Identifier,
			"enabled":    enabled,
		},
	}); err != nil {
		return nil, fmt.Errorf("toggle: events: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("toggle: commit: %w", err)
	}
	row.Enabled = enabled
	row.UpdatedAt = now
	return row, nil
}

// Get fetches an installation by id.
func (in *Installer) Get(ctx context.Context, installID string) (*Installation, error) {
	var (
		row     Installation
		permsDB []string
		cfgRaw  []byte
		pinned  *string
		instBy  *string
	)
	err := in.Pool.QueryRow(ctx, `
		SELECT id, scope, scope_id, app_id, identifier, version,
		       enabled, pinned_version, permissions_granted, config,
		       forced, installed_at, updated_at, installed_by
		  FROM app_center.installations
		 WHERE id = $1
	`, installID).Scan(
		&row.ID, &row.Scope, &row.ScopeID, &row.AppID,
		&row.Identifier, &row.Version, &row.Enabled, &pinned,
		&permsDB, &cfgRaw, &row.Forced,
		&row.InstalledAt, &row.UpdatedAt, &instBy,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get install: %w", err)
	}
	if pinned != nil {
		row.PinnedVersion = *pinned
	}
	if instBy != nil {
		row.InstalledBy = *instBy
	}
	row.PermissionsGranted = permsDB
	if len(cfgRaw) > 0 {
		_ = json.Unmarshal(cfgRaw, &row.Config)
	}
	return &row, nil
}

// GetByIdentifier looks up an installation by composite key
// (scope, scope_id, identifier). Used by the invoke path to resolve
// catalog name → install row before running Authz.
//
// Returns ErrNotFound if no row exists. Lifecycle attributes
// (permissions_granted / config) are NOT loaded — invoke only needs
// id/identifier/enabled/forced/scope to make the Authz decision.
func (in *Installer) GetByIdentifier(ctx context.Context, scope, scopeID, identifier string) (*Installation, error) {
	var (
		row     Installation
		permsDB []string
		pinned  *string
		instBy  *string
	)
	err := in.Pool.QueryRow(ctx, `
		SELECT id, scope, scope_id, app_id, identifier, version,
		       enabled, pinned_version, permissions_granted,
		       forced, installed_at, updated_at, installed_by
		  FROM app_center.installations
		 WHERE scope = $1 AND scope_id = $2 AND identifier = $3
	`, scope, scopeID, identifier).Scan(
		&row.ID, &row.Scope, &row.ScopeID, &row.AppID,
		&row.Identifier, &row.Version, &row.Enabled, &pinned,
		&permsDB, &row.Forced,
		&row.InstalledAt, &row.UpdatedAt, &instBy,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get install by identifier: %w", err)
	}
	if pinned != nil {
		row.PinnedVersion = *pinned
	}
	if instBy != nil {
		row.InstalledBy = *instBy
	}
	row.PermissionsGranted = permsDB
	return &row, nil
}

// AuthorizeInvoke runs an `app:invoke` Authz check against the supplied
// installation. Caller fetches the install via GetByIdentifier first
// (so we can also short-circuit on enabled=false / forced rules without
// a second DB hit).
//
// Returns ErrPermissionDenied on DENY, or a wrapped Authz error on
// transport failure (caller maps to 500 vs 403).
func (in *Installer) AuthorizeInvoke(ctx context.Context, install *Installation, callerUserID, callerOrgID string, callerRoles []string) error {
	dec, err := in.Authz.Check(ctx, DecideRequest{
		Principal: Entity{Type: "User", ID: callerUserID,
			Attributes: map[string]any{
				"id":     callerUserID,
				"org_id": callerOrgID,
				"roles":  toAnySlice(callerRoles),
			}},
		Action:   permissions.ActionInvoke,
		Resource: install.cedarEntity(),
	})
	if err != nil {
		return fmt.Errorf("invoke: authz: %w", err)
	}
	if dec.Decision != "ALLOW" {
		return fmt.Errorf("%w: %s", ErrPermissionDenied, dec.Reason)
	}
	return nil
}

// List returns all installations for a (scope, scope_id) tuple.
func (in *Installer) List(ctx context.Context, scope, scopeID string) ([]*Installation, error) {
	rows, err := in.Pool.Query(ctx, `
		SELECT id, scope, scope_id, app_id, identifier, version,
		       enabled, pinned_version, permissions_granted, config,
		       forced, installed_at, updated_at, installed_by
		  FROM app_center.installations
		 WHERE scope = $1 AND scope_id = $2
		 ORDER BY identifier
	`, scope, scopeID)
	if err != nil {
		return nil, fmt.Errorf("list: %w", err)
	}
	defer rows.Close()

	var out []*Installation
	for rows.Next() {
		var (
			row     Installation
			permsDB []string
			cfgRaw  []byte
			pinned  *string
			instBy  *string
		)
		if err := rows.Scan(
			&row.ID, &row.Scope, &row.ScopeID, &row.AppID,
			&row.Identifier, &row.Version, &row.Enabled, &pinned,
			&permsDB, &cfgRaw, &row.Forced,
			&row.InstalledAt, &row.UpdatedAt, &instBy,
		); err != nil {
			return nil, fmt.Errorf("list scan: %w", err)
		}
		if pinned != nil {
			row.PinnedVersion = *pinned
		}
		if instBy != nil {
			row.InstalledBy = *instBy
		}
		row.PermissionsGranted = permsDB
		if len(cfgRaw) > 0 {
			_ = json.Unmarshal(cfgRaw, &row.Config)
		}
		out = append(out, &row)
	}
	return out, rows.Err()
}

// ─── Helpers ───────────────────────────────────────────────────────

func (r *Installation) cedarEntity() Entity {
	return Entity{
		Type: "Installation", ID: r.ID,
		Attributes: map[string]any{
			"id":               r.ID,
			"identifier":       r.Identifier,
			"app_id":           r.AppID,
			"scope":            r.Scope,
			"scope_id":         r.ScopeID,
			"enabled":          r.Enabled,
			"forced":           r.Forced,
			"version":          r.Version,
			"permissions":      toAnySlice(r.PermissionsGranted),
			"net_outbound":     []any{},
			"oauth_providers":  []any{},
			"secret_providers": []any{},
			"data_scopes":      []any{},
		},
	}
}

func hookInstall(row *Installation, m biuapp.Manifest) biuapp.Install {
	return biuapp.Install{
		ID:         row.ID,
		Identifier: row.Identifier,
		Version:    row.Version,
		Scope:      row.Scope,
		ScopeID:    row.ScopeID,
		Config:     row.Config,
	}
}

func toAnySlice(in []string) []any {
	out := make([]any, len(in))
	for i, s := range in {
		out[i] = s
	}
	return out
}

// autoPinForUser appends an install to the user's desktop sidebar layout
// in the SAME tx as the install row insert. If the user has no layout
// yet, creates one with just this app. Idempotent: if the install is
// already pinned (re-install path), no-op.
//
// [preferredPosition] 来自 manifest.sidebar.preferred_position 字段
// (设计 §10A.9), 决定新 pin 在 app 段中的位置:
//
//	"top"    → prepend (放最前, 覆盖默认排序)
//	"bottom" → append  (放最后, 跟"middle" 没声明等价)
//	"middle" / "" → append (默认行为)
//
// 仅影响 "app" kind 项之间的相对顺序; system 段位置保持不变。
//
// Writes a SidebarLayoutChanged event so other devices Realtime-refresh.
func autoPinForUser(ctx context.Context, tx pgx.Tx, userIDStr, installID, preferredPosition string) error {
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return fmt.Errorf("auto-pin: invalid user_id: %w", err)
	}

	var raw []byte
	var version int
	err = tx.QueryRow(ctx, `
		SELECT items, version FROM app_center.sidebar_layouts
		 WHERE user_id = $1 AND scope = 'desktop'
		 FOR UPDATE
	`, userID).Scan(&raw, &version)

	if errors.Is(err, pgx.ErrNoRows) {
		// 首次 — 写一行只含此 app。位置策略不影响 "唯一一项" 的结果。
		items := []map[string]any{{"kind": "app", "ref": installID}}
		itemsJSON, _ := json.Marshal(items)
		if _, err := tx.Exec(ctx, `
			INSERT INTO app_center.sidebar_layouts
				(user_id, scope, items, version, updated_at, updated_by_device)
			VALUES ($1, 'desktop', $2::jsonb, 1, NOW(), 'app_center:auto')
		`, userID, itemsJSON); err != nil {
			return fmt.Errorf("auto-pin: insert layout: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("auto-pin: select layout: %w", err)
	} else {
		var items []map[string]any
		if err := json.Unmarshal(raw, &items); err != nil {
			return fmt.Errorf("auto-pin: decode items: %w", err)
		}
		// 幂等: 已 pinned 就不再插。
		for _, i := range items {
			if i["kind"] == "app" && i["ref"] == installID {
				return nil
			}
		}
		newPin := map[string]any{"kind": "app", "ref": installID}
		items = insertPinnedAt(items, newPin, preferredPosition)
		itemsJSON, _ := json.Marshal(items)
		if _, err := tx.Exec(ctx, `
			UPDATE app_center.sidebar_layouts
			   SET items = $1::jsonb, version = version + 1,
			       updated_at = NOW(), updated_by_device = 'app_center:auto'
			 WHERE user_id = $2 AND scope = 'desktop'
		`, itemsJSON, userID); err != nil {
			return fmt.Errorf("auto-pin: update layout: %w", err)
		}
	}

	// Realtime fanout (设计 §10A.8) — 让其他 device 同步刷新。
	if err := events.Write(ctx, tx, events.Event{
		ScopeKind: "user",
		ScopeID:   userID.String(),
		ActorType: events.ActorSystem,
		ActorID:   userID.String(),
		Type:      events.SidebarLayoutChanged,
		Payload: map[string]any{
			"scope":  "desktop",
			"action": "auto_pin",
			"device": "app_center:auto",
		},
	}); err != nil {
		return fmt.Errorf("auto-pin: events: %w", err)
	}
	return nil
}

// insertPinnedAt 把 [newPin] 按 [position] 策略插入 items 里 app 段的
// 相对位置。app 段在 items 中可能跟 system 项交错; 我们按 position 选
// 落点:
//
//	"top"    — 放在第一个 kind="app" 项的位置之前 (相对 app 子序列首)
//	"bottom" / "middle" / 其它 / "" — append 到最末尾 (跟用户手动 pin 一致)
//
// 落点确定后, 直接 slice 拼接而不重排其它项, 保留 system 项位置。
func insertPinnedAt(items []map[string]any, newPin map[string]any, position string) []map[string]any {
	if position != "top" {
		return append(items, newPin)
	}
	// 找第一个 app 项的位置
	idx := -1
	for i, it := range items {
		if it["kind"] == "app" {
			idx = i
			break
		}
	}
	if idx == -1 {
		// 没有现有 app — append 等价 "插到 app 段最前"
		return append(items, newPin)
	}
	out := make([]map[string]any, 0, len(items)+1)
	out = append(out, items[:idx]...)
	out = append(out, newPin)
	out = append(out, items[idx:]...)
	return out
}

func orMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
