package radar

import (
	"context"
	"crypto/sha256"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// End-to-end pipeline test against the real PG: insert a rule, push
// candidates through MatchBatch → FilterCooldown → WriteHits →
// Dispatch, verify watch_hits row + radar.hit_fired event.
func TestPipeline_EndToEnd(t *testing.T) {
	pool := openDB(t)
	store := NewStore(pool)
	ctx := context.Background()

	scope, scopeID := freshScope(t)

	rule, err := store.CreateRule(ctx, CreateRuleInput{
		Scope: scope, ScopeID: scopeID, Name: "pipeline test",
		MatchAny: []string{"OpenAI", "Anthropic"},
		OnHitBadge: "error",
		CooldownSec: 60,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.DeleteRule(ctx, scope, scopeID, rule.ID) })

	candidates := []Candidate{
		{Source: "rss:test", Title: "OpenAI 发布 GPT-X", URL: "https://example.com/1",
			TitleHash: hash("OpenAI 发布 GPT-X"),
			OwnerScope: scope, OwnerScopeID: scopeID},
		{Source: "weibo", Title: "Anthropic Claude 5 发布",
			TitleHash: hash("Anthropic Claude 5 发布"),
			URL: "https://s.weibo.com/x"},
		{Source: "weibo", Title: "国务院招聘", // no match
			TitleHash: hash("国务院招聘"),
			URL: "https://s.weibo.com/y"},
	}

	rules, _ := store.ListEnabledRulesAll(ctx)
	hits := MatchBatch(ctx, rules, candidates)
	mine := filterRule(hits, rule.ID)
	if len(mine) != 2 {
		t.Errorf("matcher: %d hits for rule, want 2", len(mine))
	}

	survived, err := store.FilterCooldown(ctx, mine)
	if err != nil {
		t.Fatal(err)
	}
	if len(survived) != 2 {
		t.Errorf("first cooldown should pass all; got %d", len(survived))
	}

	written, err := store.WriteHits(ctx, survived)
	if err != nil {
		t.Fatal(err)
	}
	if len(written) != 2 || written[0].ID == 0 {
		t.Errorf("write: %+v", written)
	}

	// Replay — cooldown blocks duplicates.
	survived2, _ := store.FilterCooldown(ctx, mine)
	if len(survived2) != 0 {
		t.Errorf("cooldown should suppress repeat; got %d", len(survived2))
	}

	// Dispatcher → events row + notified flag.
	disp := NewDispatcher(pool)
	if err := disp.Dispatch(ctx, written); err != nil {
		t.Fatal(err)
	}

	// Hits should be marked notified.
	hits2, _ := store.ListHits(ctx, scope, scopeID, ListHitsOpts{})
	for _, h := range hits2 {
		if !h.Notified {
			t.Errorf("hit %d not notified", h.ID)
		}
	}

	// radar.hit_fired event landed.
	var n int
	pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM app_center.events
		 WHERE event_type = 'radar.hit_fired'
		   AND scope = $1`, scope+":"+scopeID).Scan(&n)
	if n != 2 {
		t.Errorf("expected 2 radar.hit_fired events, got %d", n)
	}

	// SDK projection (join with rule name + severity).
	hWithRule, err := store.ListHitsWithRule(ctx, scope, scopeID, ListHitsOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(hWithRule) != 2 {
		t.Fatalf("ListHitsWithRule = %d", len(hWithRule))
	}
	if hWithRule[0].RuleName != "pipeline test" {
		t.Errorf("rule_name = %q", hWithRule[0].RuleName)
	}
	if hWithRule[0].HitSeverity != "error" {
		t.Errorf("severity = %q", hWithRule[0].HitSeverity)
	}

	// Cleanup events
	pool.Exec(ctx, `DELETE FROM app_center.events WHERE scope = $1`, scope+":"+scopeID)
}

// TestPipeline_BurstAggregate verifies AggregateForBurst caps per-rule.
func TestPipeline_BurstAggregate(t *testing.T) {
	r := uuid.New()
	hits := make([]Hit, 20)
	for i := range hits {
		hits[i] = Hit{RuleID: r, Title: "x", TitleHash: hash("x")}
	}
	out := AggregateForBurst(hits)
	if len(out) != 5 {
		t.Errorf("expected 5 (threshold), got %d", len(out))
	}
}

func hash(s string) []byte {
	h := sha256.Sum256([]byte(strings.ToLower(s)))
	return h[:]
}

func filterRule(hits []Hit, id uuid.UUID) []Hit {
	out := make([]Hit, 0)
	for _, h := range hits {
		if h.RuleID == id {
			out = append(out, h)
		}
	}
	return out
}
