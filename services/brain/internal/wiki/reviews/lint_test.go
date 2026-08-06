package reviews

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

// ── untitled_page ──────────────────────────────────────────────

func TestLintUntitledPage_FlagsEmptyTitle(t *testing.T) {
	in := LintInput{
		Page:   PageView{ID: uuid.New(), Title: "  "},
		Blocks: []BlockView{{Text: "body"}},
	}
	got := LintAll(in)
	if !findingsContainRule(got, RuleUntitledPage) {
		t.Errorf("untitled_page should flag whitespace-only title")
	}
}

func TestLintUntitledPage_AcceptsFrontmatterTitle(t *testing.T) {
	in := LintInput{
		Page: PageView{
			ID:          uuid.New(),
			Title:       "",
			Frontmatter: map[string]any{"title": "From FM"},
		},
		Blocks: []BlockView{{Text: "body"}},
	}
	got := LintAll(in)
	if findingsContainRule(got, RuleUntitledPage) {
		t.Errorf("frontmatter.title should satisfy untitled_page")
	}
}

func TestLintUntitledPage_AcceptsPageTitle(t *testing.T) {
	in := LintInput{
		Page:   PageView{ID: uuid.New(), Title: "Real Title"},
		Blocks: []BlockView{{Text: "body"}},
	}
	got := LintAll(in)
	if findingsContainRule(got, RuleUntitledPage) {
		t.Errorf("non-empty title shouldn't flag untitled_page")
	}
}

// ── empty_page ─────────────────────────────────────────────────

func TestLintEmptyPage_FlagsZeroBlocks(t *testing.T) {
	in := LintInput{Page: PageView{ID: uuid.New(), Title: "T"}}
	got := LintAll(in)
	if !findingsContainRule(got, RuleEmptyPage) {
		t.Errorf("zero blocks should flag empty_page")
	}
}

func TestLintEmptyPage_FlagsAllBlankBlocks(t *testing.T) {
	in := LintInput{
		Page: PageView{ID: uuid.New(), Title: "T"},
		Blocks: []BlockView{
			{Text: "   "},
			{Text: "\t\n"},
		},
	}
	got := LintAll(in)
	if !findingsContainRule(got, RuleEmptyPage) {
		t.Errorf("all-whitespace blocks should flag empty_page")
	}
}

func TestLintEmptyPage_DoesNotFlagWhenAnyBlockHasContent(t *testing.T) {
	in := LintInput{
		Page: PageView{ID: uuid.New(), Title: "T"},
		Blocks: []BlockView{
			{Text: ""},
			{Caption: "alt text"}, // caption counts as content
			{Text: ""},
		},
	}
	got := LintAll(in)
	if findingsContainRule(got, RuleEmptyPage) {
		t.Errorf("page with caption-only block shouldn't flag empty_page")
	}
}

// ── stub_page ──────────────────────────────────────────────────

func TestLintStubPage_FlagsShortPage(t *testing.T) {
	in := LintInput{
		Page: PageView{ID: uuid.New(), Title: "T"},
		Blocks: []BlockView{
			{Text: "tiny note"},
		},
	}
	got := LintAll(in)
	if !findingsContainRule(got, RuleStubPage) {
		t.Errorf("9-char page should flag stub_page")
	}
}

func TestLintStubPage_NoFlagOnDecentLength(t *testing.T) {
	in := LintInput{
		Page: PageView{ID: uuid.New(), Title: "T"},
		Blocks: []BlockView{
			{Text: strings.Repeat("a", 200)},
		},
	}
	got := LintAll(in)
	if findingsContainRule(got, RuleStubPage) {
		t.Errorf("200-char page shouldn't flag stub_page")
	}
}

func TestLintStubPage_NoFlagOnEmpty(t *testing.T) {
	// empty_page covers this case; stub shouldn't double-flag.
	in := LintInput{Page: PageView{ID: uuid.New(), Title: "T"}}
	got := LintAll(in)
	if findingsContainRule(got, RuleStubPage) {
		t.Errorf("empty page shouldn't double-flag stub_page")
	}
}

func TestLintStubPage_NoFlagOnManyBlocks(t *testing.T) {
	// More blocks → user is structuring something even if short.
	in := LintInput{
		Page: PageView{ID: uuid.New(), Title: "T"},
		Blocks: []BlockView{
			{Text: "a"}, {Text: "b"}, {Text: "c"},
		},
	}
	got := LintAll(in)
	if findingsContainRule(got, RuleStubPage) {
		t.Errorf("3-block page shouldn't flag stub_page")
	}
}

// ── dead_wikilink ──────────────────────────────────────────────

func TestLintDeadWikilink_FlagsUnresolvedTarget(t *testing.T) {
	in := LintInput{
		Page: PageView{ID: uuid.New(), Title: "T"},
		Blocks: []BlockView{
			{Text: "see [[Existing Page]] and [[Missing Page]]"},
		},
		KnownPageTitles: map[string]struct{}{
			"existing page": {},
			"t":             {}, // self
		},
	}
	got := LintAll(in)
	dead := findingsByRule(got, RuleDeadWikilink)
	if len(dead) != 1 {
		t.Fatalf("want 1 dead wikilink, got %d", len(dead))
	}
	if dead[0].Payload["target"] != "Missing Page" {
		t.Errorf("dead wikilink target wrong: %v", dead[0].Payload)
	}
}

func TestLintDeadWikilink_DedupesPerTarget(t *testing.T) {
	// Same target referenced twice in the same page is one finding.
	in := LintInput{
		Page: PageView{ID: uuid.New()},
		Blocks: []BlockView{
			{Text: "[[X]] and again [[X]]"},
		},
		KnownPageTitles: map[string]struct{}{},
	}
	got := LintAll(in)
	dead := findingsByRule(got, RuleDeadWikilink)
	if len(dead) != 1 {
		t.Errorf("duplicate target should produce one finding, got %d", len(dead))
	}
}

func TestLintDeadWikilink_SkipsAliasInLabel(t *testing.T) {
	// `[[target|label]]` — alias is the visible label; the regex
	// captures only the target. A live target with arbitrary label
	// must not flag.
	in := LintInput{
		Page: PageView{ID: uuid.New()},
		Blocks: []BlockView{
			{Text: "[[real|some random label]]"},
		},
		KnownPageTitles: map[string]struct{}{
			"real": {},
		},
	}
	got := LintAll(in)
	if findingsContainRule(got, RuleDeadWikilink) {
		t.Errorf("alias links to live targets shouldn't flag")
	}
}

func TestLintDeadWikilink_DegradesToSilenceWithoutContext(t *testing.T) {
	// No KnownPageTitles map → can't tell. Better silent than mass FP.
	in := LintInput{
		Page: PageView{ID: uuid.New()},
		Blocks: []BlockView{
			{Text: "[[anything]] [[everything]]"},
		},
	}
	got := LintAll(in)
	if findingsContainRule(got, RuleDeadWikilink) {
		t.Errorf("missing project context must silence dead_wikilink")
	}
}

func TestLintDeadWikilink_CaseInsensitiveMatching(t *testing.T) {
	in := LintInput{
		Page: PageView{ID: uuid.New()},
		Blocks: []BlockView{
			{Text: "[[Existing Page]]"},
		},
		KnownPageTitles: map[string]struct{}{
			"existing page": {},
		},
	}
	got := LintAll(in)
	if findingsContainRule(got, RuleDeadWikilink) {
		t.Errorf("case-insensitive match should resolve")
	}
}

func TestLintDeadWikilink_StableSubKeyAcrossRuns(t *testing.T) {
	// Same target → same hashed sub key → same dedupe_key → re-scan
	// is idempotent (the whole point of subkey hashing).
	pid := uuid.New()
	mk := func() string {
		findings := lintDeadWikilinks(LintInput{
			Page: PageView{ID: pid},
			Blocks: []BlockView{
				{Text: "[[Some Target]]"},
			},
			KnownPageTitles: map[string]struct{}{},
		})
		if len(findings) != 1 {
			t.Fatalf("want 1 finding, got %d", len(findings))
		}
		return LintDedupeKey(pid, RuleDeadWikilink, findings[0].SubKey)
	}
	if mk() != mk() {
		t.Errorf("dedupe key drifts across calls — re-scan won't dedupe")
	}
}

// ── missing_frontmatter ────────────────────────────────────────

func TestLintMissingFrontmatter_FlagsEmpty(t *testing.T) {
	in := LintInput{
		Page:   PageView{ID: uuid.New(), Title: "T", Frontmatter: map[string]any{}},
		Blocks: []BlockView{{Text: "body"}},
	}
	if !findingsContainRule(LintAll(in), RuleMissingFrontmatter) {
		t.Errorf("empty frontmatter map should flag missing_frontmatter")
	}
}

func TestLintMissingFrontmatter_FlagsNil(t *testing.T) {
	in := LintInput{
		Page:   PageView{ID: uuid.New(), Title: "T"}, // Frontmatter nil
		Blocks: []BlockView{{Text: "body"}},
	}
	if !findingsContainRule(LintAll(in), RuleMissingFrontmatter) {
		t.Errorf("nil frontmatter should flag missing_frontmatter")
	}
}

func TestLintMissingFrontmatter_AcceptsPopulated(t *testing.T) {
	in := LintInput{
		Page: PageView{
			ID:          uuid.New(),
			Title:       "T",
			Frontmatter: map[string]any{"type": "concept"},
		},
		Blocks: []BlockView{{Text: "body"}},
	}
	if findingsContainRule(LintAll(in), RuleMissingFrontmatter) {
		t.Errorf("populated frontmatter shouldn't flag missing_frontmatter")
	}
}

// ── duplicate_title ────────────────────────────────────────────

func TestLintDuplicateTitle_FlagsCollidingGroup(t *testing.T) {
	pid := uuid.New()
	other := uuid.New()
	in := LintInput{
		Page: PageView{ID: pid, Title: "Notes"},
		Blocks: []BlockView{{Text: "body"}},
		TitleGroups: map[string][]uuid.UUID{
			"notes": {pid, other},
		},
	}
	got := findingsByRule(LintAll(in), RuleDuplicateTitle)
	if len(got) != 1 {
		t.Fatalf("want 1 duplicate_title finding, got %d", len(got))
	}
	if got[0].Payload["conflict_count"].(int) != 2 {
		t.Errorf("conflict_count payload wrong: %v", got[0].Payload)
	}
}

func TestLintDuplicateTitle_NoFlagOnUniqueTitle(t *testing.T) {
	pid := uuid.New()
	in := LintInput{
		Page: PageView{ID: pid, Title: "Unique"},
		Blocks: []BlockView{{Text: "body"}},
		TitleGroups: map[string][]uuid.UUID{
			"unique": {pid}, // group of 1
		},
	}
	if findingsContainRule(LintAll(in), RuleDuplicateTitle) {
		t.Errorf("unique title shouldn't flag duplicate_title")
	}
}

func TestLintDuplicateTitle_DegradesToSilenceWithoutGroups(t *testing.T) {
	// nil TitleGroups (worker didn't compute) → silence, not mass FP.
	in := LintInput{
		Page:   PageView{ID: uuid.New(), Title: "Notes"},
		Blocks: []BlockView{{Text: "body"}},
	}
	if findingsContainRule(LintAll(in), RuleDuplicateTitle) {
		t.Errorf("nil TitleGroups must silence duplicate_title")
	}
}

func TestLintDuplicateTitle_SkipsEmptyTitle(t *testing.T) {
	// untitled_page already covers empty; don't double-flag.
	in := LintInput{
		Page:   PageView{ID: uuid.New(), Title: "   "},
		Blocks: []BlockView{{Text: "body"}},
		TitleGroups: map[string][]uuid.UUID{
			"": {uuid.New(), uuid.New()},
		},
	}
	if findingsContainRule(LintAll(in), RuleDuplicateTitle) {
		t.Errorf("empty title shouldn't flag duplicate_title")
	}
}

// ── orphan_page ────────────────────────────────────────────────

func TestLintOrphanPage_FlagsUnreferenced(t *testing.T) {
	in := LintInput{
		Page:               PageView{ID: uuid.New(), Title: "Lonely"},
		Blocks:             []BlockView{{Text: "body"}},
		IncomingLinkTitles: map[string]struct{}{"other": {}},
	}
	if !findingsContainRule(LintAll(in), RuleOrphanPage) {
		t.Errorf("page with no inbound links should flag orphan_page")
	}
}

func TestLintOrphanPage_NoFlagWhenReferenced(t *testing.T) {
	in := LintInput{
		Page: PageView{ID: uuid.New(), Title: "Popular"},
		Blocks: []BlockView{{Text: "body"}},
		IncomingLinkTitles: map[string]struct{}{
			"popular": {}, // case-insensitive match
		},
	}
	if findingsContainRule(LintAll(in), RuleOrphanPage) {
		t.Errorf("referenced page shouldn't flag orphan_page")
	}
}

func TestLintOrphanPage_DegradesToSilenceWithoutContext(t *testing.T) {
	in := LintInput{
		Page:   PageView{ID: uuid.New(), Title: "Whatever"},
		Blocks: []BlockView{{Text: "body"}},
		// IncomingLinkTitles nil
	}
	if findingsContainRule(LintAll(in), RuleOrphanPage) {
		t.Errorf("nil IncomingLinkTitles must silence orphan_page")
	}
}

func TestLintOrphanPage_SkipsEmptyTitle(t *testing.T) {
	// Empty title can't be a wikilink target; untitled_page covers it.
	in := LintInput{
		Page:               PageView{ID: uuid.New(), Title: ""},
		Blocks:             []BlockView{{Text: "body"}},
		IncomingLinkTitles: map[string]struct{}{},
	}
	if findingsContainRule(LintAll(in), RuleOrphanPage) {
		t.Errorf("empty title shouldn't flag orphan_page")
	}
}

// ── dedupe_key ─────────────────────────────────────────────────

func TestLintDedupeKey_OmitsEmptySub(t *testing.T) {
	pid := uuid.New()
	got := LintDedupeKey(pid, RuleEmptyPage, "")
	if !strings.HasPrefix(got, "lint:"+pid.String()+":empty_page") {
		t.Errorf("unexpected key: %s", got)
	}
	if strings.Count(got, ":") != 2 {
		t.Errorf("empty sub should produce 2 colons, got %s", got)
	}
}

func TestLintDedupeKey_IncludesSub(t *testing.T) {
	pid := uuid.New()
	got := LintDedupeKey(pid, RuleDeadWikilink, "abc123")
	if !strings.HasSuffix(got, ":abc123") {
		t.Errorf("sub missing from key: %s", got)
	}
}

// ── helpers ────────────────────────────────────────────────────

func findingsContainRule(fs []Finding, rule string) bool {
	for _, f := range fs {
		if f.RuleID == rule {
			return true
		}
	}
	return false
}

func findingsByRule(fs []Finding, rule string) []Finding {
	var out []Finding
	for _, f := range fs {
		if f.RuleID == rule {
			out = append(out, f)
		}
	}
	return out
}
