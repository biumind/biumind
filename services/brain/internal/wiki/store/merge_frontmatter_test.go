// MergePages frontmatter-union tests. 纯函数单测 + 一个真实 Postgres
// 集成测试（同 merge_body_test.go 惯例，DATABASE_URL 未设时 skip）。

package store

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/google/uuid"
)

func fmJSON(t *testing.T, m map[string]any) []byte {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func unmarshalFM(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal %s: %v", b, err)
	}
	return m
}

// TestMergedFrontmatterForMerge — union 语义：数组并集去重（canonical
// 顺序在前）、duplicate 独有标量补齐、canonical 已有标量不覆盖、
// 类型冲突时 canonical 权威。
func TestMergedFrontmatterForMerge(t *testing.T) {
	cases := []struct {
		name  string
		canon map[string]any
		dup   map[string]any
		want  map[string]any
	}{
		{
			"both empty",
			nil, nil,
			map[string]any{},
		},
		{
			"dup empty keeps canonical verbatim",
			map[string]any{"tags": []any{"a"}, "type": "entity"},
			nil,
			map[string]any{"tags": []any{"a"}, "type": "entity"},
		},
		{
			"canonical empty adopts duplicate",
			nil,
			map[string]any{"tags": []any{"a"}},
			map[string]any{"tags": []any{"a"}},
		},
		{
			"array union dedup keeps canonical order first",
			map[string]any{"tags": []any{"a", "b"}},
			map[string]any{"tags": []any{"b", "c"}},
			map[string]any{"tags": []any{"a", "b", "c"}},
		},
		{
			"scalar not overwritten, dup-only scalar added",
			map[string]any{"type": "entity"},
			map[string]any{"type": "query", "source": "web"},
			map[string]any{"type": "entity", "source": "web"},
		},
		{
			"type mismatch keeps canonical",
			map[string]any{"tags": []any{"a"}},
			map[string]any{"tags": "not-an-array"},
			map[string]any{"tags": []any{"a"}},
		},
		{
			"canonical scalar wins over dup array",
			map[string]any{"related": "x"},
			map[string]any{"related": []any{"y"}},
			map[string]any{"related": "x"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var canonRaw, dupRaw []byte
			if c.canon != nil {
				canonRaw = fmJSON(t, c.canon)
			}
			if c.dup != nil {
				dupRaw = fmJSON(t, c.dup)
			}
			got := unmarshalFM(t, mergedFrontmatterForMerge(canonRaw, dupRaw))
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

// TestMergePages_FrontmatterUnion — 端到端：合并后 canonical 的
// frontmatter 是两页并集；duplicate 的 frontmatter 只多 merged_into/
// merged_at 提示（其原字段保留）。
func TestMergePages_FrontmatterUnion(t *testing.T) {
	h := newWikiTestHarness(t)
	owner := uuid.New()
	proj := h.createProject(t, owner, "merge-fm-union")
	defer h.cleanupProject(t, proj.ID)
	ctx := context.Background()

	canonical, err := h.st.CreatePage(ctx, CreatePageInput{
		ProjectID: proj.ID, Title: "Alpha", ActorID: owner.String(),
		Frontmatter: map[string]any{
			"tags": []any{"go", "db"},
			"type": "entity",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := h.st.CreatePage(ctx, CreatePageInput{
		ProjectID: proj.ID, Title: "Beta", ActorID: owner.String(),
		Frontmatter: map[string]any{
			"tags":   []any{"db", "pg"},
			"type":   "query",
			"source": "webclip",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := h.st.MergePages(ctx, canonical.ID, duplicate.ID, owner.String()); err != nil {
		t.Fatalf("MergePages: %v", err)
	}

	got, err := h.st.GetPage(ctx, canonical.ID)
	if err != nil {
		t.Fatalf("GetPage canonical: %v", err)
	}
	tags, ok := got.Frontmatter["tags"].([]any)
	if !ok {
		t.Fatalf("canonical tags not an array: %v", got.Frontmatter)
	}
	if !reflect.DeepEqual(tags, []any{"go", "db", "pg"}) {
		t.Errorf("tags = %v, want [go db pg]", tags)
	}
	if got.Frontmatter["type"] != "entity" {
		t.Errorf("type = %v, want canonical's entity (不覆盖)", got.Frontmatter["type"])
	}
	if got.Frontmatter["source"] != "webclip" {
		t.Errorf("source = %v, want dup-only scalar补上 webclip", got.Frontmatter["source"])
	}
}
