package ingest

import (
	"context"
	"strings"
	"testing"

	"github.com/biumind/biumind/packages/go-sdk/biu/llm"
)

// fakeProvider replays a script of (system_must_contain → response) pairs
// in order, advancing on each call.
type fakeProvider struct {
	t       *testing.T
	step    int
	scripts []scriptEntry
}

type scriptEntry struct {
	systemMustContain string
	reply             string
}

func (f *fakeProvider) Name() string { return "fake" }

func (f *fakeProvider) ChatStream(ctx context.Context, req llm.ChatRequest) (<-chan llm.Frame, error) {
	if f.step >= len(f.scripts) {
		f.t.Fatalf("fakeProvider: unexpected extra call (step=%d)", f.step)
	}
	cur := f.scripts[f.step]
	f.step++
	if cur.systemMustContain != "" && !strings.Contains(req.System, cur.systemMustContain) {
		f.t.Fatalf("fakeProvider step %d: system mismatch.\n  got: %s\n  expected substring: %s", f.step-1, req.System, cur.systemMustContain)
	}
	out := make(chan llm.Frame, 4)
	out <- llm.Frame{Kind: llm.KindDelta, Text: cur.reply}
	out <- llm.Frame{Kind: llm.KindEnd}
	close(out)
	return out, nil
}

func TestPipelineHappyPath(t *testing.T) {
	src := Source{
		Kind: KindMarkdown,
		URL:  "https://example.com/notes",
		Content: `# Quantum Notes
Photons are quantum particles.

## Entanglement
Two particles correlate.`,
	}

	outlineJSON := `{"title":"Quantum Notes","summary":"Brief intro to quantum.","tags":["physics","quantum"],"sections":[{"heading":"Overview","goal":"intro"},{"heading":"Entanglement","goal":"correlation"}]}`
	blocksJSON := `[
	  {"type":"heading","content":{"text":"Overview","level":2}},
	  {"type":"text","content":{"text":"Photons are quantum particles."}},
	  {"type":"heading","content":{"text":"Entanglement","level":2}},
	  {"type":"text","content":{"text":"Two particles correlate."}}
	]`

	fp := &fakeProvider{t: t, scripts: []scriptEntry{
		{systemMustContain: "JSON outline", reply: outlineJSON},
		{systemMustContain: "Wiki page", reply: blocksJSON},
	}}
	p := NewPipeline(fp, "")

	draft, err := p.Ingest(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	if draft.Title != "Quantum Notes" {
		t.Errorf("title = %q", draft.Title)
	}
	if len(draft.Blocks) != 4 {
		t.Errorf("blocks = %d", len(draft.Blocks))
	}
	if draft.Blocks[0].Type != "heading" || draft.Blocks[0].Content["text"] != "Overview" {
		t.Errorf("block0 = %+v", draft.Blocks[0])
	}
	if len(draft.Outline.Tags) != 2 {
		t.Errorf("tags = %v", draft.Outline.Tags)
	}
	if draft.Frontmatter["source_url"] != src.URL {
		t.Errorf("frontmatter source_url missing")
	}
}

func TestStripFence(t *testing.T) {
	cases := map[string]string{
		"plain":                   "plain",
		"```json\n{\"a\":1}\n```": `{"a":1}`,
		"```\n[1,2]\n```":         "[1,2]",
		"  ```json\n[]\n``` ":     "[]",
	}
	for in, want := range cases {
		if got := stripFence(in); got != want {
			t.Errorf("stripFence(%q) = %q; want %q", in, got, want)
		}
	}
}

func TestPipelineHandlesFenced(t *testing.T) {
	src := Source{Kind: KindPlainText, Content: "hello"}
	outlineFenced := "```json\n{\"title\":\"X\",\"summary\":\"x\",\"sections\":[{\"heading\":\"A\",\"goal\":\"g\"}]}\n```"
	blocksFenced := "```json\n[{\"type\":\"text\",\"content\":{\"text\":\"hi\"}}]\n```"
	fp := &fakeProvider{t: t, scripts: []scriptEntry{
		{reply: outlineFenced},
		{reply: blocksFenced},
	}}
	p := NewPipeline(fp, "")
	draft, err := p.Ingest(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	if draft.Title != "X" || len(draft.Blocks) != 1 {
		t.Errorf("draft = %+v", draft)
	}
}
