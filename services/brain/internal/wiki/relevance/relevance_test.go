package relevance

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

// ── helper builders ───────────────────────────────────────────

func mkPage(id uuid.UUID, pageType string, links ...uuid.UUID) *PageNode {
	out := map[uuid.UUID]struct{}{}
	for _, l := range links {
		out[l] = struct{}{}
	}
	return &PageNode{
		ID: id, NormTitle: id.String()[:8], Type: pageType,
		OutgoingIDs: out,
	}
}

func mkGraph(nodes ...*PageNode) *ProjectGraph {
	pages := map[uuid.UUID]*PageNode{}
	for _, n := range nodes {
		pages[n.ID] = n
	}
	return &ProjectGraph{Pages: pages}
}

// ── source overlap signal (P1-4) ──────────────────────────────

func TestSourceIntersect(t *testing.T) {
	s1, s2, s3 := uuid.New(), uuid.New(), uuid.New()
	a := map[uuid.UUID]struct{}{s1: {}, s2: {}}
	b := map[uuid.UUID]struct{}{s1: {}, s3: {}}
	if n := sourceIntersect(a, b); n != 1 {
		t.Errorf("intersect = %d, want 1 (shared s1)", n)
	}
	if n := sourceIntersect(a, nil); n != 0 {
		t.Errorf("intersect nil = %d, want 0", n)
	}
	c := map[uuid.UUID]struct{}{s3: {}}
	if n := sourceIntersect(a, c); n != 0 {
		t.Errorf("intersect disjoint = %d, want 0", n)
	}
}

func TestScore_SourceOverlap(t *testing.T) {
	shared := uuid.New()
	a, b := uuid.New(), uuid.New()
	pa := mkPage(a, "concept")
	pa.Sources = map[uuid.UUID]struct{}{shared: {}}
	pb := mkPage(b, "entity")
	pb.Sources = map[uuid.UUID]struct{}{shared: {}}
	// 无直连、无共邻居 → 只 source overlap 命中（type affinity 单独检查不干扰）
	adj := map[uuid.UUID]map[uuid.UUID]struct{}{}
	degree := map[uuid.UUID]int{}
	_, signals := scorePair(pa, pb, adj, degree, ScoreOptions{}.withDefaults())
	if got := signals["source_overlap"]; got != 4.0 {
		t.Errorf("source_overlap = %v, want 4.0 (1 shared × WeightSourceOverlap)", got)
	}

	// 两页共享 2 source → 2 × 4.0 = 8.0
	sX := uuid.New()
	pa.Sources[sX] = struct{}{}
	pb.Sources[sX] = struct{}{}
	_, signals = scorePair(pa, pb, adj, degree, ScoreOptions{}.withDefaults())
	if got := signals["source_overlap"]; got != 8.0 {
		t.Errorf("source_overlap (2 shared) = %v, want 8.0", got)
	}
}

// ── direct link signal ────────────────────────────────────────

func TestScore_DirectLinkBoth(t *testing.T) {
	a := uuid.New()
	b := uuid.New()
	g := mkGraph(
		mkPage(a, "concept", b),
		mkPage(b, "entity"),
	)
	pairs := ScoreAll(g, ScoreOptions{MinScore: 0.01})
	if len(pairs) != 1 {
		t.Fatalf("want 1 pair, got %d", len(pairs))
	}
	got := pairs[0]
	if got.Signals["direct_link"] == 0 {
		t.Errorf("direct_link should fire when a→b: %v", got.Signals)
	}
	if got.Score < WeightDirectLink {
		t.Errorf("score should be ≥ direct-link weight, got %v", got.Score)
	}
}

func TestScore_DirectLinkSymmetric(t *testing.T) {
	// Edge in either direction triggers direct_link.
	a := uuid.New()
	b := uuid.New()
	g1 := mkGraph(mkPage(a, "concept", b), mkPage(b, "entity"))
	g2 := mkGraph(mkPage(a, "concept"), mkPage(b, "entity", a))
	p1 := ScoreAll(g1, ScoreOptions{MinScore: 0.01})
	p2 := ScoreAll(g2, ScoreOptions{MinScore: 0.01})
	if len(p1) != 1 || len(p2) != 1 {
		t.Fatalf("want 1 pair each, got %d / %d", len(p1), len(p2))
	}
	if p1[0].Score != p2[0].Score {
		t.Errorf("a→b and b→a should score identically: %v vs %v",
			p1[0].Score, p2[0].Score)
	}
}

// ── adamic-adar signal ────────────────────────────────────────

func TestScore_CommonNeighborBoostsSimilarity(t *testing.T) {
	// a — c — b: a and b share neighbour c, so adamic_adar fires
	// even though a and b don't link directly.
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	g := mkGraph(
		mkPage(a, "concept", c),
		mkPage(b, "concept", c),
		mkPage(c, "entity"),
	)
	pairs := ScoreAll(g, ScoreOptions{MinScore: 0.01})
	// We expect 3 pairs: (a,b), (a,c), (b,c). (a,c) and (b,c) have
	// direct links; (a,b) only has the common-neighbour signal.
	var ab *PairScore
	for i := range pairs {
		if (pairs[i].PageA == a && pairs[i].PageB == b) ||
			(pairs[i].PageA == b && pairs[i].PageB == a) {
			ab = &pairs[i]
			break
		}
	}
	if ab == nil {
		t.Fatalf("(a,b) pair missing — common neighbour signal should fire")
	}
	if ab.Signals["adamic_adar"] == 0 {
		t.Errorf("expected adamic_adar > 0, got %v", ab.Signals)
	}
	if ab.Signals["direct_link"] != 0 {
		t.Errorf("(a,b) shouldn't show direct_link, got %v", ab.Signals)
	}
}

func TestScore_HighDegreeHubContributesLess(t *testing.T) {
	// Two scenarios sharing one common neighbour. In scenario 1 the
	// neighbour has degree 2 (just a + b). In scenario 2 the neighbour
	// has degree 5 (a + b + c + d + e). Adamic-Adar(scenario 1) >
	// Adamic-Adar(scenario 2) because high-degree hubs are penalised.
	a, b := uuid.New(), uuid.New()
	relay := uuid.New()
	c, d, e := uuid.New(), uuid.New(), uuid.New()

	g1 := mkGraph(
		mkPage(a, "concept", relay),
		mkPage(b, "concept", relay),
		mkPage(relay, "entity"),
	)
	g2 := mkGraph(
		mkPage(a, "concept", relay),
		mkPage(b, "concept", relay),
		mkPage(c, "concept", relay),
		mkPage(d, "concept", relay),
		mkPage(e, "concept", relay),
		mkPage(relay, "entity"),
	)

	getABScore := func(pairs []PairScore) float32 {
		for _, p := range pairs {
			if (p.PageA == a && p.PageB == b) || (p.PageA == b && p.PageB == a) {
				return p.Signals["adamic_adar"]
			}
		}
		return -1
	}

	s1 := getABScore(ScoreAll(g1, ScoreOptions{MinScore: 0.01}))
	s2 := getABScore(ScoreAll(g2, ScoreOptions{MinScore: 0.01}))
	if s1 < 0 || s2 < 0 {
		t.Fatalf("setup: missing AA score (s1=%v s2=%v)", s1, s2)
	}
	if s1 <= s2 {
		t.Errorf("relay-of-2 should beat relay-of-5 in AA: s1=%v s2=%v", s1, s2)
	}
}

// ── type affinity ─────────────────────────────────────────────

func TestTypeAffinity_KnownPairs(t *testing.T) {
	cases := []struct {
		a, b string
		want float32
	}{
		{"entity", "concept", 1.2},
		{"concept", "entity", 1.2},
		{"source", "source", 0.5},
		{"unknown", "concept", DefaultTypeAffinity},
		{"", "concept", DefaultTypeAffinity},
	}
	for _, c := range cases {
		got := typeAffinity(c.a, c.b)
		if got != c.want {
			t.Errorf("typeAffinity(%q,%q) = %v, want %v",
				c.a, c.b, got, c.want)
		}
	}
}

func TestScore_TypeAffinityShiftsScore(t *testing.T) {
	// Two graphs with identical link structure but different type
	// pairs. entity↔concept (1.2) should outscore source↔source (0.5).
	a := uuid.New()
	b := uuid.New()
	mk := func(ta, tb string) []PairScore {
		g := mkGraph(mkPage(a, ta, b), mkPage(b, tb))
		return ScoreAll(g, ScoreOptions{MinScore: 0.01})
	}
	highAffinity := mk("entity", "concept")
	lowAffinity := mk("source", "source")
	if len(highAffinity) != 1 || len(lowAffinity) != 1 {
		t.Fatalf("setup: 1 pair each")
	}
	if highAffinity[0].Score <= lowAffinity[0].Score {
		t.Errorf("entity↔concept should outscore source↔source, got %v vs %v",
			highAffinity[0].Score, lowAffinity[0].Score)
	}
}

// ── canonical ordering + dedupe ───────────────────────────────

func TestScoreAll_PairAppearsOnce(t *testing.T) {
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	g := mkGraph(
		mkPage(a, "concept", b, c),
		mkPage(b, "entity", c),
		mkPage(c, "entity", a),
	)
	pairs := ScoreAll(g, ScoreOptions{MinScore: 0.01})
	seen := map[[2]uuid.UUID]int{}
	for _, p := range pairs {
		key := [2]uuid.UUID{p.PageA, p.PageB}
		seen[key]++
		if p.PageA.String() >= p.PageB.String() {
			t.Errorf("expected page_a < page_b: %s vs %s",
				p.PageA, p.PageB)
		}
	}
	for k, n := range seen {
		if n > 1 {
			t.Errorf("pair %v appeared %d times", k, n)
		}
	}
}

func TestScoreAll_MinScoreFilters(t *testing.T) {
	// Two unrelated pages with neutral type fall below MinScore=10.0.
	a, b := uuid.New(), uuid.New()
	g := mkGraph(mkPage(a, "concept"), mkPage(b, "entity"))
	pairs := ScoreAll(g, ScoreOptions{MinScore: 10.0})
	if len(pairs) != 0 {
		t.Errorf("MinScore=10.0 should drop weak pairs, got %d", len(pairs))
	}
}

func TestScoreAll_EmptyGraphIsCheap(t *testing.T) {
	if got := ScoreAll(&ProjectGraph{Pages: map[uuid.UUID]*PageNode{}}, ScoreOptions{}); got != nil {
		t.Errorf("empty graph should produce nil, got %v", got)
	}
	if got := ScoreAll(nil, ScoreOptions{}); got != nil {
		t.Errorf("nil graph should produce nil, got %v", got)
	}
}

// ── wikilink resolution ───────────────────────────────────────

func TestResolveWikilinks_MapsTitlesCaseInsensitive(t *testing.T) {
	a := uuid.New()
	b := uuid.New()
	titles := map[string]uuid.UUID{
		"alpha page": a, "beta": b,
	}
	got := ResolveWikilinks("see [[Alpha Page]] and [[BETA]]", titles, uuid.Nil)
	if len(got) != 2 {
		t.Fatalf("want 2 resolutions, got %d (%v)", len(got), got)
	}
}

func TestResolveWikilinks_ExcludesSelf(t *testing.T) {
	self := uuid.New()
	other := uuid.New()
	titles := map[string]uuid.UUID{"me": self, "them": other}
	got := ResolveWikilinks("[[me]] [[them]]", titles, self)
	if len(got) != 1 || got[0] != other {
		t.Errorf("self-link should drop, got %v", got)
	}
}

func TestResolveWikilinks_DedupesRepeats(t *testing.T) {
	other := uuid.New()
	titles := map[string]uuid.UUID{"x": other}
	got := ResolveWikilinks("[[x]] [[x]] [[X]]", titles, uuid.Nil)
	if len(got) != 1 {
		t.Errorf("repeated target should dedupe, got %d", len(got))
	}
}

func TestResolveWikilinks_FastPathOnNoBrackets(t *testing.T) {
	got := ResolveWikilinks(strings.Repeat("plain text ", 100),
		map[string]uuid.UUID{"x": uuid.New()}, uuid.Nil)
	if got != nil {
		t.Errorf("no [[ should short-circuit to nil, got %v", got)
	}
}

// ── top-K cap ────────────────────────────────────────────────

func TestScoreAll_PerPageCap(t *testing.T) {
	// 6 pages all linked to a relay. With MaxNeighborsPerPage=2 the relay
	// only keeps its 2 strongest, but every spoke still keeps the relay
	// as its single neighbour.
	relay := uuid.New()
	spokes := []uuid.UUID{
		uuid.New(), uuid.New(), uuid.New(),
		uuid.New(), uuid.New(),
	}
	nodes := []*PageNode{mkPage(relay, "entity")}
	for _, s := range spokes {
		nodes = append(nodes, mkPage(s, "concept", relay))
	}
	pairs := ScoreAll(mkGraph(nodes...),
		ScoreOptions{MinScore: 0.01, MaxNeighborsPerPage: 2})
	// With cap=2 the relay keeps 2 neighbours; that means 2 (relay,spoke)
	// pairs survive at minimum. 3 other spoke-spoke pairs might also
	// pass via Adamic-Adar but the relay has degree=5 so AA contribution
	// per pair is small. The test asserts the cap is respected.
	hubCount := 0
	for _, p := range pairs {
		if p.PageA == relay || p.PageB == relay {
			hubCount++
		}
	}
	if hubCount > 2 {
		t.Errorf("relay should be capped at 2 neighbours, got %d", hubCount)
	}
}
