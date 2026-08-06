package api

import (
	"context"
	"errors"
	"testing"

	"github.com/biumind/biumind/services/brain/internal/search/bm25"
	wikirelevance "github.com/biumind/biumind/services/brain/internal/wiki/relevance"
	"github.com/google/uuid"
)

// stubRelated implements relatedLister — returns the prepared rows for
// each seed, or an error per seed when set in errs.
type stubRelated struct {
	byPage map[uuid.UUID][]wikirelevance.Related
	errs   map[uuid.UUID]error
	calls  []uuid.UUID
}

func (s *stubRelated) ListRelated(_ context.Context, pageID uuid.UUID, _ int) ([]wikirelevance.Related, error) {
	s.calls = append(s.calls, pageID)
	if err, ok := s.errs[pageID]; ok {
		return nil, err
	}
	return s.byPage[pageID], nil
}

func mkRelated(other uuid.UUID, title string, score float32) wikirelevance.Related {
	return wikirelevance.Related{OtherPageID: other, Title: title, Score: score}
}

func mkBM25Page(pageID uuid.UUID, title string, score float64) bm25.Hit {
	return bm25.Hit{
		ID: pageID.String(), Kind: "page", PageID: pageID.String(),
		Title: title, Score: score,
	}
}

// ── core happy path ──────────────────────────────────────────

func TestExpandGraph_DedupesSamePageAcrossSeeds(t *testing.T) {
	seed1 := uuid.New()
	seed2 := uuid.New()
	shared := uuid.New()
	// Use values that round-trip exactly between float32 and float64
	// (powers of 2 fractions) so the test doesn't rely on epsilon.
	rel := &stubRelated{
		byPage: map[uuid.UUID][]wikirelevance.Related{
			seed1: {mkRelated(shared, "Shared Page", 0.25)},
			seed2: {mkRelated(shared, "Shared Page", 0.75)},
		},
	}
	hits := []bm25.Hit{
		mkBM25Page(seed1, "Seed 1", 1.0),
		mkBM25Page(seed2, "Seed 2", 0.9),
	}
	got := expandGraphImpl(context.Background(), rel, hits, 10, nil)
	if len(got) != 1 {
		t.Fatalf("want 1 deduped result, got %d", len(got))
	}
	if got[0].Score != 0.75 {
		t.Errorf("expected best-of-seeds score 0.75, got %v", got[0].Score)
	}
	if got[0].PageID != shared.String() {
		t.Errorf("page id wrong: %v", got[0].PageID)
	}
}

func TestExpandGraph_SkipsPagesAlreadyInSeedSet(t *testing.T) {
	// Seed B happens to be related to Seed A. The graph path mustn't
	// surface Seed B as a "graph" hit because BM25 already has it.
	seedA := uuid.New()
	seedB := uuid.New()
	rel := &stubRelated{
		byPage: map[uuid.UUID][]wikirelevance.Related{
			seedA: {mkRelated(seedB, "Seed B", 0.7)},
		},
	}
	hits := []bm25.Hit{
		mkBM25Page(seedA, "Seed A", 1.0),
		mkBM25Page(seedB, "Seed B", 0.9),
	}
	got := expandGraphImpl(context.Background(), rel, hits, 10, nil)
	if len(got) != 0 {
		t.Errorf("seed-set page must not appear in graph hits: %v", got)
	}
}

func TestExpandGraph_PreservesViaSeed(t *testing.T) {
	seed := uuid.New()
	other := uuid.New()
	rel := &stubRelated{
		byPage: map[uuid.UUID][]wikirelevance.Related{
			seed: {mkRelated(other, "Other", 0.5)},
		},
	}
	got := expandGraphImpl(context.Background(), rel,
		[]bm25.Hit{mkBM25Page(seed, "S", 1.0)}, 10, nil)
	if len(got) != 1 {
		t.Fatalf("want 1, got %d", len(got))
	}
	if got[0].ViaSeed != seed.String() {
		t.Errorf("via_seed wrong: %v vs %v", got[0].ViaSeed, seed)
	}
}

func TestExpandGraph_SortDescScore(t *testing.T) {
	seed := uuid.New()
	a := uuid.New()
	b := uuid.New()
	c := uuid.New()
	rel := &stubRelated{
		byPage: map[uuid.UUID][]wikirelevance.Related{
			seed: {
				mkRelated(a, "A", 0.3),
				mkRelated(b, "B", 0.9),
				mkRelated(c, "C", 0.5),
			},
		},
	}
	got := expandGraphImpl(context.Background(), rel,
		[]bm25.Hit{mkBM25Page(seed, "S", 1.0)}, 10, nil)
	if len(got) != 3 {
		t.Fatalf("want 3, got %d", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].Score < got[i].Score {
			t.Errorf("not sorted desc: pos %d (%v) > pos %d (%v)",
				i-1, got[i-1].Score, i, got[i].Score)
		}
	}
}

func TestExpandGraph_RespectsLimit(t *testing.T) {
	seed := uuid.New()
	rels := []wikirelevance.Related{}
	for i := 0; i < 8; i++ {
		rels = append(rels, mkRelated(uuid.New(), "p", float32(i)/10.0))
	}
	rel := &stubRelated{byPage: map[uuid.UUID][]wikirelevance.Related{seed: rels}}
	got := expandGraphImpl(context.Background(), rel,
		[]bm25.Hit{mkBM25Page(seed, "S", 1.0)}, 3, nil)
	if len(got) != 3 {
		t.Errorf("limit 3 should cap, got %d", len(got))
	}
}

func TestExpandGraph_OneSeedErrorDoesntKillBatch(t *testing.T) {
	seedA := uuid.New()
	seedB := uuid.New()
	other := uuid.New()
	rel := &stubRelated{
		byPage: map[uuid.UUID][]wikirelevance.Related{
			seedB: {mkRelated(other, "Other", 0.5)},
		},
		errs: map[uuid.UUID]error{
			seedA: errors.New("simulated DB error"),
		},
	}
	got := expandGraphImpl(context.Background(), rel,
		[]bm25.Hit{
			mkBM25Page(seedA, "A", 1.0),
			mkBM25Page(seedB, "B", 0.9),
		}, 10, nil)
	if len(got) != 1 {
		t.Fatalf("want 1 hit (seedB's), got %d", len(got))
	}
	if got[0].PageID != other.String() {
		t.Errorf("expected other page from seedB, got %v", got[0].PageID)
	}
}

func TestExpandGraph_NilRelOrEmptyHits(t *testing.T) {
	if got := expandGraphImpl(context.Background(), nil,
		[]bm25.Hit{mkBM25Page(uuid.New(), "x", 1.0)}, 10, nil); got != nil {
		t.Errorf("nil rel should return nil")
	}
	if got := expandGraphImpl(context.Background(),
		&stubRelated{}, nil, 10, nil); got != nil {
		t.Errorf("empty hits should return nil")
	}
}

func TestExpandGraph_DedupesBlockHitsToOnePageSeed(t *testing.T) {
	// BM25 may return multiple block hits for the same page. We
	// shouldn't expand-graph that page twice.
	pageID := uuid.New()
	other := uuid.New()
	rel := &stubRelated{
		byPage: map[uuid.UUID][]wikirelevance.Related{
			pageID: {mkRelated(other, "Other", 0.7)},
		},
	}
	hits := []bm25.Hit{
		{ID: "b1", Kind: "block", PageID: pageID.String(), Title: "T", Score: 1.0},
		{ID: "b2", Kind: "block", PageID: pageID.String(), Title: "T", Score: 0.9},
	}
	expandGraphImpl(context.Background(), rel, hits, 10, nil)
	if len(rel.calls) != 1 {
		t.Errorf("expected 1 ListRelated call (page-id dedupe), got %d", len(rel.calls))
	}
}

// ── tokenizeQuery / altMatchesQuery ──────────────────────────

func TestTokenizeQuery_ASCIISplitsOnSpaceAndPunct(t *testing.T) {
	got := tokenizeQuery("Hello, world! foo")
	want := []string{"hello", "world", "foo"}
	if len(got) != len(want) {
		t.Fatalf("want %v, got %v", want, got)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("[%d] %q vs %q", i, got[i], want[i])
		}
	}
}

func TestTokenizeQuery_CJKBecomesUnigrams(t *testing.T) {
	got := tokenizeQuery("总资产")
	want := []string{"总", "资", "产"}
	if len(got) != len(want) {
		t.Fatalf("want %v, got %v", want, got)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("[%d] %q vs %q", i, got[i], want[i])
		}
	}
}

func TestTokenizeQuery_MixedASCIIAndCJK(t *testing.T) {
	got := tokenizeQuery("Q3 总资产 analysis")
	want := []string{"q3", "总", "资", "产", "analysis"}
	if len(got) != len(want) {
		t.Fatalf("want %v, got %v", want, got)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("[%d] %q vs %q", i, got[i], want[i])
		}
	}
}

func TestTokenizeQuery_TrailingCJKPunctIgnored(t *testing.T) {
	got := tokenizeQuery("总资产。")
	for _, tok := range got {
		if tok == "。" {
			t.Errorf("CJK period should be punctuation, got token: %v", got)
		}
	}
}

func TestTokenizeQuery_EmptyOrWhitespace(t *testing.T) {
	if got := tokenizeQuery(""); got != nil {
		t.Errorf("empty should be nil, got %v", got)
	}
	if got := tokenizeQuery("   "); got != nil {
		t.Errorf("whitespace should be nil, got %v", got)
	}
}

func TestAltMatchesQuery_ASCIICaseInsensitive(t *testing.T) {
	if !altMatchesQuery("Login Form", tokenizeQuery("login")) {
		t.Errorf("'login' should match 'Login Form' case-insensitively")
	}
}

func TestAltMatchesQuery_CJKAnyTokenHits(t *testing.T) {
	tokens := tokenizeQuery("总资产。")
	if !altMatchesQuery("图：2023 年总资产合计", tokens) {
		t.Errorf("CJK substring match should fire even with trailing period")
	}
}

func TestAltMatchesQuery_NoMatch(t *testing.T) {
	if altMatchesQuery("logo.png", tokenizeQuery("login")) {
		t.Errorf("'logo' shouldn't match 'login'")
	}
}

func TestAltMatchesQuery_EmptyInputs(t *testing.T) {
	if altMatchesQuery("", tokenizeQuery("hi")) {
		t.Errorf("empty alt shouldn't match")
	}
	if altMatchesQuery("hello", nil) {
		t.Errorf("nil tokens shouldn't match anything")
	}
}

// ── markdownImageRE ──────────────────────────────────────────

func TestMarkdownImageRE_BasicAltAndUrl(t *testing.T) {
	matches := markdownImageRE.FindAllStringSubmatch(
		"see ![Login Form](https://x.com/img.png) below", -1)
	if len(matches) != 1 {
		t.Fatalf("want 1 match, got %d", len(matches))
	}
	if matches[0][1] != "Login Form" {
		t.Errorf("alt: %q", matches[0][1])
	}
	if matches[0][2] != "https://x.com/img.png" {
		t.Errorf("url: %q", matches[0][2])
	}
}

func TestMarkdownImageRE_EmptyAltOk(t *testing.T) {
	matches := markdownImageRE.FindAllStringSubmatch(
		"![](path/to/image.png)", -1)
	if len(matches) != 1 || matches[0][1] != "" {
		t.Errorf("empty-alt image should still match: %v", matches)
	}
}

func TestMarkdownImageRE_TitleSuffixIgnored(t *testing.T) {
	matches := markdownImageRE.FindAllStringSubmatch(
		`![alt](https://x.com/img.png "tooltip text")`, -1)
	if len(matches) != 1 {
		t.Fatalf("want 1 match")
	}
	if matches[0][2] != "https://x.com/img.png" {
		t.Errorf("url should not include title: %q", matches[0][2])
	}
}

func TestMarkdownImageRE_NotPlainLink(t *testing.T) {
	matches := markdownImageRE.FindAllStringSubmatch(
		"see [my page](path/page.md)", -1)
	if len(matches) != 0 {
		t.Errorf("link should not match image regex: %v", matches)
	}
}

func TestMarkdownImageRE_CJKAlt(t *testing.T) {
	matches := markdownImageRE.FindAllStringSubmatch(
		"![总资产合计](https://x.com/chart.png)", -1)
	if len(matches) != 1 || matches[0][1] != "总资产合计" {
		t.Errorf("CJK alt should round-trip: %v", matches)
	}
}
