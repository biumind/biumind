// MergePages body-fold tests against real Postgres (agent-45 遗留修复：
// 合并必须把 duplicate 正文并进 canonical body_md，blocks 投影与
// body_md 不漂移)。Skips when DATABASE_URL unset — 同 store_test.go 惯例。

package store

import (
	"context"
	"strings"
	"testing"

	"github.com/biumind/biumind/services/brain/internal/wiki/mdparse"
	"github.com/google/uuid"
)

// assertBlocksMatchBody — blocks 投影必须与 body_md 经 mdparse 的解析
// 结果逐块一致（type+content 身份键、顺序、数量），这是 "body_md 权威、
// blocks 投影不漂移" 的直接判定。
func assertBlocksMatchBody(t *testing.T, h *wikiTestHarness, pageID uuid.UUID, body string) {
	t.Helper()
	blocks, err := h.st.ListBlocks(context.Background(), pageID)
	if err != nil {
		t.Fatalf("ListBlocks: %v", err)
	}
	want := mdparse.ParseBlocks(body)
	if len(blocks) != len(want) {
		t.Fatalf("blocks/body drift: %d live blocks vs %d parsed from body\nbody=%q",
			len(blocks), len(want), body)
	}
	for i, b := range blocks {
		if blockContentKey(b.Type, b.Content) != blockContentKey(want[i].Type, want[i].Content) {
			t.Errorf("block %d drift: got %v/%v, want %v/%v",
				i, b.Type, b.Content, want[i].Type, want[i].Content)
		}
	}
}

// TestMergePages_FoldsDuplicateBody — 正文不同：合并后 canonical body_md
// 必须同时含两页正文（`\n\n---\n\n` 分隔 + `> 合并自「title」` 标注），
// 且 blocks 投影与合并后 body_md 完全一致。
func TestMergePages_FoldsDuplicateBody(t *testing.T) {
	h := newWikiTestHarness(t)
	owner := uuid.New()
	proj := h.createProject(t, owner, "merge-fold-body")
	defer h.cleanupProject(t, proj.ID)
	ctx := context.Background()

	canonical, err := h.st.CreatePage(ctx, CreatePageInput{
		ProjectID: proj.ID, Title: "Alpha", BodyMd: "canonical 正文", ActorID: owner.String(),
	})
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := h.st.CreatePage(ctx, CreatePageInput{
		ProjectID: proj.ID, Title: "Beta", BodyMd: "duplicate 正文", ActorID: owner.String(),
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := h.st.MergePages(ctx, canonical.ID, duplicate.ID, owner.String(), ""); err != nil {
		t.Fatalf("MergePages: %v", err)
	}

	got, err := h.st.GetPage(ctx, canonical.ID)
	if err != nil {
		t.Fatalf("GetPage canonical: %v", err)
	}
	for _, want := range []string{"canonical 正文", "duplicate 正文", "\n\n---\n\n", "合并自「Beta」"} {
		if !strings.Contains(got.BodyMd, want) {
			t.Errorf("merged body missing %q: %q", want, got.BodyMd)
		}
	}
	// canonical 自己的正文在前，duplicate 在后。
	if strings.Index(got.BodyMd, "canonical 正文") > strings.Index(got.BodyMd, "duplicate 正文") {
		t.Errorf("merge order wrong (canonical must lead): %q", got.BodyMd)
	}
	assertBlocksMatchBody(t, h, canonical.ID, got.BodyMd)

	// duplicate 已 soft-delete。
	if _, err := h.st.GetPage(ctx, duplicate.ID); err != ErrNotFound {
		t.Errorf("duplicate should be soft-deleted, got err=%v", err)
	}
}

// TestMergePages_IdenticalBodiesNoAppend — 两页正文完全相同：不追加、
// 不出现分隔符，blocks 也不因搬运而翻倍（reconcile 收敛同内容块）。
func TestMergePages_IdenticalBodiesNoAppend(t *testing.T) {
	h := newWikiTestHarness(t)
	owner := uuid.New()
	proj := h.createProject(t, owner, "merge-identical-body")
	defer h.cleanupProject(t, proj.ID)
	ctx := context.Background()

	canonical, err := h.st.CreatePage(ctx, CreatePageInput{
		ProjectID: proj.ID, Title: "Alpha", BodyMd: "same body", ActorID: owner.String(),
	})
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := h.st.CreatePage(ctx, CreatePageInput{
		ProjectID: proj.ID, Title: "Beta", BodyMd: "same body", ActorID: owner.String(),
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := h.st.MergePages(ctx, canonical.ID, duplicate.ID, owner.String(), ""); err != nil {
		t.Fatalf("MergePages: %v", err)
	}

	got, err := h.st.GetPage(ctx, canonical.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.BodyMd != "same body" {
		t.Errorf("identical bodies must not append: %q", got.BodyMd)
	}
	assertBlocksMatchBody(t, h, canonical.ID, got.BodyMd)
	if n := len(mdparse.ParseBlocks(got.BodyMd)); n != 1 {
		t.Fatalf("sanity: want 1 parsed block, got %d", n)
	}
}

// TestMergePages_RetryIdempotent — 同一合并操作重试：duplicate 已软删，
// 第二次 MergePages 报错且 canonical 正文 / blocks 零变化。
func TestMergePages_RetryIdempotent(t *testing.T) {
	h := newWikiTestHarness(t)
	owner := uuid.New()
	proj := h.createProject(t, owner, "merge-retry")
	defer h.cleanupProject(t, proj.ID)
	ctx := context.Background()

	canonical, err := h.st.CreatePage(ctx, CreatePageInput{
		ProjectID: proj.ID, Title: "Alpha", BodyMd: "canonical 正文", ActorID: owner.String(),
	})
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := h.st.CreatePage(ctx, CreatePageInput{
		ProjectID: proj.ID, Title: "Beta", BodyMd: "duplicate 正文", ActorID: owner.String(),
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := h.st.MergePages(ctx, canonical.ID, duplicate.ID, owner.String(), ""); err != nil {
		t.Fatalf("first MergePages: %v", err)
	}
	before, err := h.st.GetPage(ctx, canonical.ID)
	if err != nil {
		t.Fatal(err)
	}

	err = h.st.MergePages(ctx, canonical.ID, duplicate.ID, owner.String(), "")
	if err == nil {
		t.Fatal("retry must fail on soft-deleted duplicate")
	}

	after, err := h.st.GetPage(ctx, canonical.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.BodyMd != before.BodyMd {
		t.Errorf("retry changed body:\nbefore=%q\nafter=%q", before.BodyMd, after.BodyMd)
	}
	if after.Version != before.Version {
		t.Errorf("retry bumped version: %d → %d", before.Version, after.Version)
	}
	assertBlocksMatchBody(t, h, canonical.ID, after.BodyMd)
	// 正文只出现一份 duplicate 内容（重试没造成重复追加）。
	if strings.Count(after.BodyMd, "duplicate 正文") != 1 {
		t.Errorf("duplicate content appended more than once: %q", after.BodyMd)
	}
}

// TestMergePages_MergedBodyRewritesSelfLinks — duplicate 正文里的
// `[[Beta]]` 自引在并进 canonical 后会成死链，必须内联改写成
// `[[Alpha]]`（与 119ec8c 对其他页的改写同规则）。
func TestMergePages_MergedBodyRewritesSelfLinks(t *testing.T) {
	h := newWikiTestHarness(t)
	owner := uuid.New()
	proj := h.createProject(t, owner, "merge-self-link")
	defer h.cleanupProject(t, proj.ID)
	ctx := context.Background()

	canonical, err := h.st.CreatePage(ctx, CreatePageInput{
		ProjectID: proj.ID, Title: "Alpha", BodyMd: "canonical 正文", ActorID: owner.String(),
	})
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := h.st.CreatePage(ctx, CreatePageInput{
		ProjectID: proj.ID, Title: "Beta", BodyMd: "参见 [[Beta]] 与 [[Beta2]]", ActorID: owner.String(),
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := h.st.MergePages(ctx, canonical.ID, duplicate.ID, owner.String(), ""); err != nil {
		t.Fatalf("MergePages: %v", err)
	}
	got, err := h.st.GetPage(ctx, canonical.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.BodyMd, "[[Alpha]]") {
		t.Errorf("self-link not rewritten to canonical title: %q", got.BodyMd)
	}
	if strings.Contains(got.BodyMd, "[[Beta]]") {
		t.Errorf("dead self-link survived: %q", got.BodyMd)
	}
	if !strings.Contains(got.BodyMd, "[[Beta2]]") {
		t.Errorf("near-miss target must stay untouched: %q", got.BodyMd)
	}
	assertBlocksMatchBody(t, h, canonical.ID, got.BodyMd)
}
