// Tests for the ingest-context internal endpoint (P2 #17 two-stage
// pipeline). Auth/validation paths run without a DB (nil Wiki store);
// the happy path + truncation run against real Postgres and skip when
// DATABASE_URL is unset (同 store_test.go 惯例).

package ingest

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	wikistore "github.com/biumind/biumind/services/brain/internal/wiki/store"
	"github.com/biumind/biumind/services/brain/internal/wiki/templates"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func mountContextServer(t *testing.T, wiki *wikistore.Store, token string) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	s := NewInternalServer(nil, token,
		slog.New(slog.NewTextHandler(nopWriter{}, nil)))
	s.Wiki = wiki
	s.Mount(mux)
	return mux
}

func contextRequest(t *testing.T, h http.Handler, url, token string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, url, nil)
	if token != "" {
		r.Header.Set("X-Biumind-Internal-Token", token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestIngestContext_RejectsMissingToken(t *testing.T) {
	h := mountContextServer(t, nil, "secret-token")
	w := contextRequest(t, h,
		"/v1/internal/wiki/projects/00000000-0000-0000-0000-000000000000/ingest-context?owner_id=00000000-0000-0000-0000-000000000000",
		"")
	if w.Code != http.StatusUnauthorized {
		t.Errorf("want 401 without token, got %d", w.Code)
	}
}

func TestIngestContext_RejectsBadProjectID(t *testing.T) {
	h := mountContextServer(t, nil, "secret-token")
	w := contextRequest(t, h,
		"/v1/internal/wiki/projects/not-a-uuid/ingest-context?owner_id=00000000-0000-0000-0000-000000000000",
		"secret-token")
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400 for bad project UUID, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "bad_id") {
		t.Errorf("expected bad_id in body, got %s", w.Body.String())
	}
}

func TestIngestContext_RejectsMissingOwnerID(t *testing.T) {
	h := mountContextServer(t, nil, "secret-token")
	w := contextRequest(t, h,
		"/v1/internal/wiki/projects/00000000-0000-0000-0000-000000000000/ingest-context",
		"secret-token")
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400 without owner_id, got %d", w.Code)
	}
}

func TestIngestContext_WikiStoreMissingIs503(t *testing.T) {
	// Wiki nil（未装配）→ 503，worker 据此降级为空上下文而非误判 404。
	h := mountContextServer(t, nil, "secret-token")
	w := contextRequest(t, h,
		"/v1/internal/wiki/projects/00000000-0000-0000-0000-000000000000/ingest-context?owner_id=00000000-0000-0000-0000-000000000000",
		"secret-token")
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("want 503 when wiki store not wired, got %d", w.Code)
	}
}

func TestTruncateRunes(t *testing.T) {
	s, trunc := truncateRunes("短", 10)
	if trunc || s != "短" {
		t.Errorf("short string: got (%q, %v)", s, trunc)
	}
	long := strings.Repeat("汉", 5000)
	s, trunc = truncateRunes(long, ingestContextBodyRunes)
	if !trunc {
		t.Error("long CJK string should report truncation")
	}
	if got := len([]rune(s)); got != ingestContextBodyRunes {
		t.Errorf("truncated to %d runes, want %d", got, ingestContextBodyRunes)
	}
}

// ─── integration (real Postgres) ─────────────────────────────────

type contextTestHarness struct {
	pool  *pgxpool.Pool
	wiki  *wikistore.Store
	owner uuid.UUID
}

func newContextTestHarness(t *testing.T) *contextTestHarness {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL unset — skipping integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	h := &contextTestHarness{pool: pool, wiki: wikistore.New(pool), owner: uuid.New()}
	t.Cleanup(func() { pool.Close() })
	return h
}

func (h *contextTestHarness) dropProject(t *testing.T, pid uuid.UUID) {
	t.Helper()
	if _, err := h.pool.Exec(context.Background(),
		`DELETE FROM brain.projects WHERE id = $1`, pid); err != nil {
		t.Logf("cleanup project: %v", err)
	}
}

func TestIngestContext_SeededProjectReturnsPurposeSchemaOverviewAndIndex(t *testing.T) {
	h := newContextTestHarness(t)
	tmpl := templates.Lookup("research")
	if tmpl == nil {
		t.Fatal("research template missing")
	}
	proj, err := h.wiki.CreateProjectWithTemplate(context.Background(),
		h.owner, "ctx-test", tmpl.ID, tmpl.SeedPages)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	defer h.dropProject(t, proj.ID)

	srv := mountContextServer(t, h.wiki, "secret-token")
	url := "/v1/internal/wiki/projects/" + proj.ID.String() +
		"/ingest-context?owner_id=" + h.owner.String()
	w := contextRequest(t, srv, url, "secret-token")
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (body=%s)", w.Code, w.Body.String())
	}
	var body struct {
		Purpose          string `json:"purpose"`
		PurposeTruncated bool   `json:"purpose_truncated"`
		Schema           string `json:"schema"`
		SchemaTruncated  bool   `json:"schema_truncated"`
		Overview         string `json:"overview"`
		Pages            []struct {
			Title string `json:"title"`
			Type  string `json:"type"`
		} `json:"pages"`
		PagesTotal int `json:"pages_total"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Purpose == "" || body.Schema == "" || body.Overview == "" {
		t.Errorf("seeded project must return purpose+schema+overview bodies, got purpose=%dB schema=%dB overview=%dB",
			len(body.Purpose), len(body.Schema), len(body.Overview))
	}
	if body.PurposeTruncated || body.SchemaTruncated {
		t.Error("template bodies are short — must not report truncation")
	}
	if body.PagesTotal != 5 || len(body.Pages) != 5 {
		t.Fatalf("want 5 seeded pages, got total=%d len=%d", body.PagesTotal, len(body.Pages))
	}
	types := map[string]string{}
	for _, p := range body.Pages {
		types[p.Title] = p.Type
	}
	want := map[string]string{
		"项目目标": "purpose",
		"页面规范": "schema",
		"页面索引": "index",
		"变更日志": "log",
		"项目概览": "overview",
	}
	for title, typ := range want {
		if types[title] != typ {
			t.Errorf("page index: %q type = %q, want %q (all: %v)", title, types[title], typ, types)
		}
	}

	// owner 不匹配 → 404（不泄存在）。
	w = contextRequest(t, srv, "/v1/internal/wiki/projects/"+proj.ID.String()+
		"/ingest-context?owner_id="+uuid.New().String(), "secret-token")
	if w.Code != http.StatusNotFound {
		t.Errorf("foreign owner: want 404, got %d", w.Code)
	}

	// 未知项目 → 404。
	w = contextRequest(t, srv, "/v1/internal/wiki/projects/"+uuid.New().String()+
		"/ingest-context?owner_id="+h.owner.String(), "secret-token")
	if w.Code != http.StatusNotFound {
		t.Errorf("unknown project: want 404, got %d", w.Code)
	}
}

func TestIngestContext_BlankProjectAndTruncation(t *testing.T) {
	h := newContextTestHarness(t)
	// 空模板项目：无 purpose/schema/overview 页 → 字段为空字符串而非错误。
	proj, err := h.wiki.CreateProjectWithTemplate(context.Background(),
		h.owner, "ctx-blank", "", nil)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	defer h.dropProject(t, proj.ID)

	// 长 purpose 页（>4000 rune，CJK 混合）验证截断。
	longBody := strings.Repeat("目标ab", 1500) // 6000 runes
	if _, err := h.wiki.CreatePage(context.Background(), wikistore.CreatePageInput{
		ProjectID:   proj.ID,
		Title:       "项目目标",
		Frontmatter: map[string]any{"type": "purpose"},
		BodyMd:      longBody,
		ActorID:     "test",
	}); err != nil {
		t.Fatalf("create purpose page: %v", err)
	}

	srv := mountContextServer(t, h.wiki, "secret-token")
	w := contextRequest(t, srv, "/v1/internal/wiki/projects/"+proj.ID.String()+
		"/ingest-context?owner_id="+h.owner.String(), "secret-token")
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (body=%s)", w.Code, w.Body.String())
	}
	var body struct {
		Purpose          string `json:"purpose"`
		PurposeTruncated bool   `json:"purpose_truncated"`
		Schema           string `json:"schema"`
		Overview         string `json:"overview"`
		PagesTotal       int    `json:"pages_total"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.PurposeTruncated {
		t.Error("6000-rune purpose must report truncated=true")
	}
	if got := len([]rune(body.Purpose)); got != ingestContextBodyRunes {
		t.Errorf("purpose truncated to %d runes, want %d", got, ingestContextBodyRunes)
	}
	if body.Schema != "" {
		t.Errorf("blank project has no schema page, want empty, got %dB", len(body.Schema))
	}
	if body.Overview != "" {
		t.Errorf("blank project has no overview page, want empty, got %dB", len(body.Overview))
	}
	if body.PagesTotal != 1 {
		t.Errorf("pages_total = %d, want 1", body.PagesTotal)
	}
}
