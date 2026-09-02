// Auto-resolve tests (P2 #20 ①).
//
// classifyOpenForResolve / itemsWithDeadPages are pure — unit-tested
// here. The end-to-end worker pass is integration-tested against real
// Postgres, gated on DATABASE_URL (同 wiki/store 测试惯例).

package reviews

import (
	"context"
	"os"
	"testing"

	wikistore "github.com/biumind/biumind/services/brain/internal/wiki/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ── classifyOpenForResolve (pure) ──────────────────────────────

func openItem(kind, key string, pageIDs ...uuid.UUID) *Item {
	return &Item{
		ID:        uuid.New(),
		ProjectID: uuid.New(),
		Kind:      kind,
		Status:    StatusOpen,
		DedupeKey: key,
		PageIDs:   pageIDs,
	}
}

func TestClassifyOpenForResolve_LintKeyGoneIsStale(t *testing.T) {
	pageID := uuid.New()
	items := []*Item{
		openItem(KindLint, LintDedupeKey(pageID, RuleDeadWikilink, "abc"), pageID),
	}
	stale, pairs := classifyOpenForResolve(items, map[string]struct{}{}, nil)
	if len(stale) != 1 || stale[0] != items[0].ID {
		t.Errorf("lint item with vanished key should be stale: %v", stale)
	}
	if len(pairs) != 0 {
		t.Errorf("lint item shouldn't classify as pair")
	}
}

func TestClassifyOpenForResolve_LintKeyPresentStays(t *testing.T) {
	pageID := uuid.New()
	key := LintDedupeKey(pageID, RuleOrphanPage, "")
	items := []*Item{openItem(KindLint, key, pageID)}
	stale, _ := classifyOpenForResolve(items,
		map[string]struct{}{key: {}}, nil)
	if len(stale) != 0 {
		t.Errorf("lint item with live key must stay open")
	}
}

func TestClassifyOpenForResolve_SemanticKeysNeverStale(t *testing.T) {
	// Semantic lint shares kind=lint but its "semantic:" dedupe_keys are
	// never in the structural scan's key set — the prefix guard must
	// keep them open.
	items := []*Item{
		openItem(KindLint, "semantic:abc:stale:deadbeef", uuid.New()),
	}
	stale, _ := classifyOpenForResolve(items, map[string]struct{}{}, nil)
	if len(stale) != 0 {
		t.Errorf("semantic lint rows must never be auto-resolved by the structural pass")
	}
}

func TestClassifyOpenForResolve_SkipsTruncatedPages(t *testing.T) {
	pageID := uuid.New()
	items := []*Item{
		openItem(KindLint, LintDedupeKey(pageID, RuleDeadWikilink, "abc"), pageID),
	}
	stale, _ := classifyOpenForResolve(items,
		map[string]struct{}{},
		map[uuid.UUID]bool{pageID: true})
	if len(stale) != 0 {
		t.Errorf("truncated page's items must not be auto-resolved (unseen tail blocks)")
	}
}

func TestClassifyOpenForResolve_PairsGoToLivenessCheck(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	items := []*Item{
		openItem(KindDedup, DedupKeyForPair(a, b), a, b),
		openItem(KindMerge, DedupKeyForPair(b, a), a, b),
		openItem(KindSweep, "sweep:x:stale_page", a), // untouched by this pass
	}
	stale, pairs := classifyOpenForResolve(items, map[string]struct{}{}, nil)
	if len(stale) != 0 {
		t.Errorf("sweep rows must be ignored: %v", stale)
	}
	if len(pairs) != 2 {
		t.Errorf("dedup + merge should classify as pairs, got %d", len(pairs))
	}
}

// ── itemsWithDeadPages (pure) ──────────────────────────────────

func TestItemsWithDeadPages(t *testing.T) {
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	live := map[uuid.UUID]struct{}{a: {}, b: {}}
	both := openItem(KindDedup, "k1", a, b)
	oneDead := openItem(KindDedup, "k2", a, c)
	noPages := openItem(KindMerge, "k3")
	got := itemsWithDeadPages([]*Item{both, oneDead, noPages}, live)
	if len(got) != 1 || got[0] != oneDead.ID {
		t.Errorf("want only the pair with a dead page, got %v", got)
	}
}

// ── end-to-end worker pass (integration, DATABASE_URL gated) ───

type reviewsTestHarness struct {
	pool    *pgxpool.Pool
	wiki    *wikistore.Store
	reviews *Store
	lint    *LintWorker
}

func newReviewsTestHarness(t *testing.T) *reviewsTestHarness {
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
	rs := New(pool)
	return &reviewsTestHarness{
		pool:    pool,
		wiki:    wikistore.New(pool),
		reviews: rs,
		lint:    NewLintWorker(pool, rs, LintWorkerConfig{}),
	}
}

func (h *reviewsTestHarness) cleanupProject(t *testing.T, pid uuid.UUID) {
	t.Helper()
	if _, err := h.pool.Exec(context.Background(),
		`DELETE FROM brain.projects WHERE id = $1`, pid); err != nil {
		t.Fatalf("cleanup project: %v", err)
	}
}

// TestLintWorker_AutoresolvesFixedDeadLink — scan 1 creates a
// dead_wikilink review; the user fixes the link text; scan 2 must
// auto-resolve the review instead of leaving a ghost in the queue.
func TestLintWorker_AutoresolvesFixedDeadLink(t *testing.T) {
	h := newReviewsTestHarness(t)
	ctx := context.Background()
	owner := uuid.New()
	proj, err := h.wiki.CreateProjectWithTemplate(ctx, owner, "autoresolve", "", nil)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	defer h.cleanupProject(t, proj.ID)

	page, err := h.wiki.CreatePage(ctx, wikistore.CreatePageInput{
		ProjectID: proj.ID, Title: "Main",
		BodyMd:  "见 [[Ghost Page]] 获取更多上下文细节，这段正文需要足够长以避开 stub 规则的干扰。",
		ActorID: owner.String(),
	})
	if err != nil {
		t.Fatalf("create page: %v", err)
	}

	if _, err := h.lint.ScanProject(ctx, proj.ID, owner); err != nil {
		t.Fatalf("scan 1: %v", err)
	}
	key := ""
	items, err := h.reviews.List(ctx, ListInput{ProjectID: proj.ID, Kind: KindLint, Status: StatusOpen})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, it := range items {
		if it.Payload["rule_id"] == RuleDeadWikilink {
			key = it.DedupeKey
		}
	}
	if key == "" {
		t.Fatalf("scan 1 should create a dead_wikilink review, items=%d", len(items))
	}

	// 用户修掉死链（body_md 权威写路径）。
	if _, err := h.wiki.UpdatePageBody(ctx, wikistore.UpdatePageBodyInput{
		PageID:  page.ID,
		BodyMd:  "这段正文保留了足够长度以避开 stub 规则，死链已经移除干净。",
		ActorID: owner.String(),
	}); err != nil {
		t.Fatalf("fix body: %v", err)
	}
	if _, err := h.lint.ScanProject(ctx, proj.ID, owner); err != nil {
		t.Fatalf("scan 2: %v", err)
	}
	id, err := h.reviews.IDByDedupeKey(ctx, key)
	if err != nil || id == uuid.Nil {
		t.Fatalf("review row missing after scan 2: id=%v err=%v", id, err)
	}
	got, err := h.reviews.Get(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != StatusResolved {
		t.Errorf("fixed dead link review should auto-resolve, status=%s", got.Status)
	}
}

// TestLintWorker_AutoresolvesDeadDedupPair — an open dedup review whose
// candidate page got deleted must auto-resolve on the next lint pass.
func TestLintWorker_AutoresolvesDeadDedupPair(t *testing.T) {
	h := newReviewsTestHarness(t)
	ctx := context.Background()
	owner := uuid.New()
	proj, err := h.wiki.CreateProjectWithTemplate(ctx, owner, "autoresolve-dedup", "", nil)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	defer h.cleanupProject(t, proj.ID)

	mk := func(title string) *wikistore.Page {
		p, err := h.wiki.CreatePage(ctx, wikistore.CreatePageInput{
			ProjectID: proj.ID, Title: title,
			BodyMd:  title + " 的正文，长度要超过 stub 阈值才不会被别的规则淹没。",
			ActorID: owner.String(),
		})
		if err != nil {
			t.Fatalf("create page %s: %v", title, err)
		}
		return p
	}
	a := mk("页面甲")
	b := mk("页面乙")

	key := DedupKeyForPair(a.ID, b.ID)
	if _, _, err := h.reviews.Upsert(ctx, UpsertInput{
		ProjectID: proj.ID, OwnerID: owner,
		Kind: KindDedup, Title: "pair", Description: "d",
		PageIDs:   []uuid.UUID{a.ID, b.ID},
		DedupeKey: key,
	}); err != nil {
		t.Fatalf("seed dedup review: %v", err)
	}

	// 候选页之一被直接删除（未走 merge 流）。
	if err := h.wiki.SoftDeletePage(ctx, b.ID, owner.String()); err != nil {
		t.Fatalf("delete page: %v", err)
	}
	if _, err := h.lint.ScanProject(ctx, proj.ID, owner); err != nil {
		t.Fatalf("scan: %v", err)
	}
	id, _ := h.reviews.IDByDedupeKey(ctx, key)
	got, err := h.reviews.Get(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != StatusResolved {
		t.Errorf("dedup review with deleted candidate should auto-resolve, status=%s", got.Status)
	}
}

// TestLintWorker_KeepsLiveConditionsOpen — a review whose condition is
// still present must survive the auto-resolve pass.
func TestLintWorker_KeepsLiveConditionsOpen(t *testing.T) {
	h := newReviewsTestHarness(t)
	ctx := context.Background()
	owner := uuid.New()
	proj, err := h.wiki.CreateProjectWithTemplate(ctx, owner, "autoresolve-keep", "", nil)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	defer h.cleanupProject(t, proj.ID)

	if _, err := h.wiki.CreatePage(ctx, wikistore.CreatePageInput{
		ProjectID: proj.ID, Title: "Main",
		BodyMd:  "见 [[Still Missing]] 获取上下文，这段正文足够长以避免 stub 规则。",
		ActorID: owner.String(),
	}); err != nil {
		t.Fatalf("create page: %v", err)
	}
	if _, err := h.lint.ScanProject(ctx, proj.ID, owner); err != nil {
		t.Fatalf("scan 1: %v", err)
	}
	// 第二次扫描条件仍在（没修）→ 全部保持 open。
	if _, err := h.lint.ScanProject(ctx, proj.ID, owner); err != nil {
		t.Fatalf("scan 2: %v", err)
	}
	items, err := h.reviews.List(ctx, ListInput{ProjectID: proj.ID, Kind: KindLint, Status: StatusOpen})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	foundDead := false
	for _, it := range items {
		if it.Payload["rule_id"] == RuleDeadWikilink {
			foundDead = true
		}
	}
	if !foundDead {
		t.Errorf("live dead_wikilink review must stay open across scans")
	}
}
