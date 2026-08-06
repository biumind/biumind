package extract

import (
	"testing"
)

func TestFromText_Hashtags(t *testing.T) {
	got := FromText("Loved the #Rust talk at #conf2024 — also #Rust again")
	if len(got) != 2 {
		t.Fatalf("want 2 unique hashtags, got %d: %+v", len(got), got)
	}
	for _, c := range got {
		if c.Kind != "tag" || c.Relation != "mentions" {
			t.Errorf("bad candidate: %+v", c)
		}
	}
}

func TestFromText_Mentions(t *testing.T) {
	got := FromText("cc @alice and @bob_42 — but not user@example.com")
	names := map[string]bool{}
	for _, c := range got {
		if c.Kind == "person" {
			names[c.Name] = true
		}
	}
	if !names["alice"] || !names["bob_42"] {
		t.Fatalf("want alice + bob_42, got %v", names)
	}
	if names["example"] {
		t.Errorf("email should not have been picked up as @mention")
	}
}

func TestFromText_Wikilinks(t *testing.T) {
	got := FromText("see [[Rust ownership]] and [[Display|Borrow Checker]]")
	want := map[string]bool{
		"Rust ownership": false,
		"Borrow Checker": false,
	}
	for _, c := range got {
		if c.Kind == "concept" {
			if _, ok := want[c.Name]; ok {
				want[c.Name] = true
			}
		}
	}
	for n, ok := range want {
		if !ok {
			t.Errorf("missing concept %q in %+v", n, got)
		}
	}
}

func TestFromText_URLs(t *testing.T) {
	got := FromText("docs at https://example.com/foo/bar?x=1 and https://example.com/foo/bar/")
	var resources int
	for _, c := range got {
		if c.Kind == "resource" {
			resources++
			if c.Name != "example.com/foo/bar" {
				t.Errorf("unexpected canonical name: %s", c.Name)
			}
		}
	}
	if resources != 1 {
		t.Errorf("want 1 unique resource (path/query/trailing-slash dedup), got %d", resources)
	}
}

func TestFromText_Empty(t *testing.T) {
	if got := FromText(""); got != nil {
		t.Fatalf("want nil for empty input, got %+v", got)
	}
}

func TestFromBlockContent_WalksKnownFields(t *testing.T) {
	content := map[string]any{
		"text":    "intro #foo",
		"caption": "see [[Bar]]",
		"items":   []any{"#baz", "@quux"},
	}
	got := FromBlockContent(content)
	kinds := map[string]int{}
	for _, c := range got {
		kinds[c.Kind]++
	}
	if kinds["tag"] != 2 || kinds["person"] != 1 || kinds["concept"] != 1 {
		t.Errorf("bad distribution: %v in %+v", kinds, got)
	}
}

func TestFromText_DedupCaseInsensitiveTags(t *testing.T) {
	got := FromText("#Foo #foo #FOO")
	tags := 0
	for _, c := range got {
		if c.Kind == "tag" {
			tags++
		}
	}
	if tags != 1 {
		t.Errorf("want 1 deduped tag, got %d", tags)
	}
}
