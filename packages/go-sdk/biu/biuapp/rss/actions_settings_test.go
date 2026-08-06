package rss

import (
	"context"
	"encoding/json"
	"testing"
)

func prefsReady(t *testing.T, a *App) {
	t.Helper()
	var exists bool
	if err := a.pg.pool.QueryRow(context.Background(),
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables
		   WHERE table_schema='rss' AND table_name='user_preferences')`).Scan(&exists); err != nil {
		t.Fatalf("table check: %v", err)
	}
	if !exists {
		t.Skip("rss.user_preferences missing; apply migration 00019")
	}
}

func TestUserPrefs_GetDefaultEmpty(t *testing.T) {
	a := newPGApp(t)
	prefsReady(t, a)
	ctx := withCaller(context.Background(), "user-"+t.Name())
	out, err := a.Invoke(ctx, "user_prefs_get", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	cfg := out.(map[string]any)["config"].(map[string]any)
	if len(cfg) != 0 {
		t.Fatalf("expected empty default prefs, got %v", cfg)
	}
}

func TestUserPrefs_UpdateRoundTrip(t *testing.T) {
	a := newPGApp(t)
	prefsReady(t, a)
	ctx := withCaller(context.Background(), "user-"+t.Name())
	if _, err := a.Invoke(ctx, "user_prefs_update",
		json.RawMessage(`{"config":{"refresh_min":60,"ai_digest":false}}`)); err != nil {
		t.Fatal(err)
	}
	out, err := a.Invoke(ctx, "user_prefs_get", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	cfg := out.(map[string]any)["config"].(map[string]any)
	if cfg["refresh_min"].(float64) != 60 || cfg["ai_digest"].(bool) != false {
		t.Fatalf("round-trip mismatch: %v", cfg)
	}
}
