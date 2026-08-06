// Package enrich implements the wikilink enrichment pipeline for biumind.
//
// Goal: after a page is created or edited, ask an LLM to identify terms
// in the page text that should become [[wikilink]] references to other
// existing pages, then apply those substitutions deterministically.
//
// Design:
//
//	The LLM does NOT rewrite the page. It returns ONLY a JSON list of
//	{term, target} substitutions. We do the actual string replacement.
//	This bounds the blast radius — the worst the LLM can do is propose
//	bad links; it cannot mutate, translate, or delete user content.
//
// The "wiki index" passed to the LLM is the list of candidate target
// page titles within the same project. Cross-project enrichment is out
// of scope (privacy + scale).
package enrich

import (
	"encoding/json"
	"sort"
	"strings"
)

// LinkEntry is one substitution proposed by the LLM.
type LinkEntry struct {
	Term   string `json:"term"`
	Target string `json:"target"`
}

// BuildIndex renders the page-title list as the markdown the LLM expects.
// Sorted for determinism; titles are passed verbatim — no escaping
// because the prompt says "exact substring".
func BuildIndex(titles []string) string {
	clean := make([]string, 0, len(titles))
	seen := map[string]struct{}{}
	for _, t := range titles {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		key := strings.ToLower(t)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		clean = append(clean, "- "+t)
	}
	sort.Strings(clean)
	return strings.Join(clean, "\n")
}

// SystemPrompt is the v2 prompt — list-of-substitutions, never rewrite.
// Kept verbatim from llm_wiki to preserve LLM behaviour parity.
const SystemPrompt = `You identify which terms in a wiki page should become [[wikilinks]] pointing to existing wiki pages.

You will receive:
  - a wiki index listing existing pages (each line roughly like ` + "`- pagename`" + `)
  - the content of ONE wiki page

Return a JSON object listing which terms in the page content should be linked to which index entries.

Response format (EXACTLY this JSON shape, nothing else):
{
  "links": [
    { "term": "exact text appearing in the content", "target": "index page name" }
  ]
}

Rules:
- Each "term" MUST be a literal substring present in the page content (case-sensitive).
- Each "target" MUST be a page listed in the wiki index.
- Include at most one entry per target (first mention).
- Only include clearly-matching terms (e.g. if content mentions 'Transformer' and index has 'transformer', target='transformer' is correct).
- If no terms should be linked, return ` + "`{\"links\": []}`" + `.
- Do NOT output preamble, explanations, or markdown fences — ONLY the JSON object.

## Wiki Index
`

// BuildSystemMessage assembles the full system prompt with the index appended.
func BuildSystemMessage(index string) string {
	return SystemPrompt + index
}

// ParseLinkResponse extracts a [{term,target}] list from raw LLM output.
// Tolerant of code-fence wrappers and prose preamble — pulls the first
// balanced {...} and JSON-decodes it. Anything malformed → empty slice.
func ParseLinkResponse(raw string) []LinkEntry {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	// Strip ```json fences if present.
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```JSON")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)

	start := strings.IndexByte(raw, '{')
	if start < 0 {
		return nil
	}
	depth := 0
	inStr := false
	escape := false
	end := -1
	for i := start; i < len(raw); i++ {
		ch := raw[i]
		if escape {
			escape = false
			continue
		}
		if inStr && ch == '\\' {
			escape = true
			continue
		}
		if ch == '"' {
			inStr = !inStr
			continue
		}
		if inStr {
			continue
		}
		switch ch {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				end = i
			}
		}
		if end >= 0 {
			break
		}
	}
	if end < 0 {
		return nil
	}
	var doc struct {
		Links []LinkEntry `json:"links"`
	}
	if err := json.Unmarshal([]byte(raw[start:end+1]), &doc); err != nil {
		return nil
	}
	out := doc.Links[:0]
	for _, l := range doc.Links {
		if l.Term == "" || l.Target == "" {
			continue
		}
		out = append(out, l)
	}
	return out
}

// ApplyLinks rewrites `content` so that the FIRST occurrence of each term
// (outside YAML frontmatter and outside an existing [[...]] block) is
// wrapped in `[[target]]` (when term ≈ target, case-insensitive) or
// `[[target|term]]` (otherwise).
//
// Each target is linked at most once. Terms that don't appear, or only
// appear inside an existing wikilink, are silently dropped — matches
// llm_wiki behaviour.
func ApplyLinks(content string, links []LinkEntry) string {
	if len(links) == 0 || content == "" {
		return content
	}

	// Split off YAML frontmatter so we never modify it. Layout:
	//   ---\n
	//   key: value\n
	//   ---\n
	//   <body>
	frontmatter, body := splitFrontmatter(content)

	linkedTargets := map[string]struct{}{}
	for _, link := range links {
		key := strings.ToLower(link.Target)
		if _, dup := linkedTargets[key]; dup {
			continue
		}
		idx := findUnlinkedOccurrence(body, link.Term)
		if idx < 0 {
			continue
		}
		var replacement string
		if strings.EqualFold(link.Term, link.Target) {
			replacement = "[[" + link.Term + "]]"
		} else {
			replacement = "[[" + link.Target + "|" + link.Term + "]]"
		}
		body = body[:idx] + replacement + body[idx+len(link.Term):]
		linkedTargets[key] = struct{}{}
	}
	return frontmatter + body
}

// splitFrontmatter returns (frontmatter, body). Frontmatter includes the
// trailing "\n---\n" so concatenating frontmatter+body reconstructs the
// original. If the input has no frontmatter, frontmatter == "".
func splitFrontmatter(s string) (string, string) {
	if !strings.HasPrefix(s, "---\n") {
		return "", s
	}
	end := strings.Index(s[4:], "\n---\n")
	if end < 0 {
		return "", s
	}
	cut := 4 + end + len("\n---\n")
	return s[:cut], s[cut:]
}

// findUnlinkedOccurrence returns the byte index of the first occurrence
// of `term` in `text` that is NOT inside any existing `[[...]]` wikilink.
// Returns -1 when no clean match exists.
//
// llm_wiki's TS version only checks the two characters immediately
// before the match for `[[`. That's wrong for `[[target|term]]` shapes:
// the term sits after `|` and isn't caught. We scan backward to the
// nearest `[[` or `]]` boundary and skip the candidate if we're still
// inside an open wikilink.
func findUnlinkedOccurrence(text, term string) int {
	if term == "" {
		return -1
	}
	from := 0
	for from < len(text) {
		idx := strings.Index(text[from:], term)
		if idx < 0 {
			return -1
		}
		abs := from + idx
		if insideWikilink(text, abs) {
			from = abs + len(term)
			continue
		}
		return abs
	}
	return -1
}

// insideWikilink reports whether the byte at `pos` falls inside an open
// `[[...]]` block — i.e. the most recent `[[` or `]]` boundary before
// `pos` is `[[`.
func insideWikilink(text string, pos int) bool {
	openIdx := strings.LastIndex(text[:pos], "[[")
	if openIdx < 0 {
		return false
	}
	closeIdx := strings.LastIndex(text[:pos], "]]")
	return closeIdx < openIdx
}
