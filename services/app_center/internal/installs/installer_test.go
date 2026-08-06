package installs

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/biumind/biumind/packages/go-sdk/biu/biuapp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Integration tests run against app_center.* + auto-applied migrations.
// Skip when DATABASE_URL is unset; CI matrix runs the integration leg
// after `task migrate up`. Same pattern as runtime/internal/skills.

func openDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping installer integration tests")
	}
	p, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(p.Close)
	for _, table := range []string{
		"app_center.apps",
		"app_center.installations",
		"app_center.events",
	} {
		var exists bool
		if err := p.QueryRow(context.Background(),
			`SELECT EXISTS (SELECT 1 FROM information_schema.tables
			    WHERE table_schema = split_part($1, '.', 1)
			      AND table_name = split_part($1, '.', 2))`,
			table,
		).Scan(&exists); err != nil {
			t.Fatalf("table check %s: %v", table, err)
		}
		if !exists {
			t.Skipf("%s missing; apply services/app_center/migrations", table)
		}
	}
	return p
}

// ─── Test app implementing all hooks ─────────────────────────────

type stubApp struct {
	manifest      biuapp.Manifest
	installed     int
	uninstalled   int
	upgraded      int
	configUpdated int
}

func (s *stubApp) Manifest() biuapp.Manifest                     { return s.manifest }
func (s *stubApp) Init(ctx context.Context, _ biuapp.Deps) error { return nil }
func (s *stubApp) Invoke(ctx context.Context, action string, in json.RawMessage) (any, error) {
	return map[string]any{"ok": true}, nil
}
func (s *stubApp) OnInstall(ctx context.Context, in biuapp.Install) error { s.installed++; return nil }
func (s *stubApp) OnUninstall(ctx context.Context, in biuapp.Install) error {
	s.uninstalled++
	return nil
}
func (s *stubApp) OnUpgrade(ctx context.Context, in biuapp.Install, from string) error {
	s.upgraded++
	return nil
}
func (s *stubApp) OnConfigUpdate(ctx context.Context, in biuapp.Install) error {
	s.configUpdated++
	return nil
}

func newStubApp(slug string) *stubApp {
	return &stubApp{
		manifest: biuapp.Manifest{
			Name:        slug,
			Version:     "0.1.0",
			Description: "stub app for tests",
			Permissions: []string{"hub.invoke"},
			Actions:     []biuapp.ActionSpec{{Name: "ping"}},
		},
	}
}

// withDefaultPin clones a stub app with sidebar.default_pin=true.
func (s *stubApp) withDefaultPin() *stubApp {
	out := *s
	out.manifest.Sidebar = &biuapp.SidebarHints{DefaultPin: true}
	return &out
}

// withDefaultPinPosition clones a stub app with default_pin=true 加
// preferred_position 字段。
func (s *stubApp) withDefaultPinPosition(pos string) *stubApp {
	out := *s
	out.manifest.Sidebar = &biuapp.SidebarHints{
		DefaultPin:        true,
		PreferredPosition: pos,
	}
	return &out
}

// freshTenant gives each test a unique (scope, scope_id) so concurrent
// runs don't collide on the UNIQUE (scope, scope_id, identifier).
func freshTenant() (string, string) {
	return "user", uuid.NewString()
}

// ─── Tests ──────────────────────────────────────────────────────

func TestInstaller_HappyPath(t *testing.T) {
	pool := openDB(t)
	app := newStubApp("test-rss")
	reg := biuapp.NewRegistry(biuapp.Deps{})
	if err := reg.Register(context.Background(), app); err != nil {
		t.Fatal(err)
	}
	in := New(pool, reg, AllowAll{})

	scope, scopeID := freshTenant()
	row, err := in.Install(context.Background(), InstallRequest{
		Identifier:         "test-rss",
		Scope:              scope,
		ScopeID:            scopeID,
		GrantedPermissions: []string{"hub.invoke"},
		CallerUserID:       scopeID,
	})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if app.installed != 1 {
		t.Errorf("OnInstall fired %d times, want 1", app.installed)
	}

	// Get round-trip.
	got, err := in.Get(context.Background(), row.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Identifier != "test-rss" || got.Version != "0.1.0" {
		t.Errorf("get returned %+v", got)
	}
	if !got.Enabled {
		t.Errorf("expected enabled=true on fresh install")
	}

	// List.
	rows, err := in.List(context.Background(), scope, scopeID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("list returned %d rows, want 1", len(rows))
	}

	// Toggle off.
	off, err := in.Toggle(context.Background(), row.ID, false, scopeID, "", nil)
	if err != nil {
		t.Fatalf("toggle off: %v", err)
	}
	if off.Enabled {
		t.Errorf("expected enabled=false after toggle off")
	}

	// Toggle on (idempotent).
	on, err := in.Toggle(context.Background(), row.ID, true, scopeID, "", nil)
	if err != nil {
		t.Fatalf("toggle on: %v", err)
	}
	if !on.Enabled {
		t.Errorf("expected enabled=true after toggle on")
	}
	again, _ := in.Toggle(context.Background(), row.ID, true, scopeID, "", nil)
	if !again.Enabled {
		t.Errorf("idempotent toggle should remain enabled")
	}

	// Uninstall.
	if err := in.Uninstall(context.Background(), row.ID, scopeID, "", nil); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if app.uninstalled != 1 {
		t.Errorf("OnUninstall fired %d times, want 1", app.uninstalled)
	}
	if _, err := in.Get(context.Background(), row.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected NotFound after uninstall, got %v", err)
	}

	// Verify events ledger has both rows.
	var count int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM app_center.events
		WHERE scope = $1
	`, "install:"+row.ID).Scan(&count); err != nil {
		t.Fatalf("events count: %v", err)
	}
	// install + toggle off + toggle on + uninstall = 4 events
	if count < 3 {
		t.Errorf("expected at least 3 events, got %d", count)
	}
}

func TestInstaller_RejectsUnknownApp(t *testing.T) {
	pool := openDB(t)
	reg := biuapp.NewRegistry(biuapp.Deps{})
	in := New(pool, reg, AllowAll{})

	_, scopeID := freshTenant()
	_, err := in.Install(context.Background(), InstallRequest{
		Identifier:   "ghost",
		Scope:        "user",
		ScopeID:      scopeID,
		CallerUserID: scopeID,
	})
	if !errors.Is(err, ErrUnknownApp) {
		t.Errorf("expected ErrUnknownApp, got %v", err)
	}
}

func TestInstaller_RejectsPermissionsExceedManifest(t *testing.T) {
	pool := openDB(t)
	app := newStubApp("test-perms")
	reg := biuapp.NewRegistry(biuapp.Deps{})
	_ = reg.Register(context.Background(), app)
	in := New(pool, reg, AllowAll{})

	_, scopeID := freshTenant()
	_, err := in.Install(context.Background(), InstallRequest{
		Identifier:         "test-perms",
		Scope:              "user",
		ScopeID:            scopeID,
		GrantedPermissions: []string{"hub.invoke", "wiki.write"}, // wiki.write not in manifest
		CallerUserID:       scopeID,
	})
	if !errors.Is(err, ErrPermissionsExceed) {
		t.Errorf("expected ErrPermissionsExceed, got %v", err)
	}
}

func TestInstaller_RejectsDoubleInstall(t *testing.T) {
	pool := openDB(t)
	app := newStubApp("test-dup")
	reg := biuapp.NewRegistry(biuapp.Deps{})
	_ = reg.Register(context.Background(), app)
	in := New(pool, reg, AllowAll{})

	_, scopeID := freshTenant()
	first, err := in.Install(context.Background(), InstallRequest{
		Identifier:         "test-dup",
		Scope:              "user",
		ScopeID:            scopeID,
		GrantedPermissions: []string{"hub.invoke"},
		CallerUserID:       scopeID,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = in.Install(context.Background(), InstallRequest{
		Identifier:         "test-dup",
		Scope:              "user",
		ScopeID:            scopeID,
		GrantedPermissions: []string{"hub.invoke"},
		CallerUserID:       scopeID,
	})
	if !errors.Is(err, ErrAlreadyInstalled) {
		t.Errorf("expected ErrAlreadyInstalled, got %v", err)
	}
	// Cleanup.
	_ = in.Uninstall(context.Background(), first.ID, scopeID, "", nil)
}

func TestInstaller_AuthzDenyBlocksInstall(t *testing.T) {
	pool := openDB(t)
	app := newStubApp("test-deny")
	reg := biuapp.NewRegistry(biuapp.Deps{})
	_ = reg.Register(context.Background(), app)

	deny := stubDecider{decision: "DENY", reason: "policy says no"}
	in := New(pool, reg, deny)

	_, scopeID := freshTenant()
	_, err := in.Install(context.Background(), InstallRequest{
		Identifier:         "test-deny",
		Scope:              "user",
		ScopeID:            scopeID,
		GrantedPermissions: []string{"hub.invoke"},
		CallerUserID:       scopeID,
	})
	if !errors.Is(err, ErrPermissionDenied) {
		t.Errorf("expected ErrPermissionDenied, got %v", err)
	}
}

type stubDecider struct {
	decision string
	reason   string
}

func (s stubDecider) Check(_ context.Context, _ DecideRequest) (*DecideResult, error) {
	return &DecideResult{Decision: s.decision, Reason: s.reason}, nil
}

// ─── default_pin auto-pin (设计 §10A.10 + 决策 §21#10) ───────────

// 装一个 sidebar.default_pin=true 的 bundled-source app, 应该自动写入
// sidebar_layouts。catalog 表无该 app 行 → 视为 bundled source 处理。
func TestInstaller_DefaultPin_AutoPinsBundled(t *testing.T) {
	pool := openDB(t)
	ctx := context.Background()
	app := newStubApp("test-defaultpin").withDefaultPin()
	reg := biuapp.NewRegistry(biuapp.Deps{})
	if err := reg.Register(ctx, app); err != nil {
		t.Fatal(err)
	}
	in := New(pool, reg, AllowAll{})

	_, scopeID := freshTenant()
	row, err := in.Install(ctx, InstallRequest{
		Identifier:   "test-defaultpin",
		Scope:        "user",
		ScopeID:      scopeID,
		CallerUserID: scopeID,
	})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	t.Cleanup(func() { _ = in.Uninstall(ctx, row.ID, scopeID, "", nil) })

	// 拉 sidebar_layouts 确认已 auto-pin。
	var raw []byte
	err = pool.QueryRow(ctx, `
		SELECT items FROM app_center.sidebar_layouts
		 WHERE user_id = $1 AND scope = 'desktop'
	`, scopeID).Scan(&raw)
	if err != nil {
		t.Fatalf("layout query: %v", err)
	}
	var items []map[string]any
	if err := json.Unmarshal(raw, &items); err != nil {
		t.Fatalf("decode items: %v", err)
	}
	found := false
	for _, i := range items {
		if i["kind"] == "app" && i["ref"] == row.ID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("install_id %s not in sidebar layout: %s", row.ID, raw)
	}
}

// 同一 user 装两个 default_pin app, 第二次 append 不 overwrite 第一次。
func TestInstaller_DefaultPin_AppendsExisting(t *testing.T) {
	pool := openDB(t)
	ctx := context.Background()
	a1 := newStubApp("test-pin-a").withDefaultPin()
	a2 := newStubApp("test-pin-b").withDefaultPin()
	reg := biuapp.NewRegistry(biuapp.Deps{})
	_ = reg.Register(ctx, a1)
	_ = reg.Register(ctx, a2)
	in := New(pool, reg, AllowAll{})

	_, scopeID := freshTenant()
	r1, err := in.Install(ctx, InstallRequest{
		Identifier: "test-pin-a", Scope: "user", ScopeID: scopeID, CallerUserID: scopeID,
	})
	if err != nil {
		t.Fatalf("install a: %v", err)
	}
	t.Cleanup(func() { _ = in.Uninstall(ctx, r1.ID, scopeID, "", nil) })

	r2, err := in.Install(ctx, InstallRequest{
		Identifier: "test-pin-b", Scope: "user", ScopeID: scopeID, CallerUserID: scopeID,
	})
	if err != nil {
		t.Fatalf("install b: %v", err)
	}
	t.Cleanup(func() { _ = in.Uninstall(ctx, r2.ID, scopeID, "", nil) })

	var raw []byte
	if err := pool.QueryRow(ctx, `
		SELECT items FROM app_center.sidebar_layouts
		 WHERE user_id = $1 AND scope = 'desktop'
	`, scopeID).Scan(&raw); err != nil {
		t.Fatalf("layout query: %v", err)
	}
	var items []map[string]any
	_ = json.Unmarshal(raw, &items)
	refs := map[string]bool{}
	for _, i := range items {
		if i["kind"] == "app" {
			refs[i["ref"].(string)] = true
		}
	}
	if !refs[r1.ID] || !refs[r2.ID] {
		t.Errorf("both installs should be pinned; got %v (full items: %s)", refs, raw)
	}
}

// preferred_position=top 时, 第二次装的 app 应该 prepend 到 layout
// 头部 (而不是 append) — 设计 §10A.9。
func TestInstaller_DefaultPin_TopPositionPrepends(t *testing.T) {
	pool := openDB(t)
	ctx := context.Background()
	first := newStubApp("test-pin-first").withDefaultPin()             // 默认 (middle/append)
	second := newStubApp("test-pin-top").withDefaultPinPosition("top") // top
	reg := biuapp.NewRegistry(biuapp.Deps{})
	_ = reg.Register(ctx, first)
	_ = reg.Register(ctx, second)
	in := New(pool, reg, AllowAll{})

	_, scopeID := freshTenant()
	r1, err := in.Install(ctx, InstallRequest{
		Identifier: "test-pin-first", Scope: "user", ScopeID: scopeID, CallerUserID: scopeID,
	})
	if err != nil {
		t.Fatalf("install first: %v", err)
	}
	t.Cleanup(func() { _ = in.Uninstall(ctx, r1.ID, scopeID, "", nil) })

	r2, err := in.Install(ctx, InstallRequest{
		Identifier: "test-pin-top", Scope: "user", ScopeID: scopeID, CallerUserID: scopeID,
	})
	if err != nil {
		t.Fatalf("install second(top): %v", err)
	}
	t.Cleanup(func() { _ = in.Uninstall(ctx, r2.ID, scopeID, "", nil) })

	var raw []byte
	if err := pool.QueryRow(ctx, `
		SELECT items FROM app_center.sidebar_layouts
		 WHERE user_id = $1 AND scope = 'desktop'
	`, scopeID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var items []map[string]any
	_ = json.Unmarshal(raw, &items)
	// 找 app 子序列 — 期望 r2 在前, r1 在后。
	appRefs := []string{}
	for _, i := range items {
		if i["kind"] == "app" {
			appRefs = append(appRefs, i["ref"].(string))
		}
	}
	if len(appRefs) != 2 {
		t.Fatalf("expected 2 app pins, got %d: %s", len(appRefs), raw)
	}
	if appRefs[0] != r2.ID || appRefs[1] != r1.ID {
		t.Errorf("position=top app should be first; got order [%s, %s], want [%s, %s]",
			appRefs[0], appRefs[1], r2.ID, r1.ID)
	}
}

// default_pin=false 的 app 装完不应该写 sidebar_layouts。
func TestInstaller_NoDefaultPin_LeavesSidebarUntouched(t *testing.T) {
	pool := openDB(t)
	ctx := context.Background()
	app := newStubApp("test-nopin") // 无 SidebarHints
	reg := biuapp.NewRegistry(biuapp.Deps{})
	_ = reg.Register(ctx, app)
	in := New(pool, reg, AllowAll{})

	_, scopeID := freshTenant()
	row, err := in.Install(ctx, InstallRequest{
		Identifier: "test-nopin", Scope: "user", ScopeID: scopeID, CallerUserID: scopeID,
	})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	t.Cleanup(func() { _ = in.Uninstall(ctx, row.ID, scopeID, "", nil) })

	var n int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM app_center.sidebar_layouts
		 WHERE user_id = $1 AND scope = 'desktop'
	`, scopeID).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 sidebar rows for app without default_pin, got %d", n)
	}
}
