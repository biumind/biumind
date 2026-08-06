package radar

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func mkRule(any, all, exclude, sources []string) *Rule {
	return &Rule{
		ID:          uuid.New(),
		Scope:       "user",
		ScopeID:     "u1",
		Name:        "test",
		MatchAny:    any,
		MatchAll:    all,
		Exclude:     exclude,
		Sources:     sources,
		OnHitBadge:  "warn",
		CooldownSec: 60,
		Enabled:     true,
	}
}

func mkCand(source, title string) Candidate {
	return Candidate{Source: source, Title: title, URL: "https://e.com/x"}
}

func TestMatch_AnyKeyword(t *testing.T) {
	r := mkRule([]string{"OpenAI", "Anthropic"}, nil, nil, []string{"*"})
	if !MatchOne(r, &Candidate{Source: "weibo", Title: "OpenAI 发新模型"}) {
		t.Error("should match openai")
	}
	if !MatchOne(r, &Candidate{Source: "weibo", Title: "anthropic 发新模型"}) {
		t.Error("case-insensitive")
	}
	if MatchOne(r, &Candidate{Source: "weibo", Title: "Google 发新模型"}) {
		t.Error("should not match")
	}
}

func TestMatch_AllKeywords(t *testing.T) {
	r := mkRule(nil, []string{"芯片", "出口管制"}, nil, []string{"*"})
	if !MatchOne(r, &Candidate{Source: "weibo", Title: "美国对华芯片出口管制升级"}) {
		t.Error("should match")
	}
	if MatchOne(r, &Candidate{Source: "weibo", Title: "美国对华芯片政策"}) {
		t.Error("missing 出口管制")
	}
}

func TestMatch_AnyAndAll(t *testing.T) {
	r := mkRule([]string{"AI", "ML"}, []string{"中国", "监管"}, nil, []string{"*"})
	if !MatchOne(r, &Candidate{Source: "x", Title: "AI 监管在中国如何落地"}) {
		t.Error("should match (AI any + 中国监管 all)")
	}
	if MatchOne(r, &Candidate{Source: "x", Title: "中国监管框架"}) {
		t.Error("missing any")
	}
	if MatchOne(r, &Candidate{Source: "x", Title: "AI 在美国"}) {
		t.Error("missing all")
	}
}

func TestMatch_Exclude(t *testing.T) {
	r := mkRule([]string{"OpenAI"}, nil, []string{"招聘", "intern"}, []string{"*"})
	if !MatchOne(r, &Candidate{Source: "x", Title: "OpenAI 发新模型"}) {
		t.Error("should match")
	}
	if MatchOne(r, &Candidate{Source: "x", Title: "OpenAI 招聘工程师"}) {
		t.Error("excluded by 招聘")
	}
	if MatchOne(r, &Candidate{Source: "x", Title: "OpenAI Intern Program"}) {
		t.Error("excluded by intern (case insensitive)")
	}
}

func TestMatch_SourceWildcard(t *testing.T) {
	r := mkRule([]string{"x"}, nil, nil, []string{"*"})
	if !MatchOne(r, &Candidate{Source: "weibo", Title: "x"}) {
		t.Error("wildcard should match weibo")
	}
	if !MatchOne(r, &Candidate{Source: "rss:abc", Title: "x"}) {
		t.Error("wildcard should match rss")
	}
}

func TestMatch_SourceLimited(t *testing.T) {
	r := mkRule([]string{"x"}, nil, nil, []string{"weibo", "zhihu"})
	if !MatchOne(r, &Candidate{Source: "weibo", Title: "x"}) {
		t.Error("weibo allowed")
	}
	if MatchOne(r, &Candidate{Source: "baidu", Title: "x"}) {
		t.Error("baidu not in source list")
	}
	if MatchOne(r, &Candidate{Source: "rss:abc", Title: "x"}) {
		t.Error("rss not in source list")
	}
}

func TestMatch_DisabledRule(t *testing.T) {
	r := mkRule([]string{"x"}, nil, nil, []string{"*"})
	r.Enabled = false
	if MatchOne(r, &Candidate{Source: "weibo", Title: "x"}) {
		t.Error("disabled rule must not match")
	}
}

func TestMatch_EmptyKeywords(t *testing.T) {
	r := mkRule(nil, nil, nil, []string{"*"})
	if MatchOne(r, &Candidate{Source: "weibo", Title: "x"}) {
		t.Error("empty rule must not fire-hose")
	}
}

func TestMatchBatch_ScopeIsolation(t *testing.T) {
	rUser1 := mkRule([]string{"x"}, nil, nil, []string{"*"})
	rUser1.ScopeID = "u1"
	rUser2 := mkRule([]string{"x"}, nil, nil, []string{"*"})
	rUser2.ScopeID = "u2"

	// RSS candidate scoped to u1 — only u1's rule fires.
	rssCand := Candidate{Source: "rss:abc", Title: "x", OwnerScope: "user", OwnerScopeID: "u1"}
	hits := MatchBatch(context.Background(), []*Rule{rUser1, rUser2}, []Candidate{rssCand})
	if len(hits) != 1 || hits[0].RuleID != rUser1.ID {
		t.Errorf("scope isolation broken; hits = %+v", hits)
	}

	// Board candidate (no scope) — both rules fire.
	boardCand := Candidate{Source: "weibo", Title: "x"}
	hits = MatchBatch(context.Background(), []*Rule{rUser1, rUser2}, []Candidate{boardCand})
	if len(hits) != 2 {
		t.Errorf("board should hit both users; got %d", len(hits))
	}
}

func TestMatchBatch_BulkPerformance(t *testing.T) {
	// Modest bench: 100 rules × 1000 candidates = 100k checks.
	rules := make([]*Rule, 100)
	for i := range rules {
		rules[i] = mkRule([]string{"AI", "GPT"}, nil, []string{"招聘"}, []string{"*"})
	}
	cands := make([]Candidate, 1000)
	for i := range cands {
		cands[i] = mkCand("weibo", "OpenAI GPT-5 发布会")
	}
	hits := MatchBatch(context.Background(), rules, cands)
	if len(hits) != 100*1000 {
		t.Errorf("expected 100k hits, got %d", len(hits))
	}
}

func TestSemanticFallbackMatch(t *testing.T) {
	r := &Rule{
		Enabled:       true,
		Sources:       []string{"*"},
		SemanticQuery: "OpenAI 发布新模型 EU AI 监管",
	}
	cases := []struct {
		title string
		want  bool
	}{
		{"OpenAI 今天发布 GPT-5", true},
		{"EU 通过 AI Act", true},
		{"早餐吃什么", false},
		{"AI 监管最新进展", true},
	}
	for _, c := range cases {
		got := MatchOne(r, &Candidate{Source: "weibo", Title: c.title})
		if got != c.want {
			t.Errorf("title=%q got %v want %v", c.title, got, c.want)
		}
	}
}

func TestTokenizeSemanticQuery(t *testing.T) {
	out := tokenizeSemanticQuery("凡是关于 OpenAI / GPT-5 的资讯都通知我")
	// Should contain OpenAI, GPT-5 (latin) + 资讯 (CJK bigram)
	have := map[string]bool{}
	for _, t := range out {
		have[t] = true
	}
	if !have["OpenAI"] {
		t.Errorf("missing OpenAI in %v", out)
	}
	// "资讯" 2-char bigram or "资" "讯" individual chars; both fine
	if !have["资讯"] && !have["资"] {
		t.Errorf("missing 资讯 in %v", out)
	}
}

func TestTokenizeSemanticQuery_Dedup(t *testing.T) {
	out := tokenizeSemanticQuery("AI AI AI 监管")
	count := 0
	for _, t := range out {
		if t == "AI" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("AI should appear once, got %d in %v", count, out)
	}
}

func TestMaxSeverity(t *testing.T) {
	cases := []struct{ a, b, want string }{
		{"", "info", "info"},
		{"info", "warn", "warn"},
		{"warn", "info", "warn"},
		{"warn", "error", "error"},
		{"error", "info", "error"},
		{"", "", ""},
	}
	for _, tc := range cases {
		if got := MaxSeverity(tc.a, tc.b); got != tc.want {
			t.Errorf("MaxSeverity(%q,%q) = %q want %q", tc.a, tc.b, got, tc.want)
		}
	}
}
