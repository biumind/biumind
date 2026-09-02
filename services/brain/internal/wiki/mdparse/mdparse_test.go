package mdparse

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseBlocks_Empty(t *testing.T) {
	for _, in := range []string{"", "   ", "\n\n\t"} {
		if got := ParseBlocks(in); got != nil {
			t.Errorf("ParseBlocks(%q) = %v, want nil", in, got)
		}
	}
}

func TestParseBlocks_HeadingLevels(t *testing.T) {
	got := ParseBlocks("# Title\n\n## Sub\n\n### Deep")
	if len(got) != 3 {
		t.Fatalf("want 3 heading blocks, got %d", len(got))
	}
	for i, want := range []struct {
		text  string
		level int
	}{
		{"Title", 1}, {"Sub", 2}, {"Deep", 3},
	} {
		if got[i].Type != "heading" {
			t.Errorf("block %d type = %q, want heading", i, got[i].Type)
		}
		if got[i].Content["text"] != want.text {
			t.Errorf("block %d text = %v, want %q", i, got[i].Content["text"], want.text)
		}
		if got[i].Content["level"] != want.level {
			t.Errorf("block %d level = %v, want %d", i, got[i].Content["level"], want.level)
		}
	}
}

func TestParseBlocks_Paragraph(t *testing.T) {
	got := ParseBlocks("Just a plain paragraph.")
	if len(got) != 1 || got[0].Type != "text" {
		t.Fatalf("want 1 text block, got %+v", got)
	}
	if got[0].Content["text"] != "Just a plain paragraph." {
		t.Errorf("text = %v", got[0].Content["text"])
	}
}

func TestParseBlocks_FencedCodeWithLang(t *testing.T) {
	src := "```go\nfunc main() {}\n```"
	got := ParseBlocks(src)
	if len(got) != 1 || got[0].Type != "code" {
		t.Fatalf("want 1 code block, got %+v", got)
	}
	if got[0].Content["lang"] != "go" {
		t.Errorf("lang = %v, want go", got[0].Content["lang"])
	}
	if got[0].Content["text"] != "func main() {}" {
		t.Errorf("code body = %q, want %q", got[0].Content["text"], "func main() {}")
	}
}

func TestParseBlocks_FencedCodeNoLang(t *testing.T) {
	src := "```\nbare code\n```"
	got := ParseBlocks(src)
	if len(got) != 1 || got[0].Type != "code" {
		t.Fatalf("want 1 code block, got %+v", got)
	}
	if _, has := got[0].Content["lang"]; has {
		t.Errorf("unwanted lang key: %v", got[0].Content["lang"])
	}
	if got[0].Content["text"] != "bare code" {
		t.Errorf("code body = %q", got[0].Content["text"])
	}
}

func TestParseBlocks_TildeFence(t *testing.T) {
	// Tilde fences are CommonMark-legal and a known regex-lexer pitfall.
	src := "~~~python\nprint(1)\n~~~"
	got := ParseBlocks(src)
	if len(got) != 1 || got[0].Type != "code" {
		t.Fatalf("tilde fence should parse to code, got %+v", got)
	}
	if got[0].Content["lang"] != "python" {
		t.Errorf("lang = %v, want python", got[0].Content["lang"])
	}
}

func TestParseBlocks_UnorderedList(t *testing.T) {
	got := ParseBlocks("- alpha\n- beta\n- gamma")
	if len(got) != 1 || got[0].Type != "list" {
		t.Fatalf("want 1 list block, got %+v", got)
	}
	items, ok := got[0].Content["items"].([]string)
	if !ok {
		t.Fatalf("items not []string: %T", got[0].Content["items"])
	}
	want := []string{"alpha", "beta", "gamma"}
	if !reflect.DeepEqual(items, want) {
		t.Errorf("items = %v, want %v", items, want)
	}
}

func TestParseBlocks_OrderedList(t *testing.T) {
	got := ParseBlocks("1. first\n2. second")
	if len(got) != 1 || got[0].Type != "list" {
		t.Fatalf("want 1 list block, got %+v", got)
	}
	items := got[0].Content["items"].([]string)
	if len(items) != 2 || items[0] != "first" || items[1] != "second" {
		t.Errorf("items = %v", items)
	}
}

func TestParseBlocks_BlockquoteFallsBackToText(t *testing.T) {
	got := ParseBlocks("> a quoted line")
	if len(got) != 1 || got[0].Type != "text" {
		t.Fatalf("blockquote should collapse to text, got %+v", got)
	}
	if got[0].Content["text"] != "a quoted line" {
		t.Errorf("text = %v", got[0].Content["text"])
	}
}

func TestParseBlocks_ThematicBreakDropped(t *testing.T) {
	got := ParseBlocks("above\n\n---\n\nbelow")
	// heading? no — "above"/"below" are paragraphs, "---" dropped.
	if len(got) != 2 {
		t.Fatalf("hr should be dropped; want 2 blocks, got %d: %+v", len(got), got)
	}
	for _, b := range got {
		if b.Type != "text" {
			t.Errorf("unexpected block type %q", b.Type)
		}
	}
}

func TestParseBlocks_InlineMarkupPreservedAsMarkdown(t *testing.T) {
	// Paragraph.Text returns the RAW inline markdown (goldmark keeps
	// emphasis/link/code-span sigils), which is what we want: text blocks
	// are rendered through GptMarkdown at read time, same as the editor's
	// TextField stores whatever the user types. Heading text is the only
	// place we strip sigils — its `#` is carried by the separate level
	// field (see TestParseBlocks_HeadingLevels).
	got := ParseBlocks("This is **bold** and [a link](https://x) and `code`.")
	if len(got) != 1 || got[0].Type != "text" {
		t.Fatalf("want 1 text block, got %+v", got)
	}
	text := got[0].Content["text"].(string)
	for _, want := range []string{"**bold**", "[a link](https://x)", "`code`"} {
		if !strings.Contains(text, want) {
			t.Errorf("raw inline markdown %q lost from text: %q", want, text)
		}
	}
}

func TestParseBlocks_WikilinkPreserved(t *testing.T) {
	// `[[target]]` is not CommonMark link syntax → goldmark keeps it as
	// literal text, which is exactly what we want (the reader rewrites
	// wikilinks at render time; the stored block must keep the source).
	got := ParseBlocks("see [[claude]] and [[gpt|alias]] here")
	if len(got) != 1 || got[0].Type != "text" {
		t.Fatalf("want 1 text block, got %+v", got)
	}
	text := got[0].Content["text"].(string)
	if !strings.Contains(text, "[[claude]]") || !strings.Contains(text, "[[gpt|alias]]") {
		t.Errorf("wikilink literals lost from text: %q", text)
	}
}

func TestParseBlocks_MixedDocOrder(t *testing.T) {
	src := "# Title\n\nintro paragraph.\n\n```go\nx := 1\n```\n\n- one\n- two"
	got := ParseBlocks(src)
	if len(got) != 4 {
		t.Fatalf("want 4 blocks (heading/text/code/list), got %d: %+v", len(got), got)
	}
	wantTypes := []string{"heading", "text", "code", "list"}
	for i, w := range wantTypes {
		if got[i].Type != w {
			t.Errorf("block %d type = %q, want %q (full: %+v)", i, got[i].Type, w, got)
		}
	}
}

func TestParseBlocks_EmptyCodeBlockDropped(t *testing.T) {
	got := ParseBlocks("```\n```")
	if len(got) != 0 {
		t.Errorf("empty fence should produce no block, got %+v", got)
	}
}

// ── GFM tables ─────────────────────────────────────────────────

func TestParseBlocks_GFMTable(t *testing.T) {
	src := "# Spec\n\n" +
		"| Name | Type | Notes |\n" +
		"| :--- | ---: | :---: |\n" +
		"| alpha | int | *first* |\n" +
		"| beta | string | [[link]] |\n" +
		"\ntrailing paragraph."
	got := ParseBlocks(src)
	if len(got) != 3 {
		t.Fatalf("want 3 blocks (heading/table/text), got %d: %+v", len(got), got)
	}
	if got[1].Type != "table" {
		t.Fatalf("block 1 type = %q, want table (full: %+v)", got[1].Type, got)
	}
	raw, _ := got[1].Content["text"].(string)
	want := "| Name | Type | Notes |\n" +
		"| :--- | ---: | :---: |\n" +
		"| alpha | int | *first* |\n" +
		"| beta | string | [[link]] |"
	if raw != want {
		t.Errorf("table raw markdown mismatch:\ngot:  %q\nwant: %q", raw, want)
	}
	if got[2].Type != "text" {
		t.Errorf("block 2 type = %q, want text", got[2].Type)
	}
}

func TestParseBlocks_TableRoundTrip(t *testing.T) {
	// mdparse → (verbatim text) → mdparse must reproduce the same table
	// block. This pins the BlocksToMarkdown round-trip contract on the
	// parse side: the stored raw markdown re-parses to a table, not a
	// run of text blocks.
	src := "| a | b |\n| - | - |\n| 1 | 2 |"
	first := ParseBlocks(src)
	if len(first) != 1 || first[0].Type != "table" {
		t.Fatalf("want 1 table block, got %+v", first)
	}
	raw, _ := first[0].Content["text"].(string)
	second := ParseBlocks(raw)
	if len(second) != 1 || second[0].Type != "table" {
		t.Fatalf("re-parse must yield 1 table block, got %+v", second)
	}
	raw2, _ := second[0].Content["text"].(string)
	if raw2 != raw {
		t.Errorf("round-trip drifted:\nfirst:  %q\nsecond: %q", raw, raw2)
	}
}

func TestParseBlocks_PipeTextIsNotATable(t *testing.T) {
	// A single pipe-y line without a delimiter row is plain paragraph
	// text, not a table — make sure the extension doesn't over-trigger.
	got := ParseBlocks("a | b | c")
	if len(got) != 1 || got[0].Type != "text" {
		t.Fatalf("want 1 text block, got %+v", got)
	}
}
