// Sources store tests against real Postgres（page_sources 关联查询需真库）。
// Skips when DATABASE_URL unset — 同 wiki/store 测试惯例。

package sources

import (
	"context"
	"os"
	"testing"

	wikistore "github.com/biumind/biumind/services/brain/internal/wiki/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestAffectedPages(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL unset — skipping integration test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)

	wiki := wikistore.New(pool)
	st := New(pool)
	owner := uuid.New()
	proj, err := wiki.CreateProjectWithTemplate(ctx, owner, "affected-pages-test", "", nil)
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	t.Cleanup(func() {
		// 物理删项目（pages / wiki_sources / page_sources 随 CASCADE 删）。
		if _, err := pool.Exec(context.Background(),
			`DELETE FROM brain.projects WHERE id = $1`, proj.ID); err != nil {
			t.Fatalf("cleanup project: %v", err)
		}
	})

	newPage := func(title string) uuid.UUID {
		p, err := wiki.CreatePage(ctx, wikistore.CreatePageInput{
			ProjectID: proj.ID, Title: title, ActorID: owner.String(),
		})
		if err != nil {
			t.Fatalf("CreatePage: %v", err)
		}
		return p.ID
	}
	newSource := func(rel string) uuid.UUID {
		src, err := st.Upsert(ctx, CreateInput{
			ProjectID: proj.ID, RelPath: rel, Filename: rel,
		})
		if err != nil {
			t.Fatalf("Upsert source: %v", err)
		}
		return src.ID
	}
	link := func(pageID, sourceID uuid.UUID) {
		if err := wiki.LinkPageSource(ctx, pageID, sourceID); err != nil {
			t.Fatalf("LinkPageSource: %v", err)
		}
	}

	page1 := newPage("only-on-src1")
	page2 := newPage("on-src1-and-src2")
	page3 := newPage("unrelated")
	src1 := newSource("a.md")
	src2 := newSource("b.md")

	link(page1, src1)
	link(page2, src1)
	link(page2, src2)

	// src1：page1 仅此一源（only_source=true），page2 还有 src2（false），
	// page3 无关联不出现。
	got, err := st.AffectedPages(ctx, src1)
	if err != nil {
		t.Fatalf("AffectedPages(src1): %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("AffectedPages(src1) len = %d, want 2: %+v", len(got), got)
	}
	byPage := map[uuid.UUID]bool{}
	for _, ap := range got {
		byPage[ap.PageID] = ap.OnlySource
	}
	if only, ok := byPage[page1]; !ok || !only {
		t.Errorf("page1: only_source = %v (present=%v), want true", only, ok)
	}
	if only, ok := byPage[page2]; !ok || only {
		t.Errorf("page2: only_source = %v (present=%v), want false", only, ok)
	}

	// src2：page2 还挂着 src1，不是唯一来源。
	got, err = st.AffectedPages(ctx, src2)
	if err != nil {
		t.Fatalf("AffectedPages(src2): %v", err)
	}
	if len(got) != 1 || got[0].PageID != page2 || got[0].OnlySource {
		t.Errorf("AffectedPages(src2) = %+v, want [{page2 only_source=false}]", got)
	}

	// 无关联源：空结果。
	src3 := newSource("c.md")
	got, err = st.AffectedPages(ctx, src3)
	if err != nil {
		t.Fatalf("AffectedPages(src3): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("AffectedPages(src3) len = %d, want 0", len(got))
	}

	// 软删页不再出现（page1 是唯一只挂 src1 的页，软删后 src1 只剩 page2）。
	if _, err := pool.Exec(ctx,
		`UPDATE brain.pages SET deleted_at = now() WHERE id = $1`, page1); err != nil {
		t.Fatalf("soft delete page1: %v", err)
	}
	got, err = st.AffectedPages(ctx, src1)
	if err != nil {
		t.Fatalf("AffectedPages(src1) after soft delete: %v", err)
	}
	if len(got) != 1 || got[0].PageID != page2 {
		t.Errorf("AffectedPages(src1) after soft delete = %+v, want [page2]", got)
	}
	_ = page3
}
