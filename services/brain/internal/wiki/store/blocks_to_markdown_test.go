// BlocksToMarkdown round-trip tests — pure functions, no DB needed
// (unlike store_test.go / revisions_test.go which skip on DATABASE_URL).

package store

import (
	"strings"
	"testing"

	"github.com/biumind/biumind/services/brain/internal/wiki/mdparse"
)

// parsedToBlocks adapts mdparse output to the Block shape BlocksToMarkdown
// consumes (same mapping insertBlocksTx persists).
func parsedToBlocks(parsed []mdparse.ParsedBlock) []*Block {
	out := make([]*Block, 0, len(parsed))
	for _, p := range parsed {
		out = append(out, &Block{Type: p.Type, Content: p.Content})
	}
	return out
}

func TestBlocksToMarkdown_TableRoundTrip(t *testing.T) {
	// mdparse → BlocksToMarkdown → mdparse must be stable for GFM tables:
	// the table block keeps its raw markdown verbatim, so the projection
	// re-parses to the SAME table block (pre-table-type the projection
	// degraded the table into plain text paragraphs).
	src := "# Spec\n\n" +
		"| Name | Type |\n" +
		"| --- | --- |\n" +
		"| alpha | int |\n" +
		"| beta | string |\n" +
		"\ntrailing paragraph."
	first := mdparse.ParseBlocks(src)
	md := BlocksToMarkdown(parsedToBlocks(first))
	if !strings.Contains(md, "| Name | Type |") {
		t.Fatalf("table markdown lost from projection: %q", md)
	}
	second := mdparse.ParseBlocks(md)
	if len(second) != len(first) {
		t.Fatalf("block count drifted across round-trip: %d → %d\nmd: %q",
			len(first), len(second), md)
	}
	for i := range first {
		if first[i].Type != second[i].Type {
			t.Fatalf("block %d type drifted: %q → %q\nmd: %q",
				i, first[i].Type, second[i].Type, md)
		}
		if first[i].Type == "table" {
			raw1, _ := first[i].Content["text"].(string)
			raw2, _ := second[i].Content["text"].(string)
			if raw1 != raw2 {
				t.Errorf("table raw markdown drifted:\nfirst:  %q\nsecond: %q", raw1, raw2)
			}
		}
	}
	if second[1].Type != "table" {
		t.Errorf("table block degraded to %q after round-trip", second[1].Type)
	}
}

func TestBlocksToMarkdown_TableOnlyPage(t *testing.T) {
	// A page whose only body is a table must project to the raw table
	// markdown (previously fell through to the text default, which
	// happened to match — pin it explicitly now).
	blocks := []*Block{{
		Type:    "table",
		Content: map[string]any{"text": "| a | b |\n| - | - |\n| 1 | 2 |"},
	}}
	got := BlocksToMarkdown(blocks)
	want := "| a | b |\n| - | - |\n| 1 | 2 |"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBlocksToMarkdown_EmptyTableSkipped(t *testing.T) {
	blocks := []*Block{{Type: "table", Content: map[string]any{"text": ""}}}
	if got := BlocksToMarkdown(blocks); got != "" {
		t.Errorf("empty table should project to nothing, got %q", got)
	}
}
