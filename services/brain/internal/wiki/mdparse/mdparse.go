// mdparse turns one wiki-llm worker markdown body into structured wiki
// blocks, replacing the dumb `strings.Split(body, "\n\n")` paragraph chop
// that used to live in subscriber.go.
//
// We use goldmark (CommonMark + GFM table extension) so fence/tilde/CRLF
// edge cases are handled by a battle-tested parser instead of hand-rolled
// regex. Only the block types the client already renders are emitted —
//
//	heading  {text, level}
//	text     {text}
//	list     {items}
//	code     {text, lang?}
//	table    {text}  — raw markdown table, indivisible (chunker treats it
//	                   like code: one chunk even when oversized)
//
// (see apps/client/.../reader/block_to_markdown.dart:115). The table block
// keeps the ORIGINAL markdown source (not a cell model) so
// store.BlocksToMarkdown round-trips verbatim and the client reader renders
// it through GptMarkdown's GFM table support. store.CreateBlock accepts any
// type string and collab RGA treats content as opaque, so emitting these
// needs no downstream changes. Blockquote / indented-html / thematic-break
// collapse to text so no content is silently dropped.
package mdparse

import (
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	extensionast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
)

// ParsedBlock is one structured block shaped to drop straight into
// wikistore.CreateBlockInput{Type, Content}.
type ParsedBlock struct {
	Type    string
	Content map[string]any
}

// ParseBlocks parses markdown into ordered structured blocks. Returns nil
// for empty / whitespace-only input so callers can treat "no body" as
// "no blocks" without a separate emptiness check.
func ParseBlocks(markdown string) []ParsedBlock {
	src := []byte(strings.TrimSpace(markdown))
	if len(src) == 0 {
		return nil
	}
	doc := goldmark.New(goldmark.WithExtensions(extension.Table)).Parser().Parse(text.NewReader(src))
	var out []ParsedBlock
	for child := doc.FirstChild(); child != nil; child = child.NextSibling() {
		if pb := blockFromNode(child, src); pb != nil {
			out = append(out, *pb)
		}
	}
	return out
}

// blockFromNode maps one top-level AST node to a ParsedBlock. A nil return
// means "drop this node" (thematic break, or a construct that parsed to
// empty text).
func blockFromNode(n ast.Node, src []byte) *ParsedBlock {
	switch n.Kind() {
	case ast.KindHeading:
		h := n.(*ast.Heading)
		t := strings.TrimSpace(string(h.Text(src)))
		if t == "" {
			return nil
		}
		level := h.Level
		if level < 1 {
			level = 1
		}
		return &ParsedBlock{Type: "heading", Content: map[string]any{
			"text": t, "level": level,
		}}

	case ast.KindParagraph:
		t := strings.TrimSpace(string(n.Text(src)))
		if t == "" {
			return nil
		}
		return &ParsedBlock{Type: "text", Content: map[string]any{"text": t}}

	case ast.KindFencedCodeBlock, ast.KindCodeBlock:
		body := strings.TrimSpace(string(n.Text(src)))
		if body == "" {
			return nil
		}
		content := map[string]any{"text": body}
		if fc, ok := n.(*ast.FencedCodeBlock); ok {
			if lang := strings.TrimSpace(string(fc.Language(src))); lang != "" {
				content["lang"] = lang
			}
		}
		return &ParsedBlock{Type: "code", Content: content}

	case ast.KindList:
		items := listItems(n, src)
		if len(items) == 0 {
			return nil
		}
		return &ParsedBlock{Type: "list", Content: map[string]any{"items": items}}

	case ast.KindThematicBreak:
		// <hr> carries no text — drop rather than emit an empty block.
		return nil

	case extensionast.KindTable:
		// GFM table → indivisible block holding the raw markdown source
		// (see rawSource — the extension gives the Table node no lines of
		// its own, so the span is recovered from cell segments). The table
		// survives verbatim (alignments, inline formatting in cells and
		// all); re-parsing it back through goldmark reproduces the same
		// table block, keeping the mdparse → BlocksToMarkdown → mdparse
		// round-trip stable.
		raw := strings.TrimSpace(rawSource(n, src))
		if raw == "" {
			return nil
		}
		return &ParsedBlock{Type: "table", Content: map[string]any{"text": raw}}

	case ast.KindBlockquote, ast.KindHTMLBlock:
		// No dedicated block type; flatten to text so the quoted/raw
		// content survives into a paragraph block.
		t := strings.TrimSpace(string(n.Text(src)))
		if t == "" {
			return nil
		}
		return &ParsedBlock{Type: "text", Content: map[string]any{"text": t}}

	default:
		// Unknown block (further extension nodes if more GFM extensions
		// get wired later) → text.
		t := strings.TrimSpace(string(n.Text(src)))
		if t == "" {
			return nil
		}
		return &ParsedBlock{Type: "text", Content: map[string]any{"text": t}}
	}
}

// rawSource reconstructs a GFM table node's verbatim markdown. The table
// extension builds Table/TableRow nodes WITHOUT line segments — only the
// TableCell leaves carry (trimmed) segments — so n.Lines() is empty and we
// must derive the span from the cells: min/max cell segment bounds,
// expanded outward to physical line boundaries so leading/trailing pipes
// and alignment rows survive. Unlike n.Text(src) (which flattens to cell
// text), this keeps the pipe/alignment syntax intact for round-trip
// fidelity.
func rawSource(n ast.Node, src []byte) string {
	start, stop := -1, -1
	_ = ast.Walk(n, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if node.Type() != ast.TypeBlock {
			return ast.WalkContinue, nil
		}
		lines := node.Lines()
		for i := 0; i < lines.Len(); i++ {
			seg := lines.At(i)
			if start == -1 || seg.Start < start {
				start = seg.Start
			}
			if seg.Stop > stop {
				stop = seg.Stop
			}
		}
		return ast.WalkContinue, nil
	})
	if start < 0 || stop <= start {
		return ""
	}
	for start > 0 && src[start-1] != '\n' {
		start--
	}
	for stop < len(src) && src[stop] != '\n' {
		stop++
	}
	return string(src[start:stop])
}

// listItems walks a List node's direct ListItem children and returns each
// item's flattened text. Nested lists within an item are folded into that
// item's text (the block taxonomy doesn't model nesting yet).
func listItems(list ast.Node, src []byte) []string {
	var out []string
	for child := list.FirstChild(); child != nil; child = child.NextSibling() {
		t := strings.TrimSpace(string(child.Text(src)))
		if t == "" {
			continue
		}
		out = append(out, t)
	}
	return out
}
