// MergePages backlink-rewrite tests against real Postgres (P2 #20 ③).
// Skips when DATABASE_URL unset — 同 store_test.go / revisions_test.go 惯例。

package store

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// TestMergePages_RewritesBacklinks covers the merge cross-reference
// rewrite: `[[dup]]` / `[[dup|alias]]` in OTHER live pages must fold to
// the canonical title through the body_md authoritative path (blocks
// re-projected, revision snapshotted), while near-miss targets like
// `[[Beta2]]` stay untouched.
func TestMergePages_RewritesBacklinks(t *testing.T) {
	h := newWikiTestHarness(t)
	owner := uuid.New()
	proj := h.createProject(t, owner, "merge-rewrite")
	defer h.cleanupProject(t, proj.ID)
	ctx := context.Background()

	canonical, err := h.st.CreatePage(ctx, CreatePageInput{
		ProjectID: proj.ID, Title: "Alpha", BodyMd: "canonical body", ActorID: owner.String(),
	})
	if err != nil {
		t.Fatalf("create canonical: %v", err)
	}
	duplicate, err := h.st.CreatePage(ctx, CreatePageInput{
		ProjectID: proj.ID, Title: "Beta", BodyMd: "duplicate body", ActorID: owner.String(),
	})
	if err != nil {
		t.Fatalf("create duplicate: %v", err)
	}
	linker, err := h.st.CreatePage(ctx, CreatePageInput{
		ProjectID: proj.ID, Title: "Gamma",
		BodyMd:  "see [[Beta]] and [[Beta|B 别名]] and [[Beta2]] and [[beta]]",
		ActorID: owner.String(),
	})
	if err != nil {
		t.Fatalf("create linker: %v", err)
	}

	if err := h.st.MergePages(ctx, canonical.ID, duplicate.ID, owner.String()); err != nil {
		t.Fatalf("MergePages: %v", err)
	}

	got, err := h.st.GetPage(ctx, linker.ID)
	if err != nil {
		t.Fatalf("GetPage linker: %v", err)
	}
	body := got.BodyMd
	for _, want := range []string{"[[Alpha]]", "[[Alpha|B 别名]]", "[[Beta2]]"} {
		if !strings.Contains(body, want) {
			t.Errorf("rewritten body missing %q: %q", want, body)
		}
	}
	// 大小写形态 [[beta]] 也应改写（wikilink 解析大小写不敏感）。
	if strings.Contains(body, "[[beta]]") {
		t.Errorf("lowercase [[beta]] not rewritten: %q", body)
	}
	for _, stale := range []string{"[[Beta]]", "[[Beta|"} {
		if strings.Contains(body, stale) {
			t.Errorf("stale link %q survived: %q", stale, body)
		}
	}

	// blocks 投影必须同步（body_md 权威 → reconcile）。
	blocks, err := h.st.ListBlocks(ctx, linker.ID)
	if err != nil {
		t.Fatalf("ListBlocks: %v", err)
	}
	foundRewritten := false
	for _, b := range blocks {
		txt, _ := b.Content["text"].(string)
		if strings.Contains(txt, "[[Beta]]") {
			t.Errorf("blocks projection still holds stale link: %q", txt)
		}
		if strings.Contains(txt, "[[Alpha]]") {
			foundRewritten = true
		}
	}
	if !foundRewritten {
		t.Errorf("blocks projection missing rewritten link")
	}

	// 写前快照：linker 应有一条 edit 版本，body_md 是改写前旧文。
	revs, err := h.st.ListPageRevisions(ctx, linker.ID, 10, 0)
	if err != nil {
		t.Fatalf("ListPageRevisions: %v", err)
	}
	if len(revs) == 0 {
		t.Fatalf("expected pre-rewrite revision snapshot, got none")
	}
	full, err := h.st.GetPageRevision(ctx, linker.ID, revs[0].ID)
	if err != nil {
		t.Fatalf("GetPageRevision: %v", err)
	}
	if !strings.Contains(full.BodyMd, "[[Beta]]") {
		t.Errorf("snapshot should hold pre-rewrite body, got %q", full.BodyMd)
	}
}

// TestMergePages_NoBacklinksNoRewrite — merge without any backlinks
// must not touch other pages (no version bump, no revision snapshot).
func TestMergePages_NoBacklinksNoRewrite(t *testing.T) {
	h := newWikiTestHarness(t)
	owner := uuid.New()
	proj := h.createProject(t, owner, "merge-no-rewrite")
	defer h.cleanupProject(t, proj.ID)
	ctx := context.Background()

	canonical, err := h.st.CreatePage(ctx, CreatePageInput{
		ProjectID: proj.ID, Title: "Alpha", BodyMd: "a", ActorID: owner.String(),
	})
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := h.st.CreatePage(ctx, CreatePageInput{
		ProjectID: proj.ID, Title: "Beta", BodyMd: "b", ActorID: owner.String(),
	})
	if err != nil {
		t.Fatal(err)
	}
	other, err := h.st.CreatePage(ctx, CreatePageInput{
		ProjectID: proj.ID, Title: "Gamma", BodyMd: "no links here", ActorID: owner.String(),
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := h.st.MergePages(ctx, canonical.ID, duplicate.ID, owner.String()); err != nil {
		t.Fatalf("MergePages: %v", err)
	}
	got, err := h.st.GetPage(ctx, other.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != 1 {
		t.Errorf("unrelated page version bumped: %d", got.Version)
	}
	if got.BodyMd != "no links here" {
		t.Errorf("unrelated page body changed: %q", got.BodyMd)
	}
}
