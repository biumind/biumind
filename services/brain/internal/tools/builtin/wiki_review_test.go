package builtin

// wiki_create_review tests. Descriptor / arg-validation cases run without a
// DB (nil stores — validation happens before any store call); write /
// owner-scope / idempotency cases are integration tests against real
// Postgres, skipped when DATABASE_URL is unset（同 internal/wiki/store
// store_test.go 惯例）。

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/biumind/biumind/services/brain/internal/tools"
	wikireviews "github.com/biumind/biumind/services/brain/internal/wiki/reviews"
	wikistore "github.com/biumind/biumind/services/brain/internal/wiki/store"
)

func TestWikiCreateReviewDescriptor(t *testing.T) {
	tool := WikiCreateReview(nil, nil)
	if tool.Name != "wiki_create_review" {
		t.Errorf("name: %q", tool.Name)
	}
	if tool.ReadOnly {
		t.Error("wiki_create_review writes review_items — ReadOnly must be false")
	}
	if tool.Invoke == nil {
		t.Fatal("Invoke nil")
	}
	if !tool.Runtime.AvailableIn(tools.ExecutionCloud) {
		t.Error("expected cloud runtime")
	}
}

func TestWikiCreateReviewRequiresUserContext(t *testing.T) {
	tool := WikiCreateReview(nil, nil)
	_, err := tool.Invoke(context.Background(), json.RawMessage(
		`{"project_id":"`+uuid.NewString()+`","kind":"suggestion","title":"t"}`))
	if err == nil || !strings.Contains(err.Error(), "user identity") {
		t.Errorf("expected missing-user error, got %v", err)
	}
}

func TestWikiCreateReviewInvalidKindRejected(t *testing.T) {
	tool := WikiCreateReview(nil, nil)
	ctx := tools.WithUserID(context.Background(), uuid.New())
	_, err := tool.Invoke(ctx, json.RawMessage(
		`{"project_id":"`+uuid.NewString()+`","kind":"nonsense","title":"t"}`))
	if err == nil || !strings.Contains(err.Error(), "invalid kind") {
		t.Errorf("expected invalid-kind error, got %v", err)
	}
}

func TestWikiCreateReviewEmptyTitleRejected(t *testing.T) {
	tool := WikiCreateReview(nil, nil)
	ctx := tools.WithUserID(context.Background(), uuid.New())
	_, err := tool.Invoke(ctx, json.RawMessage(
		`{"project_id":"`+uuid.NewString()+`","kind":"suggestion","title":"  "}`))
	if err == nil || !strings.Contains(err.Error(), "title is required") {
		t.Errorf("expected title-required error, got %v", err)
	}
}

// ─── integration (DATABASE_URL gated) ─────────────────────────────

type reviewToolHarness struct {
	pool *pgxpool.Pool
	st   *wikistore.Store
	rv   *wikireviews.Store
	tool tools.Tool
}

func newReviewToolHarness(t *testing.T) *reviewToolHarness {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL unset — skipping integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	st := wikistore.New(pool)
	rv := wikireviews.New(pool)
	return &reviewToolHarness{pool: pool, st: st, rv: rv, tool: WikiCreateReview(st, rv)}
}

func (h *reviewToolHarness) createProject(t *testing.T, owner uuid.UUID, name string) *wikistore.Project {
	t.Helper()
	p, err := h.st.CreateProjectWithTemplate(context.Background(), owner, name, "", nil)
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	t.Cleanup(func() {
		// review_items / pages 随 project CASCADE 删。
		if _, err := h.pool.Exec(context.Background(),
			`DELETE FROM brain.projects WHERE id = $1`, p.ID); err != nil {
			t.Fatalf("cleanup project: %v", err)
		}
	})
	return p
}

func (h *reviewToolHarness) createPage(t *testing.T, pid, owner uuid.UUID, title string) *wikistore.Page {
	t.Helper()
	p, err := h.st.CreatePage(context.Background(), wikistore.CreatePageInput{
		ProjectID: pid, Title: title, ActorID: owner.String(),
	})
	if err != nil {
		t.Fatalf("CreatePage: %v", err)
	}
	return p
}

func (h *reviewToolHarness) invoke(t *testing.T, uid uuid.UUID, args string) map[string]any {
	t.Helper()
	ctx := tools.WithUserID(context.Background(), uid)
	out, err := h.tool.Invoke(ctx, json.RawMessage(args))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("unexpected output type %T", out)
	}
	return m
}

func TestWikiCreateReviewWritesRow(t *testing.T) {
	h := newReviewToolHarness(t)
	owner := uuid.New()
	proj := h.createProject(t, owner, "review-tool-write")
	p1 := h.createPage(t, proj.ID, owner, "Page A")
	p2 := h.createPage(t, proj.ID, owner, "Page B")

	out := h.invoke(t, owner, `{
		"project_id": "`+proj.ID.String()+`",
		"kind": "contradiction",
		"title": "A 与 B 对发布日期的表述矛盾",
		"summary": "Page A 说 3 月发布，Page B 说 5 月发布。",
		"page_ids": ["`+p1.ID.String()+`", "`+p2.ID.String()+`"]
	}`)
	if out["created"] != true {
		t.Errorf("first write must create: %v", out)
	}
	id, err := uuid.Parse(out["id"].(string))
	if err != nil {
		t.Fatalf("bad id: %v", err)
	}
	item, err := h.rv.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if item.Kind != wikireviews.KindContradiction || item.Status != wikireviews.StatusOpen {
		t.Errorf("row: kind=%s status=%s", item.Kind, item.Status)
	}
	if item.OwnerID != owner || item.ProjectID != proj.ID {
		t.Errorf("row scope: owner=%s project=%s", item.OwnerID, item.ProjectID)
	}
	if len(item.PageIDs) != 2 {
		t.Errorf("page_ids: %v", item.PageIDs)
	}
	if item.Description != "Page A 说 3 月发布，Page B 说 5 月发布。" {
		t.Errorf("description: %q", item.Description)
	}
}

func TestWikiCreateReviewSuggestionWithoutPages(t *testing.T) {
	h := newReviewToolHarness(t)
	owner := uuid.New()
	proj := h.createProject(t, owner, "review-tool-suggestion")

	out := h.invoke(t, owner, `{
		"project_id": "`+proj.ID.String()+`",
		"kind": "suggestion",
		"title": "建议新增「部署架构」页",
		"summary": "多页引用部署架构但项目里没有对应页面。"
	}`)
	if out["created"] != true || out["kind"] != wikireviews.KindSuggestion {
		t.Errorf("suggestion write: %v", out)
	}
}

func TestWikiCreateReviewIdempotent(t *testing.T) {
	h := newReviewToolHarness(t)
	owner := uuid.New()
	proj := h.createProject(t, owner, "review-tool-idem")
	p1 := h.createPage(t, proj.ID, owner, "Page A")

	args := `{
		"project_id": "` + proj.ID.String() + `",
		"kind": "dedup",
		"title": "疑似重复页",
		"page_ids": ["` + p1.ID.String() + `"]
	}`
	first := h.invoke(t, owner, args)
	second := h.invoke(t, owner, args)
	if first["created"] != true {
		t.Errorf("first write must create: %v", first)
	}
	if second["created"] != false {
		t.Errorf("second write of same finding must not create: %v", second)
	}
	if first["id"] != second["id"] || first["dedupe_key"] != second["dedupe_key"] {
		t.Errorf("repeat write must return the same row: %v vs %v", first, second)
	}
	n, err := h.rv.CountOpen(context.Background(), proj.ID)
	if err != nil {
		t.Fatalf("CountOpen: %v", err)
	}
	if n != 1 {
		t.Errorf("expected exactly 1 open review, got %d", n)
	}
}

func TestWikiCreateReviewCrossOwnerRejected(t *testing.T) {
	h := newReviewToolHarness(t)
	ownerA := uuid.New()
	ownerB := uuid.New()
	projA := h.createProject(t, ownerA, "review-tool-a")
	projB := h.createProject(t, ownerB, "review-tool-b")
	pageB := h.createPage(t, projB.ID, ownerB, "B's page")
	pageA := h.createPage(t, projA.ID, ownerA, "A's page")

	ctx := tools.WithUserID(context.Background(), ownerA)

	// ownerA 往 ownerB 的项目写 review → project not found
	_, err := h.tool.Invoke(ctx, json.RawMessage(`{
		"project_id": "`+projB.ID.String()+`",
		"kind": "suggestion", "title": "x"
	}`))
	if err == nil || !strings.Contains(err.Error(), "project not found") {
		t.Errorf("foreign project must be rejected, got %v", err)
	}

	// ownerA 在自己项目下引用 ownerB 的页面 → page not found
	_, err = h.tool.Invoke(ctx, json.RawMessage(`{
		"project_id": "`+projA.ID.String()+`",
		"kind": "contradiction", "title": "x",
		"page_ids": ["`+pageB.ID.String()+`"]
	}`))
	if err == nil || !strings.Contains(err.Error(), "page not found") {
		t.Errorf("foreign page must be rejected, got %v", err)
	}

	// ownerA 在 projA 下引用同 owner 另一项目的页面 → 跨项目拒绝
	projA2 := h.createProject(t, ownerA, "review-tool-a2")
	pageA2 := h.createPage(t, projA2.ID, ownerA, "A2's page")
	_, err = h.tool.Invoke(ctx, json.RawMessage(`{
		"project_id": "`+projA.ID.String()+`",
		"kind": "contradiction", "title": "x",
		"page_ids": ["`+pageA2.ID.String()+`"]
	}`))
	if err == nil || !strings.Contains(err.Error(), "must belong to project_id") {
		t.Errorf("cross-project page must be rejected, got %v", err)
	}

	// 合法调用（同 owner 同项目）确认放行。
	out := h.invoke(t, ownerA, `{
		"project_id": "`+projA.ID.String()+`",
		"kind": "contradiction", "title": "合法调用",
		"page_ids": ["`+pageA.ID.String()+`"]
	}`)
	if out["created"] != true {
		t.Errorf("same-owner same-project write must succeed: %v", out)
	}
}
