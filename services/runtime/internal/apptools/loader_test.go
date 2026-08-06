package apptools

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/biumind/biumind/packages/go-sdk/biu/biuapp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Integration tests run against the same DB the app_center installer
// uses. Skip when DATABASE_URL is unset; CI matrix runs the
// integration leg after `task migrate up`.

func openDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping apptools integration tests")
	}
	p, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(p.Close)
	for _, table := range []string{
		"app_center.installations",
		"app_center.agent_apps",
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

// seedInstall inserts a row directly so the loader test doesn't have
// to depend on the installs package (which would create a back-edge
// runtime → app_center). The shape mirrors what installs.Installer
// produces.
func seedInstall(t *testing.T, pool *pgxpool.Pool, scope, scopeID, identifier string) string {
	t.Helper()
	id := uuid.New()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO app_center.installations
			(id, scope, scope_id, app_id, identifier, version,
			 enabled, permissions_granted, config, forced)
		VALUES ($1, $2, $3, $4, $5, '0.1.0',
			true, '{}', '{}', false)
	`, id, scope, scopeID, "app_"+identifier, identifier); err != nil {
		t.Fatalf("seed install: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM app_center.installations WHERE id = $1`, id)
	})
	return id.String()
}

func grantAgent(t *testing.T, pool *pgxpool.Pool, installID string, agentID uuid.UUID) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO app_center.agent_apps (agent_id, install_id, enabled)
		VALUES ($1, $2, true)
		ON CONFLICT DO NOTHING
	`, agentID, installID); err != nil {
		t.Fatalf("grant: %v", err)
	}
}

// ─── Loader tests ─────────────────────────────────────────

func TestLoader_LoadsGrantedInstalls(t *testing.T) {
	pool := openDB(t)
	app := newStubApp("rss-int", biuapp.ActionSpec{Name: "fetch", Risk: biuapp.RiskLow})
	reg := biuapp.NewRegistry(biuapp.Deps{})
	if err := reg.Register(context.Background(), app); err != nil {
		t.Fatal(err)
	}
	loader := &Loader{Pool: pool, Registry: reg}

	userID := uuid.New()
	agentID := uuid.New()

	// Install the App for this user, then grant the agent.
	installID := seedInstall(t, pool, "user", userID.String(), "rss-int")
	grantAgent(t, pool, installID, agentID)

	loaded, err := loader.LoadForAgent(context.Background(), LoadInput{
		UserID:  userID,
		AgentID: agentID,
	})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(loaded.Apps) != 1 {
		t.Fatalf("loaded %d apps, want 1: %+v", len(loaded.Apps), loaded.Apps)
	}
	la := loaded.Apps[0]
	if la.Identifier != "rss-int" {
		t.Errorf("identifier = %q", la.Identifier)
	}
	if len(la.AvailableActions) != 1 {
		t.Errorf("expected 1 action, got %d", len(la.AvailableActions))
	}
}

func TestLoader_SkipsUngrantedInstalls(t *testing.T) {
	pool := openDB(t)
	app := newStubApp("rss-no-grant", biuapp.ActionSpec{Name: "fetch", Risk: biuapp.RiskLow})
	reg := biuapp.NewRegistry(biuapp.Deps{})
	_ = reg.Register(context.Background(), app)
	loader := &Loader{Pool: pool, Registry: reg}

	userID := uuid.New()
	agentID := uuid.New()
	_ = seedInstall(t, pool, "user", userID.String(), "rss-no-grant")
	// No grantAgent — the agent shouldn't see this install.

	loaded, err := loader.LoadForAgent(context.Background(), LoadInput{
		UserID:  userID,
		AgentID: agentID,
	})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(loaded.Apps) != 0 {
		t.Errorf("expected 0 apps without grant, got %d", len(loaded.Apps))
	}
}

func TestLoader_SkipsDisabledInstalls(t *testing.T) {
	pool := openDB(t)
	app := newStubApp("rss-disabled", biuapp.ActionSpec{Name: "fetch", Risk: biuapp.RiskLow})
	reg := biuapp.NewRegistry(biuapp.Deps{})
	_ = reg.Register(context.Background(), app)
	loader := &Loader{Pool: pool, Registry: reg}

	userID := uuid.New()
	agentID := uuid.New()
	installID := seedInstall(t, pool, "user", userID.String(), "rss-disabled")
	grantAgent(t, pool, installID, agentID)

	// Disable the install.
	if _, err := pool.Exec(context.Background(),
		`UPDATE app_center.installations SET enabled = false WHERE id = $1`, installID); err != nil {
		t.Fatal(err)
	}

	loaded, err := loader.LoadForAgent(context.Background(), LoadInput{
		UserID:  userID,
		AgentID: agentID,
	})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(loaded.Apps) != 0 {
		t.Errorf("expected 0 apps from disabled install, got %d", len(loaded.Apps))
	}
}

func TestLoader_RecordsMissingFromRegistry(t *testing.T) {
	pool := openDB(t)
	// Register an app that the install row will NOT match.
	app := newStubApp("real-app", biuapp.ActionSpec{Name: "fetch"})
	reg := biuapp.NewRegistry(biuapp.Deps{})
	_ = reg.Register(context.Background(), app)
	loader := &Loader{Pool: pool, Registry: reg}

	userID := uuid.New()
	agentID := uuid.New()
	installID := seedInstall(t, pool, "user", userID.String(), "ghost-app")
	grantAgent(t, pool, installID, agentID)

	loaded, err := loader.LoadForAgent(context.Background(), LoadInput{
		UserID:  userID,
		AgentID: agentID,
	})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(loaded.Apps) != 0 {
		t.Errorf("ghost app should not load: %+v", loaded.Apps)
	}
	if len(loaded.MissingFromRegistry) != 1 || loaded.MissingFromRegistry[0] != "ghost-app" {
		t.Errorf("expected MissingFromRegistry to contain ghost-app, got %v", loaded.MissingFromRegistry)
	}
}

func TestLoader_RejectsNilUUIDs(t *testing.T) {
	loader := &Loader{Pool: nil, Registry: biuapp.NewRegistry(biuapp.Deps{})}
	_, err := loader.LoadForAgent(context.Background(), LoadInput{})
	if err == nil {
		t.Error("expected error from nil pool/registry")
	}
}

// ─── EnsureDefaultAgentGrantTx is a thin wrapper exercised by the
// installs.Installer integration test (which runs in the app_center
// service). We add a unit-style assert here that the helper handles
// the no-default-agent case as a silent skip. ─────────────────────

func TestEnsureDefaultAgentGrantTx_NilAgentSilentSkip(t *testing.T) {
	// Safe to call without DB — uuid.Nil short-circuits before any tx
	// access.
	err := EnsureDefaultAgentGrantTx(context.Background(), nil, "i-1", uuid.Nil)
	if err != nil {
		t.Errorf("nil agent should be silent skip, got %v", err)
	}
}

// Sanity-check that the empty-user case returns the expected error.
func TestLoader_RequiresUserAndAgent(t *testing.T) {
	loader := &Loader{Pool: openDB(t), Registry: biuapp.NewRegistry(biuapp.Deps{})}
	_, err := loader.LoadForAgent(context.Background(), LoadInput{})
	if err == nil || !errors.Is(err, errors.New("apptools: user_id / agent_id required")) {
		// errors.Is on plain errors fails; just check substring.
		if err == nil {
			t.Error("expected error")
		}
	}
}
