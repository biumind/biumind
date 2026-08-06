// Package ingest is the shared content ingestion pipeline.
//
// Used by:
//   - Brain server (NATS consumer; ingests user-uploaded sources)
//   - biu CLI `biu ingest <file>` (lets users ingest into Wiki from terminal)
//
// Both contexts share:
//   - The same parser (Markdown / HTML / plain text).
//   - The same chunker.
//   - The same two-step CoT prompts.
//   - The same Provider abstraction (model-relay / Anthropic-direct / etc.)
//
// Embeddings (BGE / text-embedding-3) live in a separate `embed` package
// invoked at the end of the pipeline; not required for MVP.
package ingest

import "time"

// SourceKind identifies the input format.
type SourceKind string

const (
	KindMarkdown  SourceKind = "markdown"
	KindHTML      SourceKind = "html"
	KindPlainText SourceKind = "plain"
)

// Source is the unparsed input.
type Source struct {
	Kind     SourceKind
	URL      string            // origin (optional; webclip / file://)
	Title    string            // optional, parser may extract / overwrite
	Content  string            // raw text (HTML / MD / plain)
	Metadata map[string]string // tags, language, fetched_at, ...
}

// ParsedDoc is the structured intermediate representation: title + ordered chunks.
type ParsedDoc struct {
	Title  string
	Chunks []Chunk
}

// Chunk is one logical paragraph / section identified by the parser.
type Chunk struct {
	Heading  string // empty if not under a heading
	Text     string
	Position float64 // for fractional indexing later
}

// Outline is the LLM's analysis output: a candidate page structure.
type Outline struct {
	Title    string         `json:"title"`
	Summary  string         `json:"summary"`
	Sections []OutlineEntry `json:"sections"`
	Tags     []string       `json:"tags"`
}

type OutlineEntry struct {
	Heading string `json:"heading"`
	Goal    string `json:"goal"`
}

// PageDraft is the final structured page ready to write to Wiki.
type PageDraft struct {
	Title       string
	Frontmatter map[string]any
	Blocks      []BlockDraft
	GeneratedAt time.Time
	Outline     Outline
	SourceURL   string
}

// BlockDraft mirrors brain.blocks shape (kept independent so we don't pull
// the entire wiki/store package into ingest).
type BlockDraft struct {
	Type     string // "heading" / "text" / "list" / ...
	Position float64
	Content  map[string]any
}
