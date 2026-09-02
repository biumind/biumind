// Apply-fix endpoint tests (P2 #20 ②).
//
// Validation paths that don't touch the DB run as plain unit tests;
// the rewrite happy path + stale-suggestion path are integration tests
// gated on DATABASE_URL (同 autoresolve_test.go 惯例).

package reviews

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	wikistore "github.com/biumind/biumind/services/brain/internal/wiki/store"
	"github.com/google/uuid"
)

func TestHandleApplyFix_RejectsBadID(t *testing.T) {
	srv := NewServer(nil, nil, nil,
		slog.New(slog.NewTextHandler(noopWriter{}, nil)))
	r := httptest.NewRequest(http.MethodPost,
		"/v1/wiki/reviews/not-a-uuid/apply-fix", nil)
	r.SetPathValue("id", "not-a-uuid")
	w := httptest.NewRecorder()
	srv.handleApplyFix(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400 for bad id, got %d (body=%s)", w.Code, w.Body.String())
	}
}

// applyFixRequest builds a handler-bound request with claims stamped,
// bypassing the JWT middleware (same pattern as merger_test.go).
func applyFixRequest(id uuid.UUID, uid uuid.UUID) *http.Request {
	r := httptest.NewRequest(http.MethodPost,
		"/v1/wiki/reviews/"+id.String()+"/apply-fix", nil)
	r.SetPathValue("id", id.String())
	return r.WithContext(bauth.WithClaims(r.Context(), &bauth.Claims{UserID: uid.String()}))
}

func TestHandleApplyFix_RewritesAndResolves(t *testing.T) {
	h := newReviewsTestHarness(t)
	ctx := context.Background()
	owner := uuid.New()
	proj, err := h.wiki.CreateProjectWithTemplate(ctx, owner, "apply-fix", "", nil)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	defer h.cleanupProject(t, proj.ID)

	if _, err := h.wiki.CreatePage(ctx, wikistoreCreatePage(proj.ID, owner, "Getting Started",
		"the real target page body, long enough to matter.")); err != nil {
		t.Fatalf("create target: %v", err)
	}
	page, err := h.wiki.CreatePage(ctx, wikistoreCreatePage(proj.ID, owner, "Main",
		"先读 [[Getting Startd|入门指南]] 再继续。这段正文需要足够长以避开 stub 规则。再提一次 [[Getting Startd]]。"))
	if err != nil {
		t.Fatalf("create page: %v", err)
	}

	if _, err := h.lint.ScanProject(ctx, proj.ID, owner); err != nil {
		t.Fatalf("scan: %v", err)
	}
	var review *Item
	items, err := h.reviews.List(ctx, ListInput{ProjectID: proj.ID, Kind: KindLint, Status: StatusOpen})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, it := range items {
		if it.Payload["rule_id"] == RuleDeadWikilink {
			review = it
		}
	}
	if review == nil {
		t.Fatalf("expected a dead_wikilink review")
	}
	if review.Payload["suggested_target"] != "Getting Started" {
		t.Fatalf("suggested_target=%v, want Getting Started", review.Payload["suggested_target"])
	}

	srv := NewServer(h.reviews, h.wiki, nil,
		slog.New(slog.NewTextHandler(noopWriter{}, nil)))
	w := httptest.NewRecorder()
	srv.handleApplyFix(w, applyFixRequest(review.ID, owner))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (body=%s)", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["replacements"].(float64) != 2 {
		t.Errorf("want 2 replacements, resp=%v", resp)
	}
	if resp["rewritten_to"] != "Getting Started" {
		t.Errorf("rewritten_to=%v", resp["rewritten_to"])
	}

	// body_md 权威列已改写，alias 保留。
	got, err := h.wiki.GetPage(ctx, page.ID)
	if err != nil {
		t.Fatalf("get page: %v", err)
	}
	if !strings.Contains(got.BodyMd, "[[Getting Started|入门指南]]") ||
		!strings.Contains(got.BodyMd, "[[Getting Started]]") {
		t.Errorf("body not rewritten as expected: %q", got.BodyMd)
	}
	if strings.Contains(got.BodyMd, "Getting Startd") {
		t.Errorf("dead target survived: %q", got.BodyMd)
	}
	// blocks 投影同步。
	blocks, err := h.wiki.ListBlocks(ctx, page.ID)
	if err != nil {
		t.Fatalf("list blocks: %v", err)
	}
	joined := ""
	for _, b := range blocks {
		txt, _ := b.Content["text"].(string)
		joined += txt
	}
	if strings.Contains(joined, "Getting Startd") || !strings.Contains(joined, "[[Getting Started|入门指南]]") {
		t.Errorf("blocks projection not updated: %q", joined)
	}
	// review 已 resolved。
	after, err := h.reviews.Get(ctx, review.ID)
	if err != nil {
		t.Fatalf("get review: %v", err)
	}
	if after.Status != StatusResolved {
		t.Errorf("review should be resolved, status=%s", after.Status)
	}
	// 再次 apply → 409（非 open）。
	w2 := httptest.NewRecorder()
	srv.handleApplyFix(w2, applyFixRequest(review.ID, owner))
	if w2.Code != http.StatusConflict {
		t.Errorf("re-apply on resolved review should 409, got %d", w2.Code)
	}
}

func TestHandleApplyFix_StaleSuggestionConflict(t *testing.T) {
	h := newReviewsTestHarness(t)
	ctx := context.Background()
	owner := uuid.New()
	proj, err := h.wiki.CreateProjectWithTemplate(ctx, owner, "apply-fix-stale", "", nil)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	defer h.cleanupProject(t, proj.ID)

	page, err := h.wiki.CreatePage(ctx, wikistoreCreatePage(proj.ID, owner, "Main",
		"见 [[Old Name]]，这段正文需要足够长以避开 stub 规则的干扰。"))
	if err != nil {
		t.Fatalf("create page: %v", err)
	}
	// 手工造一条 suggested_target 已不存在（从未存在/已删）的 review。
	review, _, err := h.reviews.Upsert(ctx, UpsertInput{
		ProjectID: proj.ID, OwnerID: owner,
		Kind: KindLint, Title: "dead", Description: "d",
		PageIDs: []uuid.UUID{page.ID},
		Payload: map[string]any{
			"rule_id":          RuleDeadWikilink,
			"target":           "Old Name",
			"suggested_target": "Deleted Page",
		},
		DedupeKey: LintDedupeKey(page.ID, RuleDeadWikilink, "manual"),
	})
	if err != nil {
		t.Fatalf("seed review: %v", err)
	}

	srv := NewServer(h.reviews, h.wiki, nil,
		slog.New(slog.NewTextHandler(noopWriter{}, nil)))
	w := httptest.NewRecorder()
	srv.handleApplyFix(w, applyFixRequest(review.ID, owner))
	if w.Code != http.StatusConflict {
		t.Fatalf("stale suggestion should 409, got %d (body=%s)", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "suggestion_stale") {
		t.Errorf("want suggestion_stale code, got %s", w.Body.String())
	}
	// 内容未被改动、review 仍 open。
	got, _ := h.wiki.GetPage(ctx, page.ID)
	if !strings.Contains(got.BodyMd, "[[Old Name]]") {
		t.Errorf("body must be untouched on stale suggestion: %q", got.BodyMd)
	}
	after, _ := h.reviews.Get(ctx, review.ID)
	if after.Status != StatusOpen {
		t.Errorf("review must stay open, status=%s", after.Status)
	}
}

func TestHandleApplyFix_RejectsNonFixableKind(t *testing.T) {
	h := newReviewsTestHarness(t)
	ctx := context.Background()
	owner := uuid.New()
	proj, err := h.wiki.CreateProjectWithTemplate(ctx, owner, "apply-fix-nonfixable", "", nil)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	defer h.cleanupProject(t, proj.ID)

	review, _, err := h.reviews.Upsert(ctx, UpsertInput{
		ProjectID: proj.ID, OwnerID: owner,
		Kind: KindDedup, Title: "pair", Description: "d",
		PageIDs:   []uuid.UUID{uuid.New(), uuid.New()},
		DedupeKey: DedupKeyForPair(uuid.New(), uuid.New()),
	})
	if err != nil {
		t.Fatalf("seed review: %v", err)
	}
	srv := NewServer(h.reviews, h.wiki, nil,
		slog.New(slog.NewTextHandler(noopWriter{}, nil)))
	w := httptest.NewRecorder()
	srv.handleApplyFix(w, applyFixRequest(review.ID, owner))
	if w.Code != http.StatusBadRequest {
		t.Errorf("dedup review should be not_fixable (400), got %d (body=%s)",
			w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "not_fixable") {
		t.Errorf("want not_fixable code, got %s", w.Body.String())
	}
}

// wikistoreCreatePage keeps the test bodies short.
func wikistoreCreatePage(projectID, owner uuid.UUID, title, body string) wikistore.CreatePageInput {
	return wikistore.CreatePageInput{
		ProjectID: projectID, Title: title, BodyMd: body, ActorID: owner.String(),
	}
}
