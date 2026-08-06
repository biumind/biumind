// Lint rules — pure functions over (page, blocks) → []Finding.
//
// Each rule is a small Go function with a stable string ID. Findings
// become kind=lint review_items via WriteFindings. The dedupe_key
// pattern (`lint:<page>:<rule>:<sub>`) means re-running on stable
// inputs is a no-op, and a user dismiss/resolve sticks across re-runs.
//
// Rules ship deliberately conservative — false positives would teach
// the user to ignore the queue. We only flag conditions that are
// almost certainly fixable mistakes: empty pages, untitled pages,
// stubs (probably abandoned drafts), and wikilinks that don't resolve
// in the current project. Style / heading-hygiene rules wait until we
// have richer block taxonomy in production data — current ingest emits
// only "text" blocks so type-aware rules would be silent anyway.
package reviews

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Rule IDs. Externally-visible because dedupe_key uses them and
// dismiss/resolve transitions persist by key. Renaming a rule is a
// migration — old findings stay under the old key until re-scan.
const (
	RuleUntitledPage      = "untitled_page"
	RuleEmptyPage         = "empty_page"
	RuleStubPage          = "stub_page"
	RuleDeadWikilink      = "dead_wikilink"
	RuleMissingFrontmatter = "missing_frontmatter"
	RuleDuplicateTitle    = "duplicate_title"
	RuleOrphanPage        = "orphan_page"
)

// Finding is one detection from one rule against one page.
type Finding struct {
	PageID      uuid.UUID
	RuleID      string
	Title       string         // user-visible review title
	Description string         // user-visible review description
	Payload     map[string]any // rule-specific data (e.g. target slug for dead_wikilink)
	// SubKey disambiguates multiple findings of the same rule on the
	// same page. Empty for at-most-one-per-page rules; a stable hash
	// for per-occurrence rules like dead_wikilink so dismiss-by-target
	// works correctly.
	SubKey string
}

// LintInput is the projection rules consume — no live DB, no embeddings,
// just title + frontmatter + block contents. Producers of LintInput
// (the worker) are responsible for filtering deleted blocks.
type LintInput struct {
	Page       PageView
	Blocks     []BlockView
	// KnownPageTitles holds the titles of all live pages in the same
	// project, lowercased + trimmed. dead_wikilink uses it to detect
	// targets that don't resolve. Empty map = no project context, so
	// dead_wikilink degrades to no-op (better than mass false positives).
	KnownPageTitles map[string]struct{}
	// TitleGroups maps a lowercased+trimmed title to every live page ID
	// in the project that bears it. duplicate_title flags each page whose
	// title collides with at least one sibling. nil = not computed → rule
	// degrades to silence (same convention as KnownPageTitles).
	TitleGroups map[string][]uuid.UUID
	// IncomingLinkTitles holds every lowercased+trimmed page title that is
	// referenced by at least one [[wikilink]] across the project's blocks.
	// orphan_page flags pages whose title never appears here. nil = not
	// computed → rule degrades to silence.
	IncomingLinkTitles map[string]struct{}
}

// PageView is the rule-side projection of brain.pages.
type PageView struct {
	ID          uuid.UUID
	Title       string
	Frontmatter map[string]any
}

// BlockView is the rule-side projection of brain.blocks. Type +
// content text are the only fields rules currently consume.
type BlockView struct {
	ID      uuid.UUID
	Type    string
	Text    string
	Caption string
}

// LintAll runs every registered rule and concatenates findings. Order
// is rule-stable then per-rule-stable (sub-key alphabetical) so the
// review queue's natural ordering tracks the cause, not the scan time.
func LintAll(in LintInput) []Finding {
	var out []Finding
	out = append(out, lintUntitledPage(in)...)
	out = append(out, lintEmptyPage(in)...)
	out = append(out, lintStubPage(in)...)
	out = append(out, lintDeadWikilinks(in)...)
	out = append(out, lintMissingFrontmatter(in)...)
	out = append(out, lintDuplicateTitle(in)...)
	out = append(out, lintOrphanPage(in)...)
	return out
}

// ─── Rule: untitled_page ──────────────────────────────────────

func lintUntitledPage(in LintInput) []Finding {
	if strings.TrimSpace(in.Page.Title) != "" {
		return nil
	}
	// Frontmatter title is acceptable too — some authoring flows put
	// the title there and leave page.title for the URL slug.
	if title, ok := in.Page.Frontmatter["title"].(string); ok &&
		strings.TrimSpace(title) != "" {
		return nil
	}
	return []Finding{{
		PageID: in.Page.ID,
		RuleID: RuleUntitledPage,
		Title:  "页面缺少标题",
		Description: "page.title 与 frontmatter.title 都为空。" +
			"添加标题让其他页面的 wikilink 可以解析到这里。",
	}}
}

// ─── Rule: empty_page ─────────────────────────────────────────

func lintEmptyPage(in LintInput) []Finding {
	for _, b := range in.Blocks {
		if strings.TrimSpace(b.Text) != "" || strings.TrimSpace(b.Caption) != "" {
			return nil
		}
	}
	return []Finding{{
		PageID: in.Page.ID,
		RuleID: RuleEmptyPage,
		Title:  "页面无内容",
		Description: "页面没有任何 block，或所有 block 都为空。" +
			"考虑删除该页或补充内容。",
	}}
}

// ─── Rule: stub_page ──────────────────────────────────────────

// stubPageMaxBlocks / Chars are intentionally generous so we only flag
// pages that are genuinely sparse. A 100-char page with a code snippet
// might be intentional reference material; this rule just gates
// detection — the user still decides.
const (
	stubPageMaxBlocks = 2
	stubPageMaxChars  = 100
)

func lintStubPage(in LintInput) []Finding {
	if len(in.Blocks) == 0 || len(in.Blocks) > stubPageMaxBlocks {
		return nil
	}
	total := 0
	for _, b := range in.Blocks {
		total += utf8.RuneCountInString(b.Text)
		total += utf8.RuneCountInString(b.Caption)
	}
	if total == 0 {
		// empty_page already covers this case; don't double-flag.
		return nil
	}
	if total > stubPageMaxChars {
		return nil
	}
	return []Finding{{
		PageID: in.Page.ID,
		RuleID: RuleStubPage,
		Title:  "页面内容过短",
		Description: fmt.Sprintf(
			"页面只有 %d 个 block、共 %d 个字符。"+
				"考虑补充上下文或合并到相关页面。",
			len(in.Blocks), total),
		Payload: map[string]any{
			"block_count": len(in.Blocks),
			"char_count":  total,
		},
	}}
}

// ─── Rule: dead_wikilink ──────────────────────────────────────

// wikilinkRE matches `[[target]]` and `[[target|alias]]`. Same shape
// as wiki_llm.domain.wikilink — kept anchored within a single line so
// stray brackets in prose don't span paragraphs.
var wikilinkRE = regexp.MustCompile(`\[\[([^\]|\n]+)(?:\|[^\]\n]*)?\]\]`)

func lintDeadWikilinks(in LintInput) []Finding {
	if in.KnownPageTitles == nil {
		// Project context absent → can't tell live from dead. Degrade
		// to silence rather than emit false positives.
		// (An empty-but-non-nil map IS valid context — it means
		// "scanned, no other pages exist", so every wikilink is dead.)
		return nil
	}
	seen := map[string]bool{}
	var findings []Finding
	for _, b := range in.Blocks {
		matches := wikilinkRE.FindAllStringSubmatch(b.Text, -1)
		for _, m := range matches {
			target := strings.TrimSpace(m[1])
			if target == "" {
				continue
			}
			normalised := strings.ToLower(target)
			if _, ok := in.KnownPageTitles[normalised]; ok {
				continue
			}
			if seen[normalised] {
				continue // one finding per target per page
			}
			seen[normalised] = true
			findings = append(findings, Finding{
				PageID: in.Page.ID,
				RuleID: RuleDeadWikilink,
				Title:  "wikilink 找不到目标：[[" + target + "]]",
				Description: "本页引用 [[" + target + "]] 但项目里没有这个页面。" +
					"考虑创建该页或修正 wikilink 文本。",
				SubKey: hashSubKey(normalised),
				Payload: map[string]any{
					"target":            target,
					"target_normalised": normalised,
				},
			})
		}
	}
	return findings
}

// hashSubKey turns a free-form discriminator into a short stable hex
// suffix so dedupe_key stays well under any column-length issue and
// CJK / unicode targets don't fall foul of the dedupe_key UNIQUE
// btree's collation.
func hashSubKey(s string) string {
	sum := sha1.Sum([]byte(s))
	return hex.EncodeToString(sum[:8])
}

// ─── Rule: missing_frontmatter ──────────────────────────────────

// lintMissingFrontmatter flags pages whose frontmatter is absent or
// empty. Ported from wiki/lint/api.go SQL rule (00037). Pure per-page —
// no project context needed. type / tags frontmatter materially helps
// search + graph, so an empty frontmatter is a legitimate nudge.
func lintMissingFrontmatter(in LintInput) []Finding {
	if len(in.Page.Frontmatter) > 0 {
		return nil
	}
	return []Finding{{
		PageID:      in.Page.ID,
		RuleID:      RuleMissingFrontmatter,
		Title:       "页面缺少 frontmatter",
		Description: "frontmatter 为空 — 建议补 type / tags 让搜索 / 图谱更有用。",
	}}
}

// ─── Rule: duplicate_title ──────────────────────────────────────

// lintDuplicateTitle flags every page whose lowercased+trimmed title
// collides with at least one sibling in the same project. Ported from
// wiki/lint/api.go SQL rule (00037). Relies on TitleGroups (title →
// pageIDs) injected by the worker; nil = not computed → silence.
func lintDuplicateTitle(in LintInput) []Finding {
	if in.TitleGroups == nil {
		return nil
	}
	title := strings.ToLower(strings.TrimSpace(in.Page.Title))
	if title == "" {
		// untitled_page already covers empty titles; don't double-flag.
		return nil
	}
	group := in.TitleGroups[title]
	if len(group) <= 1 {
		return nil
	}
	return []Finding{{
		PageID:      in.Page.ID,
		RuleID:      RuleDuplicateTitle,
		Title:       "标题与同项目其它页重复",
		Description: fmt.Sprintf("项目内有 %d 个页面共享标题「%s」——建议区分或合并。", len(group), in.Page.Title),
		Payload: map[string]any{
			"conflict_count": len(group),
		},
	}}
}

// ─── Rule: orphan_page ──────────────────────────────────────────

// lintOrphanPage flags pages with zero inbound [[wikilink]] references.
// Ported from wiki/lint/api.go SQL rule (00037), but the backlink set
// (IncomingLinkTitles) is built in-memory by the worker scanning every
// block with wikilinkRE instead of a per-page ILIKE seq-scan. nil map =
// not computed → silence (same convention as KnownPageTitles).
//
// A single-page project trivially orphans its only page — accepted
// parity with the old SQL (`p2.id <> p.id`).
func lintOrphanPage(in LintInput) []Finding {
	if in.IncomingLinkTitles == nil {
		return nil
	}
	title := strings.ToLower(strings.TrimSpace(in.Page.Title))
	if title == "" {
		// Empty title can't be a wikilink target; untitled_page covers it.
		return nil
	}
	if _, referenced := in.IncomingLinkTitles[title]; referenced {
		return nil
	}
	return []Finding{{
		PageID:      in.Page.ID,
		RuleID:      RuleOrphanPage,
		Title:       "页面无入链（孤儿页）",
		Description: "这个页没有被任何其它页 [[引用]] — 是孤儿页吗？考虑补充入链或合并。",
	}}
}

// ─── Persistence helper ───────────────────────────────────────

// LintDedupeKey is the canonical dedupe_key for a lint finding. Stable
// across scans: same (page, rule, sub) → same key → ON CONFLICT skip.
func LintDedupeKey(pageID uuid.UUID, ruleID, subKey string) string {
	if subKey == "" {
		return "lint:" + pageID.String() + ":" + ruleID
	}
	return "lint:" + pageID.String() + ":" + ruleID + ":" + subKey
}
