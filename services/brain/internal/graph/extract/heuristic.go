// Package extract turns block content into graph candidates.
//
// The MVP extractor is purely heuristic / regex-based — no LLM, no
// dependency on model-relay. It catches the four high-signal patterns users
// already type when authoring notes:
//
//	#hashtag        → kind=tag,    name=<tag>
//	@mention        → kind=person, name=<handle>
//	[[wikilink]]    → kind=concept,name=<target>      (relation: links_to)
//	URLs            → kind=resource, name=<host+path>  (relation: references)
//
// LLM-driven NER + relation extraction lands in P4.1.5 (it'll plug in via
// the Provider interface in packages/go-sdk/biu/llm).
package extract

import (
	"net/url"
	"regexp"
	"strings"
)

type Candidate struct {
	Kind     string  // tag | person | concept | resource
	Name     string  // canonical name (lower-cased for tag/person)
	Original string  // original surface form
	Relation string  // mentions | links_to | references
	Weight   float32 // 0..1; how confident we are
}

// Each regex pulls the *capture group*, not the whole match.
var (
	// #word — letters, digits, underscore, dash. Must be preceded by start
	// of string or a non-word char so we don't match inside identifiers.
	hashtagRE = regexp.MustCompile(`(?:^|[^\w])#([A-Za-z0-9_\p{Han}-]{2,40})`)

	// @handle — same allowed charset as a github handle.
	mentionRE = regexp.MustCompile(`(?:^|[^\w])@([A-Za-z0-9_-]{2,40})`)

	// [[wikilink]] — anything until the closing brackets, leniently.
	wikilinkRE = regexp.MustCompile(`\[\[([^\]\n]{1,200})\]\]`)

	// URLs — http(s) with at least a host.
	urlRE = regexp.MustCompile(`https?://[^\s<>"]+`)
)

// FromText runs all heuristics against `text` and de-duplicates by
// (kind, name). Order is stable: hashtags → mentions → wikilinks → urls.
func FromText(text string) []Candidate {
	if text == "" {
		return nil
	}
	out := make([]Candidate, 0, 4)
	seen := make(map[string]bool, 8)

	add := func(kind, name, original, relation string, weight float32) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		key := kind + "\x00" + strings.ToLower(name)
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, Candidate{
			Kind: kind, Name: name, Original: original,
			Relation: relation, Weight: weight,
		})
	}

	for _, m := range hashtagRE.FindAllStringSubmatch(text, -1) {
		add("tag", strings.ToLower(m[1]), "#"+m[1], "mentions", 0.9)
	}
	for _, m := range mentionRE.FindAllStringSubmatch(text, -1) {
		add("person", strings.ToLower(m[1]), "@"+m[1], "mentions", 0.8)
	}
	for _, m := range wikilinkRE.FindAllStringSubmatch(text, -1) {
		// `[[Display|target]]` form — keep the target.
		raw := m[1]
		if pipe := strings.IndexByte(raw, '|'); pipe >= 0 {
			raw = strings.TrimSpace(raw[pipe+1:])
		}
		add("concept", raw, "[["+m[1]+"]]", "links_to", 1.0)
	}
	for _, m := range urlRE.FindAllString(text, -1) {
		u, err := url.Parse(m)
		if err != nil || u.Host == "" {
			continue
		}
		// Canonical name: host + path (no query / fragment to keep cardinality
		// reasonable). e.g. "github.com/anthropics/claude-code".
		name := u.Host + strings.TrimRight(u.Path, "/")
		add("resource", name, m, "references", 0.6)
	}
	return out
}

// FromBlockContent extracts from a block's `content` JSON, walking the
// fields we know carry user prose: text, caption, title, items[].
func FromBlockContent(content map[string]any) []Candidate {
	var b strings.Builder
	collect := func(v any) {
		if s, ok := v.(string); ok {
			b.WriteString(s)
			b.WriteByte('\n')
		}
	}
	collect(content["text"])
	collect(content["caption"])
	collect(content["title"])
	if items, ok := content["items"].([]any); ok {
		for _, it := range items {
			collect(it)
		}
	}
	return FromText(b.String())
}
