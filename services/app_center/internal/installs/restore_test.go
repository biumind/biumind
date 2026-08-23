package installs

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/biumind/biumind/packages/go-sdk/biu/biuapp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ─── Pure unit tests (no DB) ───────────────────────────────────────

// The restored stub mirrors webviewApp: Init is a no-op and Invoke
// always errors — it exists only to expose the manifest via the
// registry.
func TestRestoredApp_StubBehavior(t *testing.T) {
	m := biuapp.Manifest{Name: "webview-example-com-abcd1234", Version: "0.1.0"}
	app := &restoredApp{m: m}

	if got := app.Manifest(); got.Name != m.Name {
		t.Errorf("Manifest().Name = %q, want %q", got.Name, m.Name)
	}
	if err := app.Init(context.Background(), biuapp.Deps{}); err != nil {
		t.Errorf("Init: %v", err)
	}
	if _, err := app.Invoke(context.Background(), "ping", nil); err == nil {
		t.Error("Invoke should always error")
	} else if !strings.Contains(err.Error(), m.Name) {
		t.Errorf("Invoke error should name the app, got %q", err.Error())
	}
}

// registerRestored must be idempotent — a second mount replaces the
// stub instead of panicking on duplicate registration.
func TestRegisterRestored_ReplaceOnRemount(t *testing.T) {
	reg := biuapp.NewRegistry(biuapp.Deps{})
	ctx := context.Background()

	first := &restoredApp{m: biuapp.Manifest{Name: "dyn", Version: "0.1.0"}}
	if err := registerRestored(ctx, reg, first); err != nil {
		t.Fatalf("first mount: %v", err)
	}
	second := &restoredApp{m: biuapp.Manifest{Name: "dyn", Version: "0.2.0"}}
	if err := registerRestored(ctx, reg, second); err != nil {
		t.Fatalf("remount: %v", err)
	}
	app, ok := reg.Get("dyn")
	if !ok {
		t.Fatal("dyn missing after remount")
	}
	if got := app.Manifest().Version; got != "0.2.0" {
		t.Errorf("version after remount = %q, want 0.2.0", got)
	}
}

// ─── Integration tests (DATABASE_URL-gated) ────────────────────────

// insertAppsRow writes a minimal catalogue row for restore tests and
// registers a cleanup deleting every row of that identifier again.
func insertAppsRow(t *testing.T, pool *pgxpool.Pool, identifier, version, source string, m biuapp.Manifest, createdAt time.Time) {
	t.Helper()
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO app_center.apps
			(id, identifier, name, description, source,
			 manifest, manifest_hash, version, category, status, created_at)
		VALUES ($1, $2, $3, '', $4, $5, $6, $7, 'utility', 'active', $8)
	`, "app_"+identifier+"_"+version, identifier, m.Name, source,
		raw, strings.Repeat("a", 64), version, createdAt,
	); err != nil {
		t.Fatalf("insert apps row %s@%s: %v", identifier, version, err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM app_center.apps WHERE identifier = $1`, identifier)
	})
}

// insertInstallationRow writes a minimal installations row referencing
// identifier so restore tests can exercise the "still installed"
// visibility rule.
func insertInstallationRow(t *testing.T, pool *pgxpool.Pool, identifier string) {
	t.Helper()
	id := uuid.New()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO app_center.installations
			(id, scope, scope_id, app_id, identifier, version,
			 enabled, permissions_granted, config, forced,
			 installed_at, updated_at)
		VALUES ($1, 'user', $2, $3, $4, '0.1.0',
		        true, '{}', '{}', false, now(), now())
	`, id, uuid.New(), "app_"+identifier, identifier); err != nil {
		t.Fatalf("insert installations row for %s: %v", identifier, err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM app_center.installations WHERE id = $1`, id)
	})
}

// RestoreDynamicApps must mount only the newest version row per
// identifier and ignore non-dynamic sources.
func TestRestoreDynamicApps_LatestVersionOnly(t *testing.T) {
	pool := openDB(t)
	reg := biuapp.NewRegistry(biuapp.Deps{})

	identifier := "webview-restore-" + uuid.NewString()[:8]
	insertAppsRow(t, pool, identifier, "0.1.0", "user_webview",
		biuapp.Manifest{Name: identifier, Version: "0.1.0"},
		time.Now().Add(-time.Hour))
	insertAppsRow(t, pool, identifier, "0.2.0", "user_webview",
		biuapp.Manifest{Name: identifier, Version: "0.2.0"},
		time.Now())
	insertInstallationRow(t, pool, identifier)

	bundledID := "bundled-restore-" + uuid.NewString()[:8]
	insertAppsRow(t, pool, bundledID, "1.0.0", "bundled",
		biuapp.Manifest{Name: bundledID, Version: "1.0.0"},
		time.Now())

	n, err := RestoreDynamicApps(context.Background(), pool, reg)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if n != 1 {
		t.Errorf("restored = %d, want 1 (latest version of the dynamic row only)", n)
	}
	app, ok := reg.Get(identifier)
	if !ok {
		t.Fatalf("%s not restored", identifier)
	}
	if got := app.Manifest().Version; got != "0.2.0" {
		t.Errorf("restored version = %q, want 0.2.0 (latest row)", got)
	}
	if _, ok := reg.Get(bundledID); ok {
		t.Error("bundled-source row must not be restored")
	}
}

// An uninstalled user_webview app (catalogue row survives uninstall)
// must NOT be resurrected into the registry on restart.
func TestRestoreDynamicApps_SkipsUninstalledUserWebView(t *testing.T) {
	pool := openDB(t)
	reg := biuapp.NewRegistry(biuapp.Deps{})

	identifier := "webview-gone-" + uuid.NewString()[:8]
	insertAppsRow(t, pool, identifier, "0.1.0", "user_webview",
		biuapp.Manifest{Name: identifier, Version: "0.1.0"},
		time.Now())

	n, err := RestoreDynamicApps(context.Background(), pool, reg)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if n != 0 {
		t.Errorf("restored = %d, want 0 (no installations row)", n)
	}
	if _, ok := reg.Get(identifier); ok {
		t.Error("uninstalled user_webview app must not be restored")
	}
}

// gh_official / gh_community rows restore without any installation —
// catalogue visibility is their design intent.
func TestRestoreDynamicApps_OfficialVisibleWithoutInstall(t *testing.T) {
	pool := openDB(t)
	reg := biuapp.NewRegistry(biuapp.Deps{})

	identifier := "gh-official-" + uuid.NewString()[:8]
	insertAppsRow(t, pool, identifier, "0.1.0", "gh_official",
		biuapp.Manifest{Name: identifier, Version: "0.1.0"},
		time.Now())

	n, err := RestoreDynamicApps(context.Background(), pool, reg)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if n != 1 {
		t.Errorf("restored = %d, want 1 (gh_official restores without install)", n)
	}
	if _, ok := reg.Get(identifier); !ok {
		t.Error("gh_official row must be restored without an installations row")
	}
}

// Uninstall of a dynamic (user_webview) app must unregister it from the
// in-memory registry so it disappears from the catalogue immediately.
func TestUninstall_UnregistersDynamicApp(t *testing.T) {
	pool := openDB(t)
	reg := biuapp.NewRegistry(biuapp.Deps{})
	in := New(pool, reg, nil)

	identifier := "webview-unreg-" + uuid.NewString()[:8]
	app := newStubApp(identifier)
	insertAppsRow(t, pool, identifier, app.manifest.Version, "user_webview",
		app.manifest, time.Now())
	if err := reg.Register(context.Background(), app); err != nil {
		t.Fatalf("register: %v", err)
	}

	scope, scopeID := freshTenant()
	row, err := in.Install(context.Background(), InstallRequest{
		Identifier:   identifier,
		Scope:        scope,
		ScopeID:      scopeID,
		CallerUserID: uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if err := in.Uninstall(context.Background(), row.ID, uuid.NewString(), "", nil); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if _, ok := reg.Get(identifier); ok {
		t.Error("dynamic app still in registry after uninstall")
	}
}

// Uninstall of a bundled app (no dynamic catalogue row) must leave the
// process-global registration alone — other users may still have it
// installed.
func TestUninstall_KeepsBundledApp(t *testing.T) {
	pool := openDB(t)
	reg := biuapp.NewRegistry(biuapp.Deps{})
	in := New(pool, reg, nil)

	identifier := "bundled-keep-" + uuid.NewString()[:8]
	app := newStubApp(identifier)
	if err := reg.Register(context.Background(), app); err != nil {
		t.Fatalf("register: %v", err)
	}

	scope, scopeID := freshTenant()
	row, err := in.Install(context.Background(), InstallRequest{
		Identifier:   identifier,
		Scope:        scope,
		ScopeID:      scopeID,
		CallerUserID: uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if err := in.Uninstall(context.Background(), row.ID, uuid.NewString(), "", nil); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if _, ok := reg.Get(identifier); !ok {
		t.Error("bundled app must stay registered after one user's uninstall")
	}
}
