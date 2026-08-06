package rrf

import "testing"

func TestFuseBasic(t *testing.T) {
	bm25 := []Result{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	vec := []Result{{ID: "b"}, {ID: "a"}, {ID: "d"}}
	got := Fuse([][]Result{bm25, vec}, 60, 0)
	if len(got) != 4 {
		t.Fatalf("len=%d want 4", len(got))
	}
	// b at rank 1 in vec + rank 2 in bm25 wins; a at rank 1 in bm25 + rank 2 in vec
	// scores:
	//   a = 1/61 + 1/62
	//   b = 1/62 + 1/61
	// They tie; tie broken by ID alphabetical → a first.
	if got[0].ID != "a" || got[1].ID != "b" {
		t.Errorf("top order = %s,%s", got[0].ID, got[1].ID)
	}
	// d/c at single appearance lower
	last := got[len(got)-1].ID
	if last != "c" && last != "d" {
		t.Errorf("last = %s", last)
	}
}

func TestFuseClampsMax(t *testing.T) {
	out := Fuse([][]Result{
		{{ID: "a"}, {ID: "b"}, {ID: "c"}},
	}, 60, 2)
	if len(out) != 2 {
		t.Errorf("len=%d", len(out))
	}
}

func TestFuseEmpty(t *testing.T) {
	out := Fuse(nil, 60, 0)
	if len(out) != 0 {
		t.Errorf("len=%d", len(out))
	}
}

func TestFusePropagatesMeta(t *testing.T) {
	out := Fuse([][]Result{
		{{ID: "a", Meta: map[string]any{"src": "wiki"}}},
		{{ID: "a", Meta: map[string]any{"src": "vector"}}}, // first wins
	}, 60, 0)
	if out[0].Meta == nil || out[0].Meta["src"] != "wiki" {
		t.Errorf("meta = %+v", out[0].Meta)
	}
}
