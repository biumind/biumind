// Package vision ports llm_wiki's vision-caption pipeline.
//
// What it does: scans wiki blocks for markdown image refs `![alt](url)`
// with empty (or near-empty) alt text, fetches the image, asks a vision
// LLM for a 2-4 sentence factual caption, and rewrites the block text
// to inline that caption as the alt: `![caption](url)`.
//
// Why caption-as-alt rather than a separate field:
//   - search.extractImages already mines `![alt](url)` for query-match,
//     so populating alt is an immediate retrieval win.
//   - chunker concatenates `content.text` (which contains the markdown)
//     into the embedding, so the caption flows into vector search too.
//   - the human-facing Markdown Just Works in any renderer that reads
//     standard image alt text.
//
// Caching: every {url → caption} pair is content-addressed by sha256(url)
// in brain.image_captions. Re-uses across pages, projects, even across
// vision-model changes (model is recorded so a future migration can
// invalidate when we upgrade).
package vision

import (
	"crypto/sha256"
	"regexp"
	"strings"
)

// ImageRef is one markdown image reference parsed out of a block.
type ImageRef struct {
	// FullMatch is the entire matched substring including the `![..](..)`.
	FullMatch string
	// Alt is the text between the brackets (may be empty).
	Alt string
	// URL is the raw URL inside the parens.
	URL string
	// Index is the byte offset into the source text where FullMatch starts.
	Index int
}

// markdownImageRE matches `![alt](url)` — same shape as
// search.extractImages so they agree on what counts as an image.
//
// We deliberately do NOT match HTML <img> tags — biumind's wiki
// emits markdown only, and matching <img> would catch literal
// `<img>` mentions inside code blocks.
var markdownImageRE = regexp.MustCompile(`!\[([^\]]*)\]\(([^)\s]+)(?:\s+"[^"]*")?\)`)

// FindImages extracts every image reference out of `text`.
func FindImages(text string) []ImageRef {
	matches := markdownImageRE.FindAllStringSubmatchIndex(text, -1)
	out := make([]ImageRef, 0, len(matches))
	for _, m := range matches {
		// m = [matchStart, matchEnd, altStart, altEnd, urlStart, urlEnd]
		full := text[m[0]:m[1]]
		alt := text[m[2]:m[3]]
		url := text[m[4]:m[5]]
		out = append(out, ImageRef{
			FullMatch: full,
			Alt:       strings.TrimSpace(alt),
			URL:       strings.TrimSpace(url),
			Index:     m[0],
		})
	}
	return out
}

// NeedsCaption returns true when the alt text is too thin to carry
// retrieval signal — empty after trim, or so short that it's almost
// certainly a placeholder ("img", "fig", "image"). Anything ≥3 chars
// of real content is left alone (the user / source already supplied
// a caption).
func NeedsCaption(alt string) bool {
	a := strings.TrimSpace(strings.ToLower(alt))
	if a == "" {
		return true
	}
	switch a {
	case "img", "image", "fig", "figure", "screenshot", "picture", "photo":
		return true
	}
	return false
}

// ApplyCaptions rewrites `text` so that for every URL with a caption in
// the map, the FIRST `![alt](url)` whose alt looks like a placeholder
// becomes `![caption](url)`. URLs without captions, and already-named
// alt text, are left untouched.
//
// Returns (newText, changed). `changed` is false when nothing matched
// — caller can then skip a no-op UpdateBlock round-trip.
func ApplyCaptions(text string, captions map[string]string) (string, bool) {
	if len(captions) == 0 {
		return text, false
	}
	refs := FindImages(text)
	if len(refs) == 0 {
		return text, false
	}
	// Build new text by walking left → right; for each ref decide
	// whether to substitute. We can't use a single regex .ReplaceAll
	// because the substitution depends on (NeedsCaption(alt) && have
	// caption for that URL) AND only the first qualifying occurrence
	// per URL.
	var b strings.Builder
	cursor := 0
	used := make(map[string]bool, len(captions))
	changed := false
	for _, r := range refs {
		b.WriteString(text[cursor:r.Index])
		cap, ok := captions[r.URL]
		if ok && NeedsCaption(r.Alt) && !used[r.URL] {
			b.WriteString("![")
			b.WriteString(escapeAlt(cap))
			b.WriteString("](")
			b.WriteString(r.URL)
			b.WriteString(")")
			used[r.URL] = true
			changed = true
		} else {
			b.WriteString(r.FullMatch)
		}
		cursor = r.Index + len(r.FullMatch)
	}
	b.WriteString(text[cursor:])
	return b.String(), changed
}

// escapeAlt sanitises a caption so it's safe to splice between `![` and `]`.
// Newlines and `]` are the only characters that break the markdown. We
// also collapse internal whitespace to single spaces — the LLM is told
// to emit plain text but we don't trust it to follow.
func escapeAlt(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "]", "")
	// Collapse runs of whitespace.
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	return strings.TrimSpace(s)
}

// HashURL returns the sha256 digest of `url` — used as the PK of
// brain.image_captions.
func HashURL(url string) []byte {
	sum := sha256.Sum256([]byte(url))
	return sum[:]
}

// CaptionPrompt is the no-context factual-description prompt — same
// language as llm_wiki Phase 3a so both systems converge on the same
// caption shape for the same image.
const CaptionPrompt = "Describe this image factually for a knowledge-base index. Include: any visible text verbatim, chart axes and values, diagram structure (boxes/arrows/labels), key visual elements. Do NOT speculate or editorialize. 2 to 4 sentences. Output plain text only — no markdown, no preamble."
