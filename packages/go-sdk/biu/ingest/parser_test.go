package ingest

import "testing"

func TestParseMarkdown(t *testing.T) {
	src := Source{Kind: KindMarkdown, Content: `# Quantum Notes

Photons are quantum particles.

## Entanglement

When two particles are entangled,
their states correlate.

`}
	doc, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Title != "Quantum Notes" {
		t.Errorf("title = %q", doc.Title)
	}
	if len(doc.Chunks) != 2 {
		t.Fatalf("chunks = %d (%+v)", len(doc.Chunks), doc.Chunks)
	}
	if doc.Chunks[0].Text != "Photons are quantum particles." {
		t.Errorf("chunk0 = %q", doc.Chunks[0].Text)
	}
	if doc.Chunks[1].Heading != "Entanglement" {
		t.Errorf("chunk1 heading = %q", doc.Chunks[1].Heading)
	}
}

func TestParsePlain(t *testing.T) {
	src := Source{Kind: KindPlainText, Content: "Para one.\n\nPara two with\nmultiple lines.\n\n"}
	doc, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Title != "Para one." {
		t.Errorf("title = %q", doc.Title)
	}
	if len(doc.Chunks) != 2 {
		t.Errorf("chunks = %d", len(doc.Chunks))
	}
}

func TestParseHTML(t *testing.T) {
	src := Source{Kind: KindHTML, Content: `<html><head><title>HTML Doc</title></head><body><script>bad()</script><p>Hello</p><p>World</p></body></html>`}
	doc, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Title != "HTML Doc" {
		t.Errorf("title = %q", doc.Title)
	}
	// Should not contain "bad()" from script tag
	for _, c := range doc.Chunks {
		if c.Text == "bad()" {
			t.Errorf("script content leaked into chunks: %+v", doc.Chunks)
		}
	}
	if len(doc.Chunks) < 2 {
		t.Errorf("expected ≥ 2 chunks; got %d", len(doc.Chunks))
	}
}

func TestParseUnsupported(t *testing.T) {
	if _, err := Parse(Source{Kind: "ppt"}); err == nil {
		t.Fatal("expected error for unknown kind")
	}
}
