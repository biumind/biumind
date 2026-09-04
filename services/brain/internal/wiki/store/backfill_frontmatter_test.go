// BackfillFrontmatter 集成测试 —— 2026-09-04 串味事故回填。
// 真库测试，DATABASE_URL 未设时跳过（同 store_test.go 惯例）。

package store

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestBackfillFrontmatter_StripsAndReprojects(t *testing.T) {
	h := newWikiTestHarness(t)
	ctx := context.Background()
	owner := uuid.New()
	proj := h.createProject(t, owner, "fm-backfill")
	t.Cleanup(func() { h.cleanupProject(t, proj.ID) })

	// 模拟历史脏写入：frontmatter 混在 body_md 里（旧 ingest 路径）。
	dirty := "---\ntype: concept\ntitle: 测试页\ntags:\n  - a\n  - b\n---\n\n# 测试页\n\n正文第一段。\n"
	page, err := h.st.CreatePage(ctx, CreatePageInput{
		ProjectID: proj.ID, Title: "测试页", BodyMd: dirty, ActorID: owner.String(),
	})
	if err != nil {
		t.Fatalf("CreatePage: %v", err)
	}

	n, err := h.st.BackfillFrontmatter(ctx)
	if err != nil {
		t.Fatalf("BackfillFrontmatter: %v", err)
	}
	if n != 1 {
		t.Fatalf("backfilled %d pages, want 1", n)
	}

	got, err := h.st.GetPage(ctx, page.ID)
	if err != nil {
		t.Fatalf("GetPage: %v", err)
	}
	if got.Frontmatter["type"] != "concept" || got.Frontmatter["title"] != "测试页" {
		t.Fatalf("frontmatter = %v", got.Frontmatter)
	}
	if tags, ok := got.Frontmatter["tags"].([]any); !ok || len(tags) != 2 {
		t.Fatalf("tags = %v (%T)", got.Frontmatter["tags"], got.Frontmatter["tags"])
	}
	if strings.HasPrefix(got.BodyMd, "---") {
		t.Fatalf("body_md still starts with fence: %q", got.BodyMd[:40])
	}
	if !strings.Contains(got.BodyMd, "# 测试页") {
		t.Fatalf("body lost content: %q", got.BodyMd[:80])
	}

	// blocks 重投影：不再有装 YAML 的 heading 块，第一块是正文 H1。
	blocks, err := h.st.ListBlocks(ctx, page.ID)
	if err != nil {
		t.Fatalf("ListBlocks: %v", err)
	}
	for _, b := range blocks {
		if txt, _ := b.Content["text"].(string); strings.HasPrefix(txt, "type:") {
			t.Fatalf("frontmatter still in blocks: %+v", b.Content)
		}
	}
	if len(blocks) == 0 || blocks[0].Type != "heading" {
		t.Fatalf("blocks = %+v", blocks)
	}

	// 幂等：再跑返回 0，frontmatter/body 不变。
	n2, err := h.st.BackfillFrontmatter(ctx)
	if err != nil {
		t.Fatalf("second BackfillFrontmatter: %v", err)
	}
	if n2 != 0 {
		t.Fatalf("second run backfilled %d, want 0", n2)
	}
}

func TestBackfillFrontmatter_KeepsExistingKeys(t *testing.T) {
	h := newWikiTestHarness(t)
	ctx := context.Background()
	owner := uuid.New()
	proj := h.createProject(t, owner, "fm-backfill-merge")
	t.Cleanup(func() { h.cleanupProject(t, proj.ID) })

	// 已有 jsonb 键（如模板/research 写入）必须优先于剥离值。
	dirty := "---\ntype: concept\norigin: wiki-llm\n---\n正文。\n"
	page, err := h.st.CreatePage(ctx, CreatePageInput{
		ProjectID: proj.ID, Title: "合并页", BodyMd: dirty,
		Frontmatter: map[string]any{"origin": "deep-research"},
		ActorID:     owner.String(),
	})
	if err != nil {
		t.Fatalf("CreatePage: %v", err)
	}
	if _, err := h.st.BackfillFrontmatter(ctx); err != nil {
		t.Fatalf("BackfillFrontmatter: %v", err)
	}
	got, err := h.st.GetPage(ctx, page.ID)
	if err != nil {
		t.Fatalf("GetPage: %v", err)
	}
	if got.Frontmatter["origin"] != "deep-research" {
		t.Fatalf("existing key overwritten: %v", got.Frontmatter)
	}
	if got.Frontmatter["type"] != "concept" {
		t.Fatalf("extracted key missing: %v", got.Frontmatter)
	}
}

func TestBackfillFrontmatter_InvalidYAMLUntouched(t *testing.T) {
	h := newWikiTestHarness(t)
	ctx := context.Background()
	owner := uuid.New()
	proj := h.createProject(t, owner, "fm-backfill-invalid")
	t.Cleanup(func() { h.cleanupProject(t, proj.ID) })

	// 以 --- 开头但不是合法 frontmatter（YAML 不成立 / 非 map）→ 不动。
	invalid := "---\ntype: [unclosed\n---\n这不是 frontmatter。\n"
	page, err := h.st.CreatePage(ctx, CreatePageInput{
		ProjectID: proj.ID, Title: "坏页", BodyMd: invalid, ActorID: owner.String(),
	})
	if err != nil {
		t.Fatalf("CreatePage: %v", err)
	}
	if _, err := h.st.BackfillFrontmatter(ctx); err != nil {
		t.Fatalf("BackfillFrontmatter: %v", err)
	}
	got, err := h.st.GetPage(ctx, page.ID)
	if err != nil {
		t.Fatalf("GetPage: %v", err)
	}
	if got.BodyMd != invalid {
		t.Fatalf("invalid body modified: %q", got.BodyMd[:60])
	}
	fmJSON, _ := json.Marshal(got.Frontmatter)
	if len(got.Frontmatter) != 0 {
		t.Fatalf("frontmatter should stay empty, got %s", fmJSON)
	}
}
