package rss

import (
	"context"
	"encoding/json"
	"testing"
)

// shareReady skips when the shared_views table (migration 00017) isn't
// applied to the test DB.
func shareReady(t *testing.T, a *App) {
	t.Helper()
	var exists bool
	if err := a.pg.pool.QueryRow(context.Background(),
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables
		   WHERE table_schema='rss' AND table_name='shared_views')`).Scan(&exists); err != nil {
		t.Fatalf("table check: %v", err)
	}
	if !exists {
		t.Skip("rss.shared_views missing; apply migration 00017")
	}
}

func TestShares_CreateListRevoke(t *testing.T) {
	a := newPGApp(t)
	shareReady(t, a)
	ctx := withCaller(context.Background(), "user-"+t.Name())

	// create
	out, err := a.Invoke(ctx, "shares_create", json.RawMessage(`{"view_kind":"today"}`))
	if err != nil {
		t.Fatal(err)
	}
	m := out.(map[string]any)
	token := m["token"].(string)
	if token == "" {
		t.Fatal("empty token")
	}
	if url, _ := m["url"].(string); url == "" {
		t.Fatal("empty url")
	}

	// list → should contain the token, active
	lst, err := a.Invoke(ctx, "shares_list", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	items := lst.(map[string]any)["items"].([]map[string]any)
	found := false
	for _, it := range items {
		if it["token"] == token {
			found = true
			if it["active"] != true {
				t.Error("new share not active")
			}
		}
	}
	if !found {
		t.Fatal("created share not in list")
	}

	// revoke
	if _, err := a.Invoke(ctx, "shares_revoke",
		json.RawMessage(`{"token":"`+token+`"}`)); err != nil {
		t.Fatal(err)
	}

	// double-revoke → ErrNotFound
	if _, err := a.Invoke(ctx, "shares_revoke",
		json.RawMessage(`{"token":"`+token+`"}`)); err == nil {
		t.Fatal("expected error revoking already-revoked share")
	}
}

func TestShares_CreateRejectsUnknownKind(t *testing.T) {
	a := newPGApp(t)
	shareReady(t, a)
	ctx := withCaller(context.Background(), "user-"+t.Name())
	if _, err := a.Invoke(ctx, "shares_create",
		json.RawMessage(`{"view_kind":"bogus"}`)); err == nil {
		t.Fatal("expected error for unknown view_kind")
	}
}
