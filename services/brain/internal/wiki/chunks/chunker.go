// Package chunks turns brain.pages + brain.blocks rows into
// embedding-sized text chunks for the vector retrieval path.
//
// Two design choices worth pinning down up front:
//
//  1. We chunk at the BLOCK level, not at raw-markdown level. biumind's
//     wiki truth-source is already structured (one row per block, JSON
//     content with `text` / `caption` / `items` fields). Re-tokenising the
//     page into markdown just to re-split it would lose block_id linkage
//     and duplicate the editor's segmentation work. (The upstream
//     markdown→block parse lives in wiki/ingest/mdparse.go.)
//
//  2. Every chunk carries a HeadingPath breadcrumb ("Title > Section >
//     Sub") built from the page title + heading blocks, so a short orphan
//     block (e.g. "see also") still embeds with its section context. This
//     mirrors the headingPath in reference/llm_wiki text-chunker.ts:8;
//     the path lives in a separate column and is prepended to the embed
//     input at retrieval time, keeping chunk text clean.
//
// The recursive split ladder (paragraph → sentence → space → hard slice)
// follows the llm_wiki contract; CJK punctuation is included in the
// sentence regex so Chinese blocks split cleanly.
package chunks

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Options controls chunk sizing. Defaults below tuned for OpenAI
// text-embedding-3-small (8k token window, ~1k chars per chunk gives
// ~12 chunks per typical page).
type Options struct {
	TargetChars  int // aim for this many chars per chunk
	MaxChars     int // hard upper bound; oversized atomic pieces still pass
	MinChars     int // smaller chunks merge into the next sibling
	OverlapChars int // overlap between neighbours within a section
}

func (o Options) withDefaults() Options {
	if o.TargetChars == 0 {
		o.TargetChars = 1000
	}
	if o.MaxChars == 0 {
		o.MaxChars = 1500
	}
	if o.MinChars == 0 {
		o.MinChars = 200
	}
	if o.OverlapChars == 0 {
		o.OverlapChars = 200
	}
	if o.MaxChars < o.TargetChars {
		o.MaxChars = o.TargetChars
	}
	if o.OverlapChars >= o.TargetChars {
		o.OverlapChars = o.TargetChars / 2
	}
	return o
}

// BlockSlice is the minimal projection of brain.blocks the chunker needs.
// Callers extract these from store.ListBlocks + content JSON.
//
// Type drives how the block is chunked:
//   - "heading" — updates the headingPath stack but emits no chunk itself
//     (headings carry no body to embed; their context flows into the
//     HeadingPath of the body chunks that follow).
//   - "code" / "list" / "table" — indivisible: emitted as one chunk even
//     when over MaxChars (splitting a code block mid-statement or a GFM
//     table mid-row wrecks embeddings).
//   - "text" / "" — recursive splitByLength ladder (default; back-compat
//     for callers that don't populate Type).
type BlockSlice struct {
	BlockID uuid.UUID
	// Text is the block's primary textual content (content->>'text').
	Text string
	// Caption is the optional secondary text (content->>'caption' on
	// image/code blocks). Concatenated to Text with a separator so the
	// caption survives into the embedding.
	Caption string
	// Type is the block taxonomy label
	// ("heading"/"text"/"code"/"list"/"table"). Empty is treated as
	// "text".
	Type string
	// Level is the heading level (1..6) when Type=="heading".
	Level int
	// Lang is the code fence language when Type=="code" (informational;
	// not currently embedded, reserved for future code-aware retrieval).
	Lang string
	// Items holds list entries when Type=="list".
	Items []string
}

// Chunk is one emitted slice ready for persistence.
type Chunk struct {
	BlockID *uuid.UUID
	Ord     int
	Text    string
	// HeadingPath is the section breadcrumb at emit time, e.g.
	// "Page Title > Section > Sub". Always carries the page title as root
	// so even a short orphan chunk embeds with topical context. Mirrors
	// the headingPath breadcrumb in reference/llm_wiki text-chunker.ts.
	HeadingPath string
	// TokenCount is a rough estimate (chars/4 + utf8 boost). Used for
	// context budget, not billing — it's intentionally approximate so
	// callers don't need a tokenizer dependency at chunk time.
	TokenCount int
}

// headingEntry is one frame in the section breadcrumb stack.
type headingEntry struct {
	level int
	text  string
}

// ChunkPage produces chunks for one page. The page title and any heading
// blocks encountered form a section breadcrumb (HeadingPath) attached to
// EVERY emitted chunk, so a short orphan block (e.g. "see also") still
// embeds with its section context — a strict improvement on the previous
// "prepend # Title to the first chunk only" behaviour.
//
// code / list / table blocks are indivisible (emitted whole even over
// MaxChars); text blocks go through the recursive splitByLength ladder. Pages with
// zero blocks but a non-empty title still emit a single title-only chunk
// so the page is at least retrievable.
func ChunkPage(title string, blocks []BlockSlice, opts Options) []Chunk {
	opts = opts.withDefaults()
	titleLine := strings.TrimSpace(title)

	if len(blocks) == 0 {
		if titleLine == "" {
			return nil
		}
		text := "# " + titleLine
		return []Chunk{{
			Ord:         0,
			Text:        text,
			HeadingPath: titleLine,
			TokenCount:  estimateTokens(text),
		}}
	}

	out := make([]Chunk, 0, len(blocks))
	var stack []headingEntry

	// currentPath renders the live breadcrumb: page title first, then the
	// heading stack in document order, joined by " > ".
	currentPath := func() string {
		parts := make([]string, 0, 1+len(stack))
		if titleLine != "" {
			parts = append(parts, titleLine)
		}
		for _, h := range stack {
			parts = append(parts, h.text)
		}
		return strings.Join(parts, " > ")
	}
	// pushHeading pops same-or-deeper levels before appending, so a new
	// heading at level N clears siblings/sub-headings under it.
	pushHeading := func(level int, text string) {
		for len(stack) > 0 && stack[len(stack)-1].level >= level {
			stack = stack[:len(stack)-1]
		}
		stack = append(stack, headingEntry{level: level, text: text})
	}

	ord := 0
	for _, b := range blocks {
		bt := b.Type
		if bt == "" {
			bt = "text"
		}
		switch bt {
		case "heading":
			t := strings.TrimSpace(b.Text)
			if t == "" {
				continue
			}
			lvl := b.Level
			if lvl < 1 {
				lvl = 1
			}
			pushHeading(lvl, t)
			// Headings carry no body; their context flows via HeadingPath
			// into subsequent chunks. No chunk emitted.
			continue

		case "code", "list", "table":
			text := strings.TrimSpace(blockBodyText(b))
			if text == "" {
				continue
			}
			id := b.BlockID
			hp := currentPath()
			out = append(out, Chunk{
				BlockID:     &id,
				Ord:         ord,
				Text:        text,
				HeadingPath: hp,
				TokenCount:  estimateTokens(text),
			})
			ord++

		default: // text
			raw := strings.TrimSpace(b.Text)
			if cap := strings.TrimSpace(b.Caption); cap != "" {
				if raw == "" {
					raw = cap
				} else {
					raw = raw + "\n" + cap
				}
			}
			if raw == "" {
				continue
			}
			hp := currentPath()
			for _, p := range splitByLength(raw, opts) {
				id := b.BlockID
				out = append(out, Chunk{
					BlockID:     &id,
					Ord:         ord,
					Text:        p,
					HeadingPath: hp,
					TokenCount:  estimateTokens(p),
				})
				ord++
			}
		}
	}

	out = mergeSmall(out, opts)
	out = applyOverlap(out, opts)
	return out
}

// blockBodyText returns the embeddable body of a non-text block: list
// items joined by newline, otherwise the raw Text (code body). text-block
// caption concatenation is handled inline in ChunkPage.
func blockBodyText(b BlockSlice) string {
	if b.Type == "list" && len(b.Items) > 0 {
		return strings.Join(b.Items, "\n")
	}
	return b.Text
}

// applyOverlap prepends a tail slice of the previous chunk to each
// subsequent chunk that shares the same HeadingPath (same section). The
// overlap is purely additive — it never shortens the source chunk — so it
// can't drop content, only repeats a boundary span to keep neighbour
// embeddings coherent. No-op when OverlapChars <= 0 (the prior dead
// config default is now honoured).
//
// Overlap is scoped to same-section neighbours so a new heading doesn't
// inherit the previous section's tail (that would cross a semantic
// boundary and pollute the embedding).
func applyOverlap(chunks []Chunk, opts Options) []Chunk {
	if opts.OverlapChars <= 0 || len(chunks) < 2 {
		return chunks
	}
	for i := 1; i < len(chunks); i++ {
		if chunks[i].HeadingPath != chunks[i-1].HeadingPath {
			continue
		}
		tail := lastRunes(chunks[i-1].Text, opts.OverlapChars)
		if tail == "" {
			continue
		}
		chunks[i].Text = tail + "\n" + chunks[i].Text
		chunks[i].TokenCount = estimateTokens(chunks[i].Text)
	}
	return chunks
}

// lastRunes returns up to n trailing runes of s, skipping leading
// whitespace so the overlap doesn't start mid-token. When s is shorter
// than n runes the whole (trimmed) string is returned.
func lastRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return strings.TrimSpace(s)
	}
	start := len(runes) - n
	for start < len(runes) && unicode.IsSpace(runes[start]) {
		start++
	}
	return strings.TrimSpace(string(runes[start:]))
}

// splitByLength breaks `text` into chunks ≤ TargetChars using the
// recursive ladder paragraph → sentence → space → hard slice. Indivisible
// pieces (no separator found before MaxChars) pass through whole.
func splitByLength(text string, opts Options) []string {
	if utf8.RuneCountInString(text) <= opts.TargetChars {
		return []string{text}
	}
	// Paragraph split first — strongest semantic boundary.
	paras := splitKeepSep(text, paragraphSep)
	out := make([]string, 0, len(paras))
	for _, p := range paras {
		if utf8.RuneCountInString(p) <= opts.TargetChars {
			out = append(out, p)
			continue
		}
		// Descend: sentence boundaries (CJK + ASCII).
		sents := splitKeepSep(p, sentenceSep)
		if allFit(sents, opts.TargetChars) && len(sents) > 1 {
			out = append(out, sents...)
			continue
		}
		// Descend: whitespace.
		toks := splitKeepSep(p, spaceSep)
		if allFit(toks, opts.TargetChars) && len(toks) > 1 {
			out = append(out, toks...)
			continue
		}
		// Last resort: hard slice by rune count.
		out = append(out, hardSlice(p, opts.TargetChars)...)
	}
	// Pack adjacent small pieces back up to TargetChars so we don't
	// emit a flood of tiny rows.
	return packPieces(out, opts)
}

var (
	paragraphSep = regexp.MustCompile(`(\n{2,})`)
	sentenceSep  = regexp.MustCompile(`([。！？!?；;]+\s*|\.\s+)`)
	spaceSep     = regexp.MustCompile(`(\s+)`)
)

// splitKeepSep splits `text` by `sep` but keeps each separator attached
// to the preceding fragment so concatenated pieces equal the original.
func splitKeepSep(text string, sep *regexp.Regexp) []string {
	idx := sep.FindAllStringIndex(text, -1)
	if len(idx) == 0 {
		return []string{text}
	}
	out := make([]string, 0, len(idx)+1)
	last := 0
	for _, m := range idx {
		end := m[1]
		out = append(out, text[last:end])
		last = end
	}
	if last < len(text) {
		out = append(out, text[last:])
	}
	return out
}

func allFit(parts []string, target int) bool {
	for _, p := range parts {
		if utf8.RuneCountInString(p) > target {
			return false
		}
	}
	return true
}

// hardSlice cuts `text` into rune-count windows of at most `target` each.
// We slice on rune boundaries (not bytes) so multibyte chars stay intact.
func hardSlice(text string, target int) []string {
	if target <= 0 {
		return []string{text}
	}
	out := make([]string, 0, utf8.RuneCountInString(text)/target+1)
	runes := []rune(text)
	for i := 0; i < len(runes); i += target {
		end := i + target
		if end > len(runes) {
			end = len(runes)
		}
		out = append(out, string(runes[i:end]))
	}
	return out
}

// packPieces greedily concatenates pieces into chunks of ≤ TargetChars.
// Pieces already over MaxChars pass through unmolested (they're
// indivisible — flagging them oversized is the chunker's job; the
// downstream embedder/store don't care).
func packPieces(pieces []string, opts Options) []string {
	out := make([]string, 0, len(pieces))
	var buf strings.Builder
	bufLen := 0
	flush := func() {
		if bufLen > 0 {
			out = append(out, buf.String())
			buf.Reset()
			bufLen = 0
		}
	}
	for _, p := range pieces {
		n := utf8.RuneCountInString(p)
		if n == 0 {
			continue
		}
		if n > opts.TargetChars {
			flush()
			out = append(out, p)
			continue
		}
		if bufLen+n > opts.TargetChars && bufLen > 0 {
			flush()
		}
		buf.WriteString(p)
		bufLen += n
	}
	flush()
	return out
}

// mergeSmall runs once after packing to absorb any chunk shorter than
// MinChars into its previous sibling, capped at MaxChars. Prevents
// short-paragraph pages from emitting many 30-char rows.
func mergeSmall(chunks []Chunk, opts Options) []Chunk {
	if len(chunks) < 2 {
		return chunks
	}
	out := make([]Chunk, 0, len(chunks))
	for _, c := range chunks {
		n := len(out)
		if n == 0 {
			out = append(out, c)
			continue
		}
		prev := out[n-1]
		prevLen := utf8.RuneCountInString(prev.Text)
		curLen := utf8.RuneCountInString(c.Text)
		if prevLen < opts.MinChars && prevLen+curLen <= opts.MaxChars {
			merged := prev
			merged.Text = prev.Text + "\n" + c.Text
			merged.TokenCount = estimateTokens(merged.Text)
			// Keep prev.BlockID (or carry curr's if prev had none); ord
			// stays as prev.Ord — caller renumbers downstream if needed.
			if merged.BlockID == nil {
				merged.BlockID = c.BlockID
			}
			out[n-1] = merged
			continue
		}
		out = append(out, c)
	}
	// Renumber ord densely (post-merge gaps would confuse callers).
	for i := range out {
		out[i].Ord = i
	}
	return out
}

// estimateTokens is a coarse char→token approximation:
//   - Latin-heavy text: ~4 chars/token
//   - CJK-heavy text:   ~1.5 chars/token (each char is a near-token)
//
// We don't ship a tokenizer here — this number drives context-budget
// heuristics, not billing. ±30% accuracy is good enough.
func estimateTokens(text string) int {
	if text == "" {
		return 0
	}
	cjk := 0
	other := 0
	for _, r := range text {
		if (r >= 0x4E00 && r <= 0x9FFF) || // CJK Unified
			(r >= 0x3000 && r <= 0x303F) || // CJK punctuation
			(r >= 0x3040 && r <= 0x30FF) { // Hiragana/Katakana
			cjk++
		} else {
			other++
		}
	}
	tokens := cjk*2/3 + other/4
	if tokens == 0 {
		tokens = 1
	}
	return tokens
}
