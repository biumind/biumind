// Sweep rules — find pages that have likely outlived their usefulness.
//
// Two rules ship in v1, both pure functions over (page, project_state):
//
//	stale_page     — page.updated_at older than StaleAfterDays (default 90)
//	orphaned_page  — page has no incoming wikilinks from any live page
//	                 in the same project AND is also stale enough that
//	                 the lack of links is unlikely to be an oversight
//
// The orphan check is conservative: a brand-new page hasn't had time
// to be linked to from elsewhere, and "no incoming wikilinks" alone
// would flag it. We layer the staleness gate on top so an active
// page-creation session doesn't fill the queue.
//
// Kind=sweep findings live in the same review_items table as dedup
// and lint (P2-D-{1,2}). The MCP/REST surface filters by kind so each
// audit category renders independently in the UI.
package reviews

import (
	"strings"
	"time"

	"github.com/google/uuid"
)

// Rule IDs. Same naming convention as lint rules (verb_noun, lower_snake).
const (
	RuleStalePage    = "stale_page"
	RuleOrphanedPage = "orphaned_page"
)

// SweepInput is the rule-side projection of one project's pages plus
// link-graph state. Producers (the worker) are responsible for
// computing IncomingLinks once per project before iterating pages.
type SweepInput struct {
	Page            SweepPageView
	IncomingLinks   int // count of distinct other pages whose blocks reference this page's title
	Now             time.Time
	StaleAfterDays  int // default 90 if zero/negative
	OrphanAfterDays int // default 60 if zero/negative
}

// SweepPageView is the rule-side projection of brain.pages for sweep.
// Title is included so the UI can render a useful review title without
// a separate fetch.
type SweepPageView struct {
	ID        uuid.UUID
	Title     string
	UpdatedAt time.Time
}

// SweepAll runs every sweep rule against one page. Order is rule-stable
// (stale → orphan) so the queue's natural ordering is predictable.
//
// We intentionally allow stale + orphan to fire on the same page —
// they're different signals (one is "old", one is "unreferenced"),
// and a user dismissing one doesn't necessarily mean the other is
// wrong. dedupe_key per rule keeps both rows independent.
func SweepAll(in SweepInput) []Finding {
	var out []Finding
	if f := sweepStalePage(in); f != nil {
		out = append(out, *f)
	}
	if f := sweepOrphanedPage(in); f != nil {
		out = append(out, *f)
	}
	return out
}

// ─── Rule: stale_page ─────────────────────────────────────────

func sweepStalePage(in SweepInput) *Finding {
	threshold := in.StaleAfterDays
	if threshold <= 0 {
		threshold = 90
	}
	now := in.Now
	if now.IsZero() {
		now = time.Now()
	}
	age := now.Sub(in.Page.UpdatedAt)
	if age < time.Duration(threshold)*24*time.Hour {
		return nil
	}
	days := int(age / (24 * time.Hour))
	return &Finding{
		PageID: in.Page.ID,
		RuleID: RuleStalePage,
		Title:  "页面长期未更新：" + displayTitleSweep(in.Page.Title),
		Description: itoa(days) + " 天未更新。" +
			"如果内容仍然准确，标记 dismissed 即可；否则考虑刷新或归档。",
		Payload: map[string]any{
			"days_idle":   days,
			"updated_at":  in.Page.UpdatedAt.UTC().Format(time.RFC3339),
			"threshold_d": threshold,
		},
	}
}

// ─── Rule: orphaned_page ──────────────────────────────────────

func sweepOrphanedPage(in SweepInput) *Finding {
	if in.IncomingLinks > 0 {
		return nil
	}
	threshold := in.OrphanAfterDays
	if threshold <= 0 {
		threshold = 60
	}
	now := in.Now
	if now.IsZero() {
		now = time.Now()
	}
	age := now.Sub(in.Page.UpdatedAt)
	if age < time.Duration(threshold)*24*time.Hour {
		// Page is too fresh — no time to be linked-to yet, not an orphan.
		return nil
	}
	days := int(age / (24 * time.Hour))
	return &Finding{
		PageID: in.Page.ID,
		RuleID: RuleOrphanedPage,
		Title:  "孤立页面：" + displayTitleSweep(in.Page.Title),
		Description: "项目中没有其他页面通过 [[wikilink]] 引用本页，" +
			"且 " + itoa(days) + " 天未更新。" +
			"考虑加交叉引用、合并到相关页面，或归档。",
		Payload: map[string]any{
			"incoming_links": in.IncomingLinks,
			"days_idle":      days,
			"threshold_d":    threshold,
		},
	}
}

// SweepDedupeKey is the canonical dedupe_key for a sweep finding —
// stable across runs so re-scans are no-ops.
func SweepDedupeKey(pageID uuid.UUID, ruleID string) string {
	return "sweep:" + pageID.String() + ":" + ruleID
}

// ─── helpers ──────────────────────────────────────────────────

func displayTitleSweep(t string) string {
	t = strings.TrimSpace(t)
	if t == "" {
		return "(未命名页面)"
	}
	const cap = 60
	if len(t) > cap {
		return t[:cap] + "…"
	}
	return t
}

// itoa avoids pulling strconv just for one call site; sweep payloads
// emit integers like days_idle that are bounded by stale-after years.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
