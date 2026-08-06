// Wiki store tests against real Postgres（pgvector / zhparser / jsonb 需真库）。
// Skips when DATABASE_URL unset — 同 note/files 测试惯例。

package store

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type wikiTestHarness struct {
	pool *pgxpool.Pool
	st   *Store
}

func newWikiTestHarness(t *testing.T) *wikiTestHarness {
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
	return &wikiTestHarness{pool: pool, st: New(pool)}
}

// cleanupProject — 物理删项目（pages/blocks/events/page_revisions 随 CASCADE 删）。
func (h *wikiTestHarness) cleanupProject(t *testing.T, pid uuid.UUID) {
	t.Helper()
	if _, err := h.pool.Exec(context.Background(),
		`DELETE FROM brain.projects WHERE id = $1`, pid); err != nil {
		t.Fatalf("cleanup project: %v", err)
	}
}

func (h *wikiTestHarness) createProject(t *testing.T, owner uuid.UUID, name string) *Project {
	t.Helper()
	p, err := h.st.CreateProjectWithTemplate(context.Background(), owner, name, "", nil)
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	return p
}

func (h *wikiTestHarness) createPage(t *testing.T, pid, owner uuid.UUID, title string) *Page {
	t.Helper()
	p, err := h.st.CreatePage(context.Background(), CreatePageInput{
		ProjectID: pid, Title: title, ActorID: owner.String(),
	})
	if err != nil {
		t.Fatalf("CreatePage: %v", err)
	}
	return p
}

func (h *wikiTestHarness) createBlock(t *testing.T, pid, pageID, owner uuid.UUID, btype, text string, pos float64) *Block {
	t.Helper()
	b, err := h.st.CreateBlock(context.Background(), CreateBlockInput{
		PageID: pageID, ProjectID: pid, Position: pos, Type: btype,
		Content: map[string]any{"text": text}, ActorID: owner.String(),
	})
	if err != nil {
		t.Fatalf("CreateBlock: %v", err)
	}
	return b
}
