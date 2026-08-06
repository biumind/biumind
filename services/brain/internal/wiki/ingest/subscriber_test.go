package ingest

import (
	"testing"

	"github.com/google/uuid"
)

func TestSeenPaths_NilSafe(t *testing.T) {
	if got := seenPaths(nil); got != nil {
		t.Fatalf("nil progress should yield nil seen, got %v", got)
	}
	if got := seenPaths(map[string]any{}); got != nil {
		t.Fatalf("empty progress should yield nil seen, got %v", got)
	}
}

func TestSeenPaths_RoundTripsStringList(t *testing.T) {
	progress := map[string]any{
		"seen_paths": []any{"wiki/a.md", "wiki/b.md"},
	}
	got := seenPaths(progress)
	if len(got) != 2 || got[0] != "wiki/a.md" || got[1] != "wiki/b.md" {
		t.Fatalf("unexpected seen paths: %v", got)
	}
}

func TestSeenPaths_IgnoresNonStringEntries(t *testing.T) {
	progress := map[string]any{
		"seen_paths": []any{"wiki/a.md", 42, nil, "wiki/b.md"},
	}
	got := seenPaths(progress)
	if len(got) != 2 {
		t.Fatalf("expected 2 string entries, got %v", got)
	}
}

func TestMergeProgress_AppendsAndRecordsLast(t *testing.T) {
	pid := uuid.New()
	prev := map[string]any{
		"seen_paths": []any{"wiki/a.md"},
		"unrelated":  "preserved",
	}
	out := mergeProgress(prev, "wiki/b.md", pid, 2)

	if out["unrelated"] != "preserved" {
		t.Errorf("unrelated keys must pass through")
	}
	if out["last_path"] != "wiki/b.md" {
		t.Errorf("last_path: %v", out["last_path"])
	}
	if out["pages_total"].(int) != 2 {
		t.Errorf("pages_total: %v", out["pages_total"])
	}
	if out["last_page_id"] != pid.String() {
		t.Errorf("last_page_id: %v", out["last_page_id"])
	}
	seen := seenPaths(out)
	if len(seen) != 2 || seen[1] != "wiki/b.md" {
		t.Errorf("seen list not appended: %v", seen)
	}
}


func TestPathBasename(t *testing.T) {
	cases := map[string]string{
		"wiki/concepts/rope.md":      "rope",
		"wiki/index.md":              "index",
		"rope.md":                    "rope",
		"deep/nested/path/foo.bar.md": "foo.bar",
		"":                           "untitled",
		"/":                          "untitled",
	}
	for in, want := range cases {
		if got := pathBasename(in); got != want {
			t.Errorf("pathBasename(%q)=%q want %q", in, got, want)
		}
	}
}
