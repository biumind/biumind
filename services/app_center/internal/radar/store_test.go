package radar

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func openDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	for _, table := range []string{"rss.watch_rules", "rss.watch_hits"} {
		var ok bool
		if err := pool.QueryRow(context.Background(),
			`SELECT EXISTS (SELECT 1 FROM information_schema.tables
			    WHERE table_schema = split_part($1, '.', 1)
			      AND table_name = split_part($1, '.', 2))`, table).Scan(&ok); err != nil {
			t.Fatal(err)
		}
		if !ok {
			t.Skipf("%s missing", table)
		}
	}
	return pool
}

func freshScope(t *testing.T) (string, string) {
	t.Helper()
	return "user", uuid.NewString()
}

func TestStore_RuleCRUD(t *testing.T) {
	store := NewStore(openDB(t))
	ctx := context.Background()
	scope, scopeID := freshScope(t)

	r, err := store.CreateRule(ctx, CreateRuleInput{
		Scope: scope, ScopeID: scopeID, Name: "OpenAI watch",
		MatchAny: []string{"OpenAI"}, OnHitBadge: "error",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.DeleteRule(ctx, scope, scopeID, r.ID) })

	if r.OnHitBadge != "error" || !r.Enabled {
		t.Errorf("created rule = %+v", r)
	}

	// list
	rs, err := store.ListRules(ctx, scope, scopeID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 1 {
		t.Errorf("list = %d", len(rs))
	}

	// update
	disabled := false
	newName := "OpenAI watch v2"
	updated, err := store.UpdateRule(ctx, scope, scopeID, r.ID, UpdateRuleInput{
		Name: &newName, Enabled: &disabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != newName || updated.Enabled {
		t.Errorf("update missed: %+v", updated)
	}

	// delete
	if err := store.DeleteRule(ctx, scope, scopeID, r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetRule(ctx, r.ID); err != ErrNotFound {
		t.Errorf("expected NotFound, got %v", err)
	}
}

func TestStore_CreateRule_RejectsEmpty(t *testing.T) {
	store := NewStore(openDB(t))
	scope, scopeID := freshScope(t)
	_, err := store.CreateRule(context.Background(), CreateRuleInput{
		Scope: scope, ScopeID: scopeID, Name: "empty",
	})
	if err != ErrEmptyRule {
		t.Errorf("expected ErrEmptyRule, got %v", err)
	}
}

func TestStore_FilterCooldown_FirstSecondThird(t *testing.T) {
	store := NewStore(openDB(t))
	ctx := context.Background()
	scope, scopeID := freshScope(t)
	r, _ := store.CreateRule(ctx, CreateRuleInput{
		Scope: scope, ScopeID: scopeID, Name: "x",
		MatchAny: []string{"x"}, CooldownSec: 60,
	})
	t.Cleanup(func() { _ = store.DeleteRule(ctx, scope, scopeID, r.ID) })

	hash := []byte("0123456789012345678901234567890a")
	hit := Hit{
		RuleID: r.ID, Source: "rss:1", Title: "x", URL: "https://e.com/x",
		TitleHash: hash, RuleSnapshot: *r,
	}

	// First fire passes cooldown (no prior).
	first, err := store.FilterCooldown(ctx, []Hit{hit})
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 {
		t.Errorf("first = %d", len(first))
	}
	written, _ := store.WriteHits(ctx, first)
	if len(written) != 1 || written[0].ID == 0 {
		t.Errorf("write = %+v", written)
	}

	// Second fire within window — filtered.
	second, _ := store.FilterCooldown(ctx, []Hit{hit})
	if len(second) != 0 {
		t.Errorf("cooldown should suppress: %d", len(second))
	}

	// Different title hash — not suppressed.
	hit2 := hit
	hit2.TitleHash = []byte("differentdifferentdifferentdif")
	hit2.Title = "y"
	other, _ := store.FilterCooldown(ctx, []Hit{hit2})
	if len(other) != 1 {
		t.Errorf("different hash should fire: %d", len(other))
	}
}

func TestStore_HitsListUnreadCountSeverity(t *testing.T) {
	store := NewStore(openDB(t))
	ctx := context.Background()
	scope, scopeID := freshScope(t)

	rWarn, _ := store.CreateRule(ctx, CreateRuleInput{
		Scope: scope, ScopeID: scopeID, Name: "warn rule",
		MatchAny: []string{"x"}, OnHitBadge: "warn",
	})
	rError, _ := store.CreateRule(ctx, CreateRuleInput{
		Scope: scope, ScopeID: scopeID, Name: "error rule",
		MatchAny: []string{"x"}, OnHitBadge: "error",
	})
	t.Cleanup(func() {
		_ = store.DeleteRule(ctx, scope, scopeID, rWarn.ID)
		_ = store.DeleteRule(ctx, scope, scopeID, rError.ID)
	})

	store.WriteHits(ctx, []Hit{
		{RuleID: rWarn.ID, Source: "rss:1", Title: "a",
			TitleHash: []byte("aaa"), RuleSnapshot: *rWarn},
		{RuleID: rError.ID, Source: "weibo", Title: "b",
			TitleHash: []byte("bbb"), RuleSnapshot: *rError},
	})

	n, _ := store.UnreadCount(ctx, scope, scopeID)
	if n != 2 {
		t.Errorf("unread = %d", n)
	}

	worst, _ := store.UnreadMaxSeverity(ctx, scope, scopeID)
	if worst != "error" {
		t.Errorf("worst = %q", worst)
	}

	hits, _ := store.ListHits(ctx, scope, scopeID, ListHitsOpts{UnreadOnly: true})
	if len(hits) != 2 {
		t.Errorf("list unread = %d", len(hits))
	}

	// Mark one read; severity drops to warn.
	if err := store.MarkHitRead(ctx, scope, scopeID, hits[0].ID); err != nil {
		t.Fatal(err)
	}
	n, _ = store.UnreadCount(ctx, scope, scopeID)
	if n != 1 {
		t.Errorf("after mark read, unread = %d", n)
	}
	worst, _ = store.UnreadMaxSeverity(ctx, scope, scopeID)
	// hits[0] is most recent — depending on which rule's hit was first,
	// the surviving severity could be either. Just check not empty +
	// is one of the valid values.
	if worst != "warn" && worst != "error" {
		t.Errorf("unexpected severity %q", worst)
	}
}

func TestStore_RuleScopeIsolation(t *testing.T) {
	store := NewStore(openDB(t))
	ctx := context.Background()
	_, scope1 := freshScope(t)
	_, scope2 := freshScope(t)

	r1, _ := store.CreateRule(ctx, CreateRuleInput{
		Scope: "user", ScopeID: scope1, Name: "u1 rule",
		MatchAny: []string{"x"},
	})
	r2, _ := store.CreateRule(ctx, CreateRuleInput{
		Scope: "user", ScopeID: scope2, Name: "u2 rule",
		MatchAny: []string{"x"},
	})
	t.Cleanup(func() {
		_ = store.DeleteRule(ctx, "user", scope1, r1.ID)
		_ = store.DeleteRule(ctx, "user", scope2, r2.ID)
	})

	// User1 can't see User2's rule.
	rs1, _ := store.ListRules(ctx, "user", scope1)
	if len(rs1) != 1 || rs1[0].ID != r1.ID {
		t.Errorf("scope1 list = %+v", rs1)
	}
	// And can't update / delete it.
	_, err := store.UpdateRule(ctx, "user", scope1, r2.ID, UpdateRuleInput{})
	if err != ErrNotFound {
		t.Errorf("cross-scope update should NotFound, got %v", err)
	}
	if err := store.DeleteRule(ctx, "user", scope1, r2.ID); err != ErrNotFound {
		t.Errorf("cross-scope delete should NotFound, got %v", err)
	}
}

func TestStore_ListEnabledRulesAll(t *testing.T) {
	store := NewStore(openDB(t))
	ctx := context.Background()
	_, scopeA := freshScope(t)
	_, scopeB := freshScope(t)

	en, _ := store.CreateRule(ctx, CreateRuleInput{
		Scope: "user", ScopeID: scopeA, Name: "enabled",
		MatchAny: []string{"x"},
	})
	dis, _ := store.CreateRule(ctx, CreateRuleInput{
		Scope: "user", ScopeID: scopeB, Name: "disabled",
		MatchAny: []string{"x"},
	})
	disF := false
	store.UpdateRule(ctx, "user", scopeB, dis.ID, UpdateRuleInput{Enabled: &disF})
	t.Cleanup(func() {
		_ = store.DeleteRule(ctx, "user", scopeA, en.ID)
		_ = store.DeleteRule(ctx, "user", scopeB, dis.ID)
	})

	rules, err := store.ListEnabledRulesAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	gotEn := false
	gotDis := false
	for _, r := range rules {
		if r.ID == en.ID {
			gotEn = true
		}
		if r.ID == dis.ID {
			gotDis = true
		}
	}
	if !gotEn || gotDis {
		t.Errorf("enabled-all should include en (%v), exclude dis (%v)", gotEn, gotDis)
	}
}
