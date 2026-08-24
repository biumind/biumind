// Repo App install + build persistence (Repo Apps M1.4, tech plan
// §2.4/§2.6).
//
// A Repo App is a GitHub project installed as a kind=webview app whose
// view URL resolves to a local runner at open time. The server side
// follows the user_webview five-step shape (user_webview.go:65-167):
//
//  1. the manifest comes from a server-side re-run of repoanalyze
//     (the client-supplied draft is never trusted);
//  2. catalogue UPSERT into app_center.apps with source='gh_private',
//     tier='private', repo_meta and adapter_source columns filled;
//  3. events.Write(AppPublished) in the same tx (I4);
//  4. in-memory Registry stub registration (restoredApp);
//  5. Installer.Install for scope=user.
//
// Secret defence (D9): env fields flagged secret in the repo's env
// schema are runner-local (.env on the user's machine, written by the
// CLI). They must never reach installations.config — rejectSecretConfig
// enforces that at the persistence boundary regardless of caller.

package installs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/biumind/biumind/packages/go-sdk/biu/biuapp"
	"github.com/biumind/biumind/services/app_center/internal/events"
	"github.com/biumind/biumind/services/app_center/internal/repoanalyze"
	"github.com/jackc/pgx/v5"
)

var (
	// ErrSecretConfigField — a config key matches a secret env field of
	// the repo. Secrets stay on the runner's local .env (D9); the
	// server refuses to persist them.
	ErrSecretConfigField = errors.New("repo_app: secret field not allowed in server-side config")
	// ErrNoRepoRef — the catalogue row has no repo_meta.latest_ref to
	// redeploy (should not happen for rows written by CreateRepoApp;
	// guards hand-migrated / legacy rows).
	ErrNoRepoRef = errors.New("repo_app: no ref available to redeploy")
	// ErrBuildAlreadyFinal — the build row is already live/failed; a
	// repeated complete report is rejected so a retried runner callback
	// can't resurrect or overwrite a terminal build.
	ErrBuildAlreadyFinal = errors.New("repo_app: build already in terminal state")
	// ErrInvalidBuildStatus — complete reports accept only live|failed.
	ErrInvalidBuildStatus = errors.New("repo_app: build status must be live or failed")
)

// RepoAppRequest carries everything the API layer resolved for one
// repo-app install.
type RepoAppRequest struct {
	// Analysis is the fresh server-side repoanalyze.Result (never the
	// client-supplied draft).
	Analysis *repoanalyze.Result
	// RefType is "release" | "branch" — recorded on the published
	// event; ref resolution itself happens inside repoanalyze.
	RefType string
	// Config holds the non-secret env values only; secret keys are
	// rejected before any write.
	Config      map[string]any
	UserID      string
	CallerOrgID string
}

// CreateRepoApp synthesises the catalogue row and installs the repo app
// for the calling user. Re-installing the same repo hits the
// (identifier, version) UPSERT + ErrAlreadyInstalled path — same
// idempotency semantics as user_webview.
func (in *Installer) CreateRepoApp(ctx context.Context, req RepoAppRequest) (*Installation, error) {
	if req.Analysis == nil {
		return nil, fmt.Errorf("repo_app: analysis required")
	}
	if req.UserID == "" {
		return nil, fmt.Errorf("repo_app: user_id required")
	}
	if err := rejectSecretConfig(req.Config, req.Analysis.EnvSchema); err != nil {
		return nil, err
	}

	manifest := req.Analysis.ManifestDraft
	if err := biuapp.Validate(&manifest); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrManifestInvalid, err)
	}
	identifier := manifest.Slug()

	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		return nil, fmt.Errorf("repo_app: marshal manifest: %w", err)
	}
	repoMetaJSON, err := json.Marshal(req.Analysis.RepoMeta)
	if err != nil {
		return nil, fmt.Errorf("repo_app: marshal repo_meta: %w", err)
	}
	// M2.1: the release poller diffs latest_* against installed_* to
	// raise update_available, and the complete endpoint rewinds these on
	// a successful redeploy — pin both at install time.
	repoMetaJSON, err = mergeJSONFields(repoMetaJSON, map[string]any{
		"installed_ref": req.Analysis.RepoMeta.LatestRef,
		"installed_sha": req.Analysis.RepoMeta.LatestSHA,
	})
	if err != nil {
		return nil, fmt.Errorf("repo_app: merge repo_meta installed fields: %w", err)
	}
	manifestHash := sha256Hex(manifestJSON)
	appID := "app_" + identifier

	tx, err := in.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("repo_app: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Catalogue UPSERT. tier='private' + adapter_source='auto' per
	// migration 00002; adapter / index_entry / verification stay NULL
	// until M2.5.
	if _, err := tx.Exec(ctx, `
		INSERT INTO app_center.apps
			(id, identifier, name, description, source,
			 manifest, manifest_hash, version, category, status,
			 tier, repo_meta, adapter_source)
		VALUES ($1, $2, $3, $4, 'gh_private',
		        $5, $6, $7, $8, 'active',
		        'private', $9, 'auto')
		ON CONFLICT (identifier, version)
		DO UPDATE SET
			name           = EXCLUDED.name,
			description    = EXCLUDED.description,
			manifest       = EXCLUDED.manifest,
			manifest_hash  = EXCLUDED.manifest_hash,
			tier           = EXCLUDED.tier,
			repo_meta      = EXCLUDED.repo_meta,
			adapter_source = EXCLUDED.adapter_source,
			updated_at     = now()
	`,
		appID, identifier, manifest.DisplayName(), manifest.Description,
		manifestJSON, manifestHash, manifest.Version, manifest.Category,
		repoMetaJSON,
	); err != nil {
		return nil, fmt.Errorf("repo_app: upsert apps row: %w", err)
	}

	if err := events.Write(ctx, tx, events.Event{
		ScopeKind: "app",
		ScopeID:   appID,
		ActorType: events.ActorUser,
		ActorID:   req.UserID,
		Type:      events.AppPublished,
		Payload: map[string]any{
			"identifier": identifier,
			"version":    manifest.Version,
			"source":     "gh_private",
			"repo_url":   req.Analysis.RepoMeta.URL,
			"ref_type":   req.RefType,
			"ref":        req.Analysis.RepoMeta.LatestRef,
			"sha":        req.Analysis.RepoMeta.LatestSHA,
		},
	}); err != nil {
		return nil, fmt.Errorf("repo_app: write app.published event: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("repo_app: commit apps row: %w", err)
	}

	// In-memory stub so /v1/apps + Install's manifest read can find the
	// app. Same restoredApp shape as the startup restore path.
	if err := registerRestored(ctx, in.Registry, &restoredApp{m: manifest}); err != nil {
		return nil, fmt.Errorf("repo_app: register: %w", err)
	}

	return in.Install(ctx, InstallRequest{
		Identifier:         identifier,
		Scope:              "user",
		ScopeID:            req.UserID,
		GrantedPermissions: manifest.Permissions,
		Config:             req.Config,
		CallerUserID:       req.UserID,
		CallerOrgID:        req.CallerOrgID,
	})
}

// mergeJSONFields overlays extra keys onto an already-marshalled JSON
// object (repo_meta gains poller-facing fields the RepoMeta struct
// doesn't carry).
func mergeJSONFields(raw []byte, extra map[string]any) ([]byte, error) {
	m := map[string]any{}
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	for k, v := range extra {
		m[k] = v
	}
	return json.Marshal(m)
}

// rejectSecretConfig enforces the D9 boundary: a config key that matches
// a secret env field of the repo is refused. Exact, case-sensitive name
// match — env var names are conventional uppercase and the client echoes
// the schema names back verbatim.
func rejectSecretConfig(config map[string]any, schema []repoanalyze.EnvField) error {
	if len(config) == 0 {
		return nil
	}
	for _, f := range schema {
		if !f.Secret {
			continue
		}
		if _, ok := config[f.Name]; ok {
			return fmt.Errorf("%w: %q", ErrSecretConfigField, f.Name)
		}
	}
	return nil
}

// ─── Ownership + builds (runtime / builds / redeploy endpoints) ────

// OwnedRepoInstall resolves an installation id for the calling user,
// folding "does not exist" and "belongs to someone else" into
// ErrNotFound so the API layer never discloses other users' install ids.
func (in *Installer) OwnedRepoInstall(ctx context.Context, installID, userID string) (*Installation, error) {
	row, err := in.Get(ctx, installID)
	if err != nil {
		return nil, err
	}
	if row.Scope != "user" || row.ScopeID != userID {
		return nil, ErrNotFound
	}
	return row, nil
}

// RepoBuild mirrors one app_center.repo_builds row. JSON tags are the
// wire contract with the desktop client (snake_case only).
type RepoBuild struct {
	ID         string    `json:"id"`
	Ref        string    `json:"ref"`
	SHA        string    `json:"sha"`
	Status     string    `json:"status"`
	LogRef     *string   `json:"log_ref,omitempty"`
	DurationMs *int      `json:"duration_ms,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// LatestBuildStatus returns the status of the install's most recent
// build row, or "" when no build exists yet.
func (in *Installer) LatestBuildStatus(ctx context.Context, installID string) (string, error) {
	var status string
	err := in.Pool.QueryRow(ctx, `
		SELECT status FROM app_center.repo_builds
		 WHERE install_id = $1
		 ORDER BY created_at DESC
		 LIMIT 1
	`, installID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("repo_app: latest build: %w", err)
	}
	return status, nil
}

// ListBuilds returns the install's recent builds, newest first.
func (in *Installer) ListBuilds(ctx context.Context, installID string, limit int) ([]RepoBuild, error) {
	rows, err := in.Pool.Query(ctx, `
		SELECT id, ref, sha, status, log_ref, duration_ms, created_at
		  FROM app_center.repo_builds
		 WHERE install_id = $1
		 ORDER BY created_at DESC
		 LIMIT $2
	`, installID, limit)
	if err != nil {
		return nil, fmt.Errorf("repo_app: list builds: %w", err)
	}
	defer rows.Close()

	builds := []RepoBuild{}
	for rows.Next() {
		var b RepoBuild
		if err := rows.Scan(&b.ID, &b.Ref, &b.SHA, &b.Status,
			&b.LogRef, &b.DurationMs, &b.CreatedAt); err != nil {
			return nil, fmt.Errorf("repo_app: scan build: %w", err)
		}
		builds = append(builds, b)
	}
	return builds, rows.Err()
}

// QueuedRedeploy is the redeploy endpoint's response: the queued build
// id plus the ref/sha it is pinned at. The client hands ref/sha to the
// CLI (`biu repo-app update --ref`) so runner and server agree on the
// target without a second lookup.
type QueuedRedeploy struct {
	BuildID string `json:"build_id"`
	Ref     string `json:"ref"`
	SHA     string `json:"sha"`
}

// QueueRedeploy records a queued build row pinned at the catalogue's
// repo_meta.latest_ref/sha. The actual fetch+restart is the runner's
// job; this endpoint only persists intent + the audit event.
func (in *Installer) QueueRedeploy(ctx context.Context, installID, identifier, callerUserID string) (*QueuedRedeploy, error) {
	var ref, sha string
	err := in.Pool.QueryRow(ctx, `
		SELECT COALESCE(repo_meta->>'latest_ref', ''),
		       COALESCE(repo_meta->>'latest_sha', '')
		  FROM app_center.apps
		 WHERE identifier = $1
		 ORDER BY created_at DESC
		 LIMIT 1
	`, identifier).Scan(&ref, &sha)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("repo_app: read repo_meta: %w", err)
	}
	if ref == "" {
		return nil, fmt.Errorf("%w: %s", ErrNoRepoRef, identifier)
	}

	tx, err := in.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("repo_app: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var buildID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO app_center.repo_builds (install_id, ref, sha, status)
		VALUES ($1, $2, $3, 'queued')
		RETURNING id
	`, installID, ref, sha).Scan(&buildID); err != nil {
		return nil, fmt.Errorf("repo_app: insert build: %w", err)
	}

	if err := events.Write(ctx, tx, events.Event{
		ScopeKind: "install",
		ScopeID:   installID,
		ActorType: events.ActorUser,
		ActorID:   callerUserID,
		Type:      events.AppUpgraded,
		Payload: map[string]any{
			"action":     "redeploy_queued",
			"identifier": identifier,
			"build_id":   buildID,
			"ref":        ref,
			"sha":        sha,
		},
	}); err != nil {
		return nil, fmt.Errorf("repo_app: write redeploy event: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("repo_app: commit build: %w", err)
	}
	return &QueuedRedeploy{BuildID: buildID, Ref: ref, SHA: sha}, nil
}

// ─── Build completion report (M2.3) ──────────────────────────────────

// CompleteBuildRequest is the runner's build outcome, relayed by the
// client (POST .../builds/{build_id}/complete).
type CompleteBuildRequest struct {
	// Status is "live" (runner is serving the new ref) or "failed"
	// (runner rolled back to the old ref).
	Status string
	// SHA is the commit the runner actually checked out; empty falls
	// back to the sha the build row was queued with.
	SHA string
	// LogRef points at the runner's local build log (instance dir path).
	LogRef string
	// CallerUserID is the JWT subject — recorded as the event actor.
	CallerUserID string
}

// CompleteRepoBuild applies the runner's outcome in a single tx:
//
//   - live: repo_builds row → live (+ duration_ms), apps.repo_meta
//     installed_ref/installed_sha advance to the build's ref/sha,
//     update_available clears, next_poll_at resets to the base
//     interval; app.upgraded{action:redeploy_completed} event (I4).
//   - failed: repo_builds row → failed; repo_meta keeps the old
//     installed_* (the runner rolled back), event marks the failure.
//
// The build must belong to the install and still be non-terminal —
// "not this install's build" folds into ErrNotFound, a terminal build
// into ErrBuildAlreadyFinal.
func (in *Installer) CompleteRepoBuild(ctx context.Context, install *Installation, buildID string, req CompleteBuildRequest) error {
	if req.Status != "live" && req.Status != "failed" {
		return fmt.Errorf("%w: %q", ErrInvalidBuildStatus, req.Status)
	}

	tx, err := in.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("repo_app: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var buildStatus, buildRef, buildSHA string
	err = tx.QueryRow(ctx, `
		SELECT status, ref, sha FROM app_center.repo_builds
		 WHERE id = $1 AND install_id = $2
		 FOR UPDATE
	`, buildID, install.ID).Scan(&buildStatus, &buildRef, &buildSHA)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("repo_app: lock build: %w", err)
	}
	if buildStatus == "live" || buildStatus == "failed" {
		return fmt.Errorf("%w: %s", ErrBuildAlreadyFinal, buildID)
	}

	sha := req.SHA
	if sha == "" {
		sha = buildSHA
	}

	if _, err := tx.Exec(ctx, `
		UPDATE app_center.repo_builds
		   SET status      = $2,
		       log_ref     = NULLIF($3, ''),
		       duration_ms = (EXTRACT(EPOCH FROM (now() - created_at)) * 1000)::int
		 WHERE id = $1
	`, buildID, req.Status, req.LogRef); err != nil {
		return fmt.Errorf("repo_app: update build: %w", err)
	}

	action := "redeploy_failed"
	if req.Status == "live" {
		action = "redeploy_completed"
		// Advance the installed pair + clear the banner; reset the poll
		// schedule to the base interval (poll_interval_sec is written by
		// the poller, default 6h when absent).
		if _, err := tx.Exec(ctx, `
			UPDATE app_center.apps
			   SET repo_meta = repo_meta || jsonb_build_object(
			           'installed_ref', $2::text,
			           'installed_sha', $3::text,
			           'update_available', false,
			           'next_poll_at', now() + COALESCE(
			               (repo_meta->>'poll_interval_sec')::int, 21600
			           ) * interval '1 second'),
			       updated_at = now()
			 WHERE id = (
			       SELECT id FROM app_center.apps
			        WHERE identifier = $1
			        ORDER BY created_at DESC
			        LIMIT 1)
		`, install.Identifier, buildRef, sha); err != nil {
			return fmt.Errorf("repo_app: advance installed ref: %w", err)
		}
	}

	if err := events.Write(ctx, tx, events.Event{
		ScopeKind: "install",
		ScopeID:   install.ID,
		ActorType: events.ActorUser,
		ActorID:   req.CallerUserID,
		Type:      events.AppUpgraded,
		Payload: map[string]any{
			"action":     action,
			"identifier": install.Identifier,
			"build_id":   buildID,
			"ref":        buildRef,
			"sha":        sha,
		},
	}); err != nil {
		return fmt.Errorf("repo_app: write complete event: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("repo_app: commit complete: %w", err)
	}
	return nil
}

// RuntimeStatusFor maps a repo_builds status onto the runtime endpoint's
// coarse status vocabulary ("" = no build row yet).
func RuntimeStatusFor(buildStatus string) string {
	switch buildStatus {
	case "live":
		return "running"
	case "failed":
		return "failed"
	case "queued", "building", "deploying":
		return "starting"
	default:
		return "stopped"
	}
}
