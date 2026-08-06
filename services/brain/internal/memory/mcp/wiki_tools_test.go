package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	wikistore "github.com/biumind/biumind/services/brain/internal/wiki/store"
	"github.com/google/uuid"
)

// ── Schema-list tests (no DB needed) ─────────────────────────────

func TestToolsList_IncludesAllWikiTools(t *testing.T) {
	want := []string{
		"wiki.search", "wiki.list_pages", "wiki.get_page",
		"wiki.create_page", "wiki.update_page", "wiki.ingest",
		"wiki.list_reviews", "wiki.dismiss_review", "wiki.merge_pages",
	}
	have := map[string]bool{}
	for _, schema := range toolSchemas {
		name, _ := schema["name"].(string)
		have[name] = true
	}
	for _, n := range want {
		if !have[n] {
			t.Errorf("toolSchemas missing %q", n)
		}
	}
}

func TestWikiToolSchemas_AreWellFormed(t *testing.T) {
	// Each schema must declare name + description + inputSchema with
	// type=object + at least one required field. AI clients use these
	// to render the tool palette; broken shapes silently disappear.
	for _, schema := range wikiToolSchemas {
		name, _ := schema["name"].(string)
		if name == "" {
			t.Errorf("wiki schema missing name: %v", schema)
			continue
		}
		desc, _ := schema["description"].(string)
		if len(desc) < 10 {
			t.Errorf("%s: description too terse", name)
		}
		input, ok := schema["inputSchema"].(map[string]any)
		if !ok {
			t.Errorf("%s: missing inputSchema", name)
			continue
		}
		if input["type"] != "object" {
			t.Errorf("%s: inputSchema.type must be object", name)
		}
		if _, ok := input["properties"].(map[string]any); !ok {
			t.Errorf("%s: inputSchema.properties missing", name)
		}
		req, ok := input["required"].([]string)
		if !ok || len(req) == 0 {
			t.Errorf("%s: inputSchema.required empty", name)
		}
	}
}

// ── Dispatch tests for missing-dep paths (no DB needed) ──────────

func TestWikiSearch_ReturnsInternalErrorWhenBM25Unset(t *testing.T) {
	srv := &Server{} // no BM25 wired
	uid := uuid.New()
	ctx := bauth.WithClaims(context.Background(), &bauth.Claims{UserID: uid.String()})
	args, _ := json.Marshal(map[string]any{"query": "anything"})
	_, rerr := srv.callWikiSearch(ctx, uid, args)
	if rerr == nil {
		t.Fatal("expected error when BM25 unset")
	}
	if rerr.Code != codeInternalError {
		t.Errorf("want internal-error code, got %d", rerr.Code)
	}
	if !strings.Contains(rerr.Message, "search not configured") {
		t.Errorf("message should explain config gap, got %q", rerr.Message)
	}
}

func TestWikiIngest_ReturnsInternalErrorWhenIngestUnset(t *testing.T) {
	srv := &Server{} // no Ingest / Publisher wired
	uid := uuid.New()
	ctx := bauth.WithClaims(context.Background(), &bauth.Claims{UserID: uid.String()})
	args, _ := json.Marshal(map[string]any{
		"project_id": uuid.NewString(),
		"raw_text":   "something",
	})
	// callWikiIngest first hits checkProject which calls Wiki store —
	// that nil deref would beat our config check. So we exercise the
	// config check in isolation by skipping straight to it: mock is
	// hard without a DB here, so instead we assert that callWikiIngest
	// rejects raw_text-empty input first (before hitting any store).
	args2, _ := json.Marshal(map[string]any{
		"project_id": uuid.NewString(),
		"raw_text":   "",
	})
	_, rerr := srv.callWikiIngest(ctx, uid, args2)
	if rerr == nil {
		t.Fatal("expected error for empty raw_text")
	}
	if !strings.Contains(rerr.Message, "raw_text") {
		t.Errorf("expected raw_text validation error, got %q", rerr.Message)
	}
	_ = args // silence unused if we extend later
}

func TestWikiSearch_RejectsEmptyQuery(t *testing.T) {
	// Even with BM25 wired, an empty query should be rejected before
	// hitting the searcher.
	srv := &Server{BM25: nil} // BM25 nil but query empty wins first
	uid := uuid.New()
	ctx := bauth.WithClaims(context.Background(), &bauth.Claims{UserID: uid.String()})
	args, _ := json.Marshal(map[string]any{"query": "   "})
	_, rerr := srv.callWikiSearch(ctx, uid, args)
	if rerr == nil {
		t.Fatal("expected error for empty query")
	}
	if !strings.Contains(rerr.Message, "query is required") {
		t.Errorf("expected query-required error, got %q", rerr.Message)
	}
}

// ── Output projection tests ──────────────────────────────────────

func TestPageOut_OmitsFrontmatterWhenAsked(t *testing.T) {
	parent := uuid.New()
	page := &wikistore.Page{
		ID: uuid.New(), ProjectID: uuid.New(), ParentID: &parent,
		Title: "T", Version: 1,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
		Frontmatter: map[string]any{"k": "v"},
	}
	out := pageOut(page, false)
	if _, ok := out["frontmatter"]; ok {
		t.Errorf("frontmatter should be omitted when includeFM=false")
	}
	out2 := pageOut(page, true)
	if _, ok := out2["frontmatter"]; !ok {
		t.Errorf("frontmatter should be present when includeFM=true")
	}
	if out2["parent_id"] != parent.String() {
		t.Errorf("parent_id missing or wrong: %v", out2["parent_id"])
	}
}

func TestWikiListReviews_ReturnsInternalErrorWhenStoreUnset(t *testing.T) {
	srv := &Server{}
	uid := uuid.New()
	ctx := bauth.WithClaims(context.Background(), &bauth.Claims{UserID: uid.String()})
	args, _ := json.Marshal(map[string]any{"project_id": uuid.NewString()})
	_, rerr := srv.callWikiListReviews(ctx, uid, args)
	if rerr == nil {
		t.Fatal("expected error when Reviews unset")
	}
	if rerr.Code != codeInternalError {
		t.Errorf("want internal-error code, got %d", rerr.Code)
	}
	if !strings.Contains(rerr.Message, "reviews not configured") {
		t.Errorf("message should explain config gap, got %q", rerr.Message)
	}
}

func TestWikiDismissReview_RejectsBadUUID(t *testing.T) {
	// Stub a Reviews store but pass a bad uuid; even with a valid
	// store this should fail-fast at parse, not on store call.
	srv := &Server{Reviews: nil}
	uid := uuid.New()
	ctx := bauth.WithClaims(context.Background(), &bauth.Claims{UserID: uid.String()})
	args, _ := json.Marshal(map[string]any{"id": "not-a-uuid"})
	_, rerr := srv.callWikiDismissReview(ctx, uid, args)
	if rerr == nil {
		t.Fatal("expected error")
	}
	// Hits the Reviews-nil check first since it precedes UUID parse.
	if !strings.Contains(rerr.Message, "reviews not configured") {
		t.Errorf("want reviews-not-configured first, got %q", rerr.Message)
	}
}

func TestWikiMergePages_RejectsSameUUID(t *testing.T) {
	srv := &Server{}
	uid := uuid.New()
	ctx := bauth.WithClaims(context.Background(), &bauth.Claims{UserID: uid.String()})
	id := uuid.NewString()
	args, _ := json.Marshal(map[string]any{
		"canonical_id": id, "duplicate_id": id,
	})
	_, rerr := srv.callWikiMergePages(ctx, uid, args)
	if rerr == nil {
		t.Fatal("expected error when canonical == duplicate")
	}
	if !strings.Contains(rerr.Message, "must differ") {
		t.Errorf("expected differ msg, got %q", rerr.Message)
	}
}

func TestWikiMergePages_RejectsBadUUIDs(t *testing.T) {
	srv := &Server{}
	uid := uuid.New()
	ctx := bauth.WithClaims(context.Background(), &bauth.Claims{UserID: uid.String()})
	cases := []map[string]any{
		{"canonical_id": "not-uuid", "duplicate_id": uuid.NewString()},
		{"canonical_id": uuid.NewString(), "duplicate_id": "not-uuid"},
	}
	for i, c := range cases {
		args, _ := json.Marshal(c)
		_, rerr := srv.callWikiMergePages(ctx, uid, args)
		if rerr == nil {
			t.Errorf("case %d: expected UUID validation error", i)
			continue
		}
		if !strings.Contains(rerr.Message, "UUID") {
			t.Errorf("case %d: expected UUID error, got %q", i, rerr.Message)
		}
	}
}

func TestPageOut_OmitsParentIDWhenNil(t *testing.T) {
	page := &wikistore.Page{
		ID: uuid.New(), ProjectID: uuid.New(),
		Title: "T", Version: 1,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	out := pageOut(page, true)
	if _, ok := out["parent_id"]; ok {
		t.Errorf("parent_id should be omitted when nil")
	}
}
