package reviews

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func mkSweepInput(updatedDaysAgo int, incoming int) SweepInput {
	now := time.Now()
	return SweepInput{
		Page: SweepPageView{
			ID:        uuid.New(),
			Title:     "Test Page",
			UpdatedAt: now.Add(-time.Duration(updatedDaysAgo) * 24 * time.Hour),
		},
		IncomingLinks: incoming,
		Now:           now,
	}
}

// ── stale_page ─────────────────────────────────────────────────

func TestSweepStale_FlagsOldPage(t *testing.T) {
	got := SweepAll(mkSweepInput(120, 5))
	if !findingsContainRule(got, RuleStalePage) {
		t.Errorf("120-day-old page should flag stale_page")
	}
}

func TestSweepStale_DoesNotFlagFreshPage(t *testing.T) {
	got := SweepAll(mkSweepInput(30, 5))
	if findingsContainRule(got, RuleStalePage) {
		t.Errorf("30-day-old page shouldn't flag stale_page")
	}
}

func TestSweepStale_RespectsCustomThreshold(t *testing.T) {
	in := mkSweepInput(45, 5)
	in.StaleAfterDays = 30
	got := SweepAll(in)
	if !findingsContainRule(got, RuleStalePage) {
		t.Errorf("with threshold=30, 45-day page should flag stale_page")
	}
}

func TestSweepStale_PayloadIncludesDaysIdle(t *testing.T) {
	got := SweepAll(mkSweepInput(120, 5))
	for _, f := range got {
		if f.RuleID != RuleStalePage {
			continue
		}
		days, ok := f.Payload["days_idle"].(int)
		if !ok {
			t.Errorf("payload.days_idle missing or wrong type: %v", f.Payload)
			continue
		}
		// allow a slight drift since we recompute now() once
		if days < 119 || days > 121 {
			t.Errorf("days_idle should be ~120, got %d", days)
		}
	}
}

// ── orphaned_page ─────────────────────────────────────────────

func TestSweepOrphan_FlagsNoIncomingAndStale(t *testing.T) {
	got := SweepAll(mkSweepInput(90, 0))
	if !findingsContainRule(got, RuleOrphanedPage) {
		t.Errorf("orphan + 90-day-old should flag orphaned_page")
	}
}

func TestSweepOrphan_DoesNotFlagWithIncomingLinks(t *testing.T) {
	got := SweepAll(mkSweepInput(180, 1))
	if findingsContainRule(got, RuleOrphanedPage) {
		t.Errorf("page with 1 incoming link shouldn't flag orphan")
	}
}

func TestSweepOrphan_DoesNotFlagFreshPage(t *testing.T) {
	// No incoming links but only 10 days old — too fresh to be considered
	// orphaned (might just not be linked yet).
	got := SweepAll(mkSweepInput(10, 0))
	if findingsContainRule(got, RuleOrphanedPage) {
		t.Errorf("10-day-old page shouldn't flag orphan even with 0 incoming")
	}
}

func TestSweepOrphan_RespectsCustomThreshold(t *testing.T) {
	in := mkSweepInput(30, 0)
	in.OrphanAfterDays = 14
	got := SweepAll(in)
	if !findingsContainRule(got, RuleOrphanedPage) {
		t.Errorf("with threshold=14, 30-day orphan should flag")
	}
}

func TestSweepOrphan_PayloadShowsZeroIncoming(t *testing.T) {
	got := SweepAll(mkSweepInput(100, 0))
	for _, f := range got {
		if f.RuleID != RuleOrphanedPage {
			continue
		}
		incoming, ok := f.Payload["incoming_links"].(int)
		if !ok || incoming != 0 {
			t.Errorf("expected incoming_links=0, got %v", f.Payload["incoming_links"])
		}
	}
}

// ── stale + orphan stacking ──────────────────────────────────

func TestSweepAll_BothRulesCanFireOnSamePage(t *testing.T) {
	got := SweepAll(mkSweepInput(180, 0))
	if !findingsContainRule(got, RuleStalePage) {
		t.Errorf("expected stale_page on 180-day-old")
	}
	if !findingsContainRule(got, RuleOrphanedPage) {
		t.Errorf("expected orphaned_page on 180-day-old + 0 incoming")
	}
}

func TestSweepDedupeKey_StableAndDistinct(t *testing.T) {
	pid := uuid.New()
	stale := SweepDedupeKey(pid, RuleStalePage)
	orphan := SweepDedupeKey(pid, RuleOrphanedPage)
	if stale == orphan {
		t.Errorf("different rules should yield different keys")
	}
	if !strings.HasPrefix(stale, "sweep:") {
		t.Errorf("missing sweep prefix: %s", stale)
	}
	// Stable across calls.
	if stale != SweepDedupeKey(pid, RuleStalePage) {
		t.Errorf("dedupe key should be deterministic")
	}
}

// ── display title fallback ────────────────────────────────────

func TestDisplayTitleSweep_HandlesEmptyAndLong(t *testing.T) {
	if displayTitleSweep("") != "(未命名页面)" {
		t.Errorf("empty fallback")
	}
	if displayTitleSweep("   ") != "(未命名页面)" {
		t.Errorf("whitespace fallback")
	}
	long := strings.Repeat("x", 100)
	got := displayTitleSweep(long)
	if !strings.HasSuffix(got, "…") || len([]rune(got)) > 65 {
		t.Errorf("long title not truncated cleanly: %q", got)
	}
}
