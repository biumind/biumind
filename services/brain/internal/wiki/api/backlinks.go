// Backlinks endpoint — "what other pages link to this one?"
//
//	GET /v1/wiki/projects/{pid}/pages/{id}/backlinks
//
// Returns one row per (referring page, referring block) pair. Same
// page can appear multiple times if it has [[X]] in two different
// blocks — that surfaces context, which the UI shows as separate
// snippets.
//
// Algorithm:
//   1. SQL pre-filter: blocks whose text contains '[[' (cheap LIKE).
//   2. Go-side regex: extract every [[target]] / [[target|alias]] from
//      the text, lowercase the target, compare against this page's
//      lowercased title.
//   3. Snippet: 80 chars centered on the matched wikilink.
//
// We run it at request time rather than maintaining a denormalised
// edge table because:
//   - the worker that maintains brain.relevance / page_relevance
//     already needs the same regex, and a parallel edge table would
//     drift with edits unless we re-introduced the pubsub trigger
//     llm_wiki had — which biumind's "self-healing tick" architecture
//     deliberately avoided.
//   - typical projects (<1k pages, <10k blocks) finish the scan in
//     low double-digit ms; we cap the SQL at 500 candidate blocks
//     per request to bound the worst case.

package api

import (
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

// backlinksRE matches [[target]] / [[target|alias]] — the target
// capture is what we compare against the lower-cased page title.
var backlinksRE = regexp.MustCompile(`\[\[([^\]|\n]+)(?:\|[^\]\n]*)?\]\]`)

type backlinkOut struct {
	PageID    string `json:"page_id"`
	PageTitle string `json:"page_title"`
	BlockID   string `json:"block_id"`
	Snippet   string `json:"snippet"`
	UpdatedAt string `json:"updated_at"`
}

func (s *Server) handleListBacklinks(w http.ResponseWriter, r *http.Request) {
	pid, err := uuid.Parse(r.PathValue("pid"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_project_id", "")
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_id", "")
		return
	}
	if !s.ownsProject(w, r, pid) {
		return
	}
	target, err := s.Store.GetPage(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "")
		return
	}
	if target.ProjectID != pid {
		writeErr(w, http.StatusNotFound, "not_found", "")
		return
	}
	wantTitle := strings.ToLower(strings.TrimSpace(target.Title))
	if wantTitle == "" {
		// Untitled page can't be a wikilink target.
		writeJSON(w, http.StatusOK, map[string]any{"backlinks": []backlinkOut{}})
		return
	}

	// Pull candidate blocks: same project, has '[[', not the target's
	// own page, alive. 500 cap is the safety floor — large projects
	// would route through a background-built denormalised index.
	cands, err := s.Store.ListBacklinkCandidates(r.Context(), pid, id, 500)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	out := make([]backlinkOut, 0, len(cands))
	for _, c := range cands {
		match, found := matchWikilinkInText(c.Text, wantTitle)
		if !found {
			continue
		}
		out = append(out, backlinkOut{
			PageID:    c.PageID.String(),
			PageTitle: c.PageTitle,
			BlockID:   c.BlockID.String(),
			Snippet:   match,
			UpdatedAt: c.UpdatedAt.UTC().Format(time.RFC3339),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"backlinks": out})
}

// matchWikilinkInText scans `text` for any [[target]] whose lowercased
// target equals `wantLower`. Returns (snippet, true) on first hit;
// snippet is up to 160 chars centered on the match. (false, "") on miss.
//
// Snippet width is balanced against the eventual UI list-card density —
// 80–160 chars give the reader enough context to recognise WHY the
// link was made without dominating the panel.
func matchWikilinkInText(text, wantLower string) (string, bool) {
	if !strings.Contains(text, "[[") {
		return "", false
	}
	matches := backlinksRE.FindAllStringSubmatchIndex(text, -1)
	for _, m := range matches {
		// m[2:4] = first capture group (target) byte range.
		target := strings.ToLower(strings.TrimSpace(text[m[2]:m[3]]))
		if target == wantLower {
			return snippetAround(text, m[0], m[1]), true
		}
	}
	return "", false
}

// snippetAround returns up to 160 chars of `text` centered on
// [hit, end), with leading/trailing "…" when truncated. Guards
// against tail-end and head-end edge cases.
func snippetAround(text string, hit, end int) string {
	const radius = 60
	start := hit - radius
	stop := end + radius
	prefix := ""
	suffix := ""
	if start < 0 {
		start = 0
	} else {
		prefix = "…"
	}
	if stop > len(text) {
		stop = len(text)
	} else {
		suffix = "…"
	}
	out := strings.ReplaceAll(text[start:stop], "\n", " ")
	out = strings.TrimSpace(out)
	return prefix + out + suffix
}
