// Dynamic-app registry restore (M1.5).
//
// Dynamic apps (user_webview today; gh_* sources land later) have no
// in-process implementation — their catalogue row in app_center.apps
// IS the source of truth, and the in-memory biuapp.Registry only holds
// a synthesised stub so /v1/apps and the invoke path can find them.
// That stub used to be registered only in the process that handled
// the POST, so a restart (or a different replica) made the app vanish
// from the catalogue with a 404. RestoreDynamicApps re-mounts those
// stubs from the DB at startup.

package installs

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/biumind/biumind/packages/go-sdk/biu/biuapp"
	"github.com/jackc/pgx/v5/pgxpool"
)

// dynamicSources are the app_center.apps sources whose App is
// synthesised from the stored manifest rather than registered from
// in-process code. bundled / org / marketplace apps register
// themselves at boot and must NOT be touched here.
//
// gh_* are not yet writable (the baseline CHECK constraint allows only
// bundled / org / marketplace / user_webview) — listing them here makes
// the restore path forward-compatible at zero cost.
var dynamicSources = []string{"user_webview", "gh_private", "gh_community", "gh_official"}

// RestoreDynamicApps mounts a stub App for every dynamic catalogue row
// into reg. When an identifier has multiple version rows
// (UNIQUE(identifier, version) upserts), only the most recently created
// row is restored — created_at DESC stands in for semver ordering,
// which is good enough while versions are published monotonically.
//
// Visibility rule: user_webview / gh_private rows are restored only
// while at least one installations row still references them — an
// uninstalled app must not reappear in the catalogue after a restart
// (previously it did: the catalogue row survives uninstall, so every
// reboot resurrected deleted apps). gh_official / gh_community rows
// restore unconditionally: being visible in the catalogue without an
// install is their design intent.
//
// Returns the number of apps (re)mounted. Idempotent: an identifier
// already present in reg is replaced, so a re-run never panics on
// duplicate registration.
func RestoreDynamicApps(ctx context.Context, pool *pgxpool.Pool, reg *biuapp.Registry) (int, error) {
	rows, err := pool.Query(ctx, `
		SELECT DISTINCT ON (identifier) identifier, manifest
		  FROM app_center.apps a
		 WHERE source = ANY($1)
		   AND (source IN ('gh_official', 'gh_community')
		        OR EXISTS (SELECT 1 FROM app_center.installations i
		                    WHERE i.identifier = a.identifier))
		 ORDER BY identifier, created_at DESC
	`, dynamicSources)
	if err != nil {
		return 0, fmt.Errorf("restore: query apps: %w", err)
	}
	defer rows.Close()

	restored := 0
	for rows.Next() {
		var (
			identifier string
			raw        []byte
		)
		if err := rows.Scan(&identifier, &raw); err != nil {
			return restored, fmt.Errorf("restore: scan: %w", err)
		}
		var m biuapp.Manifest
		if err := json.Unmarshal(raw, &m); err != nil {
			return restored, fmt.Errorf("restore: decode manifest for %q: %w", identifier, err)
		}
		if m.Name == "" {
			m.Name = identifier
		}
		if err := registerRestored(ctx, reg, &restoredApp{m: m}); err != nil {
			return restored, fmt.Errorf("restore: register %q: %w", identifier, err)
		}
		restored++
	}
	return restored, rows.Err()
}

// unregisterIfDynamic removes identifier from the in-memory registry
// when its catalogue row is a dynamic source. Called after a successful
// Uninstall so a removed user_webview app disappears from the catalogue
// immediately instead of lingering until the next restart.
//
// Bundled apps are deliberately left alone: the registry is
// process-global and other users' installs still depend on them.
// Best-effort — a missing catalogue row (bundled apps upsert lazily)
// or a query error simply skips the unregister.
func (in *Installer) unregisterIfDynamic(ctx context.Context, identifier string) {
	var source string
	err := in.Pool.QueryRow(ctx, `
		SELECT source FROM app_center.apps
		 WHERE identifier = $1
		 ORDER BY created_at DESC
		 LIMIT 1
	`, identifier).Scan(&source)
	if err != nil {
		return
	}
	for _, s := range dynamicSources {
		if source == s {
			in.Registry.Unregister(identifier)
			return
		}
	}
}

// registerRestored is the restore-path analogue of
// registerOrReplaceUserWebView: idempotent mount of a stub App. A name
// already present means a previous restore (or a duplicate catalogue
// identifier) — replace it rather than panic.
func registerRestored(ctx context.Context, reg *biuapp.Registry, app biuapp.App) error {
	if _, exists := reg.Get(app.Manifest().Name); exists {
		return reg.Replace(ctx, app)
	}
	return reg.Register(ctx, app)
}

// ─── Restored stub ─────────────────────────────────────────────────
//
// Same shape as webviewApp (see user_webview.go): Init is a no-op and
// Invoke always errors — the stub exists so Registry.Get/List expose
// the manifest, not to execute actions. Kept as a distinct type so the
// Invoke error doesn't misreport a gh_* app as a webview.

type restoredApp struct{ m biuapp.Manifest }

func (a *restoredApp) Manifest() biuapp.Manifest                   { return a.m }
func (a *restoredApp) Init(_ context.Context, _ biuapp.Deps) error { return nil }
func (a *restoredApp) Invoke(_ context.Context, action string, _ json.RawMessage) (any, error) {
	return nil, fmt.Errorf("dynamic app %q has no in-process implementation (got action %q)", a.m.Name, action)
}
