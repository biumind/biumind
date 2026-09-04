package mdparse

import (
	"reflect"
	"testing"
)

func TestSplitFrontmatter_StandardForm(t *testing.T) {
	md := "---\ntype: concept\ntitle: 普通住宅前期物业\ntags:\n  - 物业\n  - 价格监管\nrelated: [a, b]\n---\n\n# 正文标题\n\n正文。\n"
	fm, body := SplitFrontmatter(md)
	if fm == nil {
		t.Fatal("expected frontmatter map")
	}
	want := map[string]any{
		"type":    "concept",
		"title":   "普通住宅前期物业",
		"tags":    []string{"物业", "价格监管"},
		"related": []string{"a", "b"},
	}
	if !reflect.DeepEqual(fm, want) {
		t.Fatalf("frontmatter = %v, want %v", fm, want)
	}
	if body != "\n# 正文标题\n\n正文。\n" {
		t.Fatalf("body = %q", body)
	}
	// 剥离后的正文投影不再产出 YAML 标题块（事故回归钉）。
	blocks := ParseBlocks(body)
	if len(blocks) == 0 || blocks[0].Type != "heading" || blocks[0].Content["level"] != 1 {
		t.Fatalf("first block of stripped body = %+v", blocks)
	}
}

func TestSplitFrontmatter_ScalarCoercion(t *testing.T) {
	md := "---\ncount: 3\npublished: true\ncreated: 2026-07-31\nempty:\n---\nbody\n"
	fm, body := SplitFrontmatter(md)
	if fm == nil {
		t.Fatal("expected frontmatter map")
	}
	if fm["count"] != "3" || fm["published"] != "true" || fm["empty"] != "" {
		t.Fatalf("scalar coercion = %v", fm)
	}
	if fm["created"] != "2026-07-31" {
		t.Fatalf("date coercion = %v", fm["created"])
	}
	if body != "body\n" {
		t.Fatalf("body = %q", body)
	}
}

func TestSplitFrontmatter_LeakRegression(t *testing.T) {
	// 事故原样输入：标准 frontmatter + 正文。剥离前 goldmark 会把 YAML
	// 投成 setext H2；剥离后第一块必须是正文 H1。
	md := "---\ntype: concept\ntitle: X\ncreated: 2026-07-31\n---\n\n# X\n\n正文第一段。\n"
	fm, body := SplitFrontmatter(md)
	if fm == nil {
		t.Fatal("expected frontmatter map")
	}
	blocks := ParseBlocks(body)
	for _, b := range blocks {
		if txt, _ := b.Content["text"].(string); txt != "" &&
			(txt == "type: concept\ntitle: X\ncreated: 2026-07-31" ||
				len(txt) > 5 && txt[:5] == "type:") {
			t.Fatalf("frontmatter leaked into blocks: %+v", b)
		}
	}
}

func TestSplitFrontmatter_NoFrontmatter(t *testing.T) {
	cases := map[string]string{
		"heading first":   "# 标题\n\n---\n\n正文（分隔线非 frontmatter）。\n",
		"plain":           "正文，没有 frontmatter。",
		"empty":           "",
		"only open fence": "---\ntype: concept\n（没有结尾围栏）\n",
		"not at start":    "\n\n---\ntype: concept\n---\nbody\n",
	}
	for name, md := range cases {
		fm, body := SplitFrontmatter(md)
		if fm != nil {
			t.Errorf("%s: expected nil frontmatter, got %v", name, fm)
		}
		if body != md {
			t.Errorf("%s: body modified: %q", name, body)
		}
	}
}

func TestSplitFrontmatter_NotAMap(t *testing.T) {
	// YAML 负载解析为列表 / 标量 → 非 frontmatter，原文保留。
	for _, md := range []string{
		"---\n- a\n- b\n---\nbody\n",
		"---\njust a scalar\n---\nbody\n",
		"---\n---\nbody\n",
	} {
		fm, body := SplitFrontmatter(md)
		if fm != nil {
			t.Errorf("expected nil frontmatter for %q, got %v", md, fm)
		}
		if body != md {
			t.Errorf("body modified for %q", md)
		}
	}
}

func TestSplitFrontmatter_InvalidYAML(t *testing.T) {
	md := "---\ntype: [unclosed\n---\nbody\n"
	fm, body := SplitFrontmatter(md)
	if fm != nil {
		t.Fatalf("expected nil frontmatter, got %v", fm)
	}
	if body != md {
		t.Fatalf("body modified: %q", body)
	}
}

func TestSplitFrontmatter_CRLF(t *testing.T) {
	md := "---\r\ntype: concept\r\n---\r\n# 正文\n"
	fm, body := SplitFrontmatter(md)
	if fm == nil || fm["type"] != "concept" {
		t.Fatalf("CRLF frontmatter = %v", fm)
	}
	if body != "# 正文\n" {
		t.Fatalf("CRLF body = %q", body)
	}
}

func TestSplitFrontmatter_NestedValueKeptVisible(t *testing.T) {
	md := "---\nmeta:\n  a: 1\n---\nbody\n"
	fm, _ := SplitFrontmatter(md)
	if fm == nil {
		t.Fatal("expected frontmatter map")
	}
	s, ok := fm["meta"].(string)
	if !ok || s == "" {
		t.Fatalf("nested value should be stringified, got %v", fm["meta"])
	}
}
