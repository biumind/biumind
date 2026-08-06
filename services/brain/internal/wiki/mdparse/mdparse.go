// mdparse turns one wiki-llm worker markdown body into structured wiki
// blocks, replacing the dumb `strings.Split(body, "\n\n")` paragraph chop
// that used to live in subscriber.go.
//
// We use goldmark (CommonMark) so fence/tilde/CRLF edge cases are handled
// by a battle-tested parser instead of hand-rolled regex. Only the four
// block types the client already renders are emitted —
//
//	heading  {text, level}
//	text     {text}
//	list     {items}
//	code     {text, lang?}
//
// (see apps/client/.../reader/block_to_markdown.dart:115 and
// block_editor.dart:166). store.CreateBlock accepts any type string and
// collab RGA treats content as opaque, so emitting these needs no
// downstream changes. Blockquote / indented-html / thematic-break collapse
// to text so no content is silently dropped; GFM tables (only parsed when
// an extension is wired) likewise fall through to the text default.
package mdparse

import (
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
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
	doc := goldmark.New().Parser().Parse(text.NewReader(src))
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

	case ast.KindBlockquote, ast.KindHTMLBlock:
		// No dedicated block type; flatten to text so the quoted/raw
		// content survives into a paragraph block.
		t := strings.TrimSpace(string(n.Text(src)))
		if t == "" {
			return nil
		}
		return &ParsedBlock{Type: "text", Content: map[string]any{"text": t}}

	default:
		// Unknown block (extension nodes if GFM wired later) → text.
		t := strings.TrimSpace(string(n.Text(src)))
		if t == "" {
			return nil
		}
		return &ParsedBlock{Type: "text", Content: map[string]any{"text": t}}
	}
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
