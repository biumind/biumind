package builtin

// wiki write tools 的 run_id 透传测试（§1.2 P2）：ctx 带 tools.WithRunID 时
// 写前快照落 page_revisions.run_id；无 run 上下文 → NULL（人工/MCP 不变）。
// 真 Postgres，DATABASE_URL 未设时 skip（同 wiki_review_test.go 惯例）。

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/biumind/biumind/services/brain/internal/tools"
	wikistore "github.com/biumind/biumind/services/brain/internal/wiki/store"
)

type writeToolHarness struct {
	pool *pgxpool.Pool
	st   *wikistore.Store
}

func newWriteToolHarness(t *testing.T) *writeToolHarness {
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
	return &writeToolHarness{pool: pool, st: wikistore.New(pool)}
}

func (h *writeToolHarness) createProject(t *testing.T, owner uuid.UUID) *wikistore.Project {
	t.Helper()
	p, err := h.st.CreateProjectWithTemplate(context.Background(), owner, "wt-"+uuid.NewString()[:8], "", nil)
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	t.Cleanup(func() {
		if _, err := h.pool.Exec(context.Background(),
			`DELETE FROM brain.projects WHERE id = $1`, p.ID); err != nil {
			t.Fatalf("cleanup project: %v", err)
		}
	})
	return p
}

// lastRunID 取该页最新快照的 run_id（nil = NULL）。
func (h *writeToolHarness) lastRunID(t *testing.T, pageID uuid.UUID) *string {
	t.Helper()
	var runID *string
	if err := h.pool.QueryRow(context.Background(), `
		SELECT run_id FROM brain.page_revisions
		WHERE page_id = $1 ORDER BY created_at DESC LIMIT 1
	`, pageID).Scan(&runID); err != nil {
		t.Fatalf("query run_id: %v", err)
	}
	return runID
}

func TestWikiUpdatePagePropagatesRunID(t *testing.T) {
	h := newWriteToolHarness(t)
	owner := uuid.New()
	proj := h.createProject(t, owner)
	page, err := h.st.CreatePage(context.Background(), wikistore.CreatePageInput{
		ProjectID: proj.ID, Title: "p", ActorID: owner.String(),
	})
	if err != nil {
		t.Fatal(err)
	}
	tool := WikiUpdatePage(h.st)

	// 带 run 上下文 → 快照 run_id = run-42。
	ctx := tools.WithRunID(tools.WithUserID(context.Background(), owner), "run-42")
	args, _ := json.Marshal(map[string]any{
		"page_id": page.ID.String(), "version": page.Version, "body_md": "agent 改后",
	})
	if _, err := tool.Invoke(ctx, args); err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if got := h.lastRunID(t, page.ID); got == nil || *got != "run-42" {
		t.Fatalf("run_id = %v, want run-42", got)
	}

	// 无 run 上下文（人工/MCP 同款 ctx）→ 快照 run_id NULL。
	// 拨回快照时间跳过 5min 窗口合并，保证产生新行。
	if _, err := h.pool.Exec(context.Background(), `
		UPDATE brain.page_revisions SET created_at = now() - interval '6 minutes'
		WHERE page_id = $1
	`, page.ID); err != nil {
		t.Fatal(err)
	}
	cur, err := h.st.GetPage(context.Background(), page.ID)
	if err != nil {
		t.Fatal(err)
	}
	args, _ = json.Marshal(map[string]any{
		"page_id": page.ID.String(), "version": cur.Version, "body_md": "人工改后",
	})
	if _, err := tool.Invoke(tools.WithUserID(context.Background(), owner), args); err != nil {
		t.Fatalf("Invoke (no run): %v", err)
	}
	if got := h.lastRunID(t, page.ID); got != nil {
		t.Fatalf("manual write run_id = %q, want NULL", *got)
	}
}

func TestWikiMergePagesPropagatesRunID(t *testing.T) {
	h := newWriteToolHarness(t)
	owner := uuid.New()
	proj := h.createProject(t, owner)
	mk := func(title string) *wikistore.Page {
		p, err := h.st.CreatePage(context.Background(), wikistore.CreatePageInput{
			ProjectID: proj.ID, Title: title, ActorID: owner.String(),
		})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	canonical, duplicate := mk("canonical"), mk("duplicate")
	tool := WikiMergePages(h.st, nil)

	ctx := tools.WithRunID(tools.WithUserID(context.Background(), owner), "run-99")
	args, _ := json.Marshal(map[string]any{
		"canonical_id": canonical.ID.String(), "duplicate_id": duplicate.ID.String(),
	})
	if _, err := tool.Invoke(ctx, args); err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	// merge 双页快照都落 run_id。
	for _, id := range []uuid.UUID{canonical.ID, duplicate.ID} {
		if got := h.lastRunID(t, id); got == nil || *got != "run-99" {
			t.Fatalf("page %s run_id = %v, want run-99", id, got)
		}
	}
}
