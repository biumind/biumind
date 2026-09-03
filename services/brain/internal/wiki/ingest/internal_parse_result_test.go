// parse-result 回写集成测试（B1 OCR，docs/BiuMind-Wiki-OCR-Plan.md §5.3）：
//   ① parser=mineru 走 OCR charger（fake model-relay 记录 model 字段分档）
//   ② [terminal] 终态错误 → retries 直接置上限，不再被 ListParseQueue 重扫
//   ③ done 带 parser → parse_meta 写入 parser 键（provenance）
// DATABASE_URL 未设跳过（同 store_test.go / internal_context_test.go 惯例）。

package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/biumind/biumind/services/brain/internal/wiki/sources"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// fakeRelay 假装 model-relay /v1/internal/usage/charge，记录每次请求的
// body 供断言计费分档。
type fakeRelay struct {
	srv  *httptest.Server
	mu   sync.Mutex
	reqs []map[string]any
}

func newFakeRelay(t *testing.T) *fakeRelay {
	t.Helper()
	f := &fakeRelay{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.reqs = append(f.reqs, body)
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"charged_amount":10}`))
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeRelay) bodies() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]map[string]any, len(f.reqs))
	copy(out, f.reqs)
	return out
}

type parseResultHarness struct {
	pool    *pgxpool.Pool
	sources *sources.Store
	owner   uuid.UUID
	pid     uuid.UUID
	fileID  uuid.UUID
}

func newParseResultHarness(t *testing.T) *parseResultHarness {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL unset — skipping integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	h := &parseResultHarness{
		pool: pool, sources: sources.New(pool),
		owner: uuid.New(), fileID: uuid.New(),
	}
	ctx := context.Background()
	if err := pool.QueryRow(ctx,
		`INSERT INTO brain.projects (owner_id, name) VALUES ($1, $2) RETURNING id`,
		h.owner, "parse-result-test").Scan(&h.pid); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	// ListParseQueue 只收 file_id 非空的 upload 行；FK 指向 files.objects，
	// 插一条最小文件行（sha256 按用户唯一，随机 hex 防撞）。
	sha := sha256.Sum256([]byte("fake-" + h.fileID.String()))
	if _, err := pool.Exec(ctx, `
		INSERT INTO files.objects (id, user_id, sha256, size_bytes, bucket, object_key, source)
		VALUES ($1, $2, $3, 10, 'biumind-files', $4, 'test')`,
		h.fileID, h.owner, hex.EncodeToString(sha[:]), "test/"+h.fileID.String()); err != nil {
		t.Fatalf("insert file object: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(),
			`DELETE FROM brain.projects WHERE id = $1`, h.pid); err != nil {
			t.Logf("cleanup project: %v", err)
		}
		if _, err := pool.Exec(context.Background(),
			`DELETE FROM files.objects WHERE id = $1`, h.fileID); err != nil {
			t.Logf("cleanup file: %v", err)
		}
		pool.Close()
	})
	return h
}

func (h *parseResultHarness) createSource(t *testing.T, relPath string) *sources.Source {
	t.Helper()
	src, err := h.sources.Upsert(context.Background(), sources.CreateInput{
		ProjectID: h.pid, UserID: &h.owner, FileID: &h.fileID,
		RelPath: relPath, Filename: relPath, Mime: "application/pdf",
		// 与 handleCreate → normalizeCreateReq 的生产路径一致：parse_meta
		// 总是非 nil 空 map（nil 会 marshal 成 jsonb 'null' 标量，jsonb_set 拒绝）。
		ParseMeta: map[string]any{},
	})
	if err != nil {
		t.Fatalf("upsert source: %v", err)
	}
	return src
}

func mountParseResultServer(t *testing.T, st *sources.Store, charger, ocrCharger *UsageCharger) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	s := NewInternalServer(st, "secret-token",
		slog.New(slog.NewTextHandler(nopWriter{}, nil)))
	s.Charger = charger
	s.OCRCharger = ocrCharger
	s.Mount(mux)
	return mux
}

func postParseResult(t *testing.T, h http.Handler, src *sources.Source, owner uuid.UUID, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(body)
	r := httptest.NewRequest(http.MethodPost,
		"/v1/internal/wiki/sources/"+src.ID.String()+"/parse-result?owner_id="+owner.String(),
		strings.NewReader(string(raw)))
	r.Header.Set("X-Biumind-Internal-Token", "secret-token")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("parse-result: want 200, got %d (body=%s)", w.Code, w.Body.String())
	}
	return w
}

func doneBody(text, parser string, pages int) map[string]any {
	sum := sha256.Sum256([]byte(text))
	return map[string]any{
		"parse_status":   "done",
		"extracted_text": text,
		"content_hash":   hex.EncodeToString(sum[:]),
		"page_count":     pages,
		"parser":         parser,
	}
}

func TestParseResult_BillingTierByParser(t *testing.T) {
	h := newParseResultHarness(t)
	relay := newFakeRelay(t)
	charger := NewUsageCharger(relay.srv.URL, "tok", "wiki-parse-text", nil)
	ocrCharger := NewUsageCharger(relay.srv.URL, "tok", "wiki-ocr", nil)
	if charger == nil || ocrCharger == nil {
		t.Fatal("chargers must construct with non-empty args")
	}
	srv := mountParseResultServer(t, h.sources, charger, ocrCharger)

	// parser=mineru → OCR charger（wiki-ocr 档）。
	mineruSrc := h.createSource(t, "scan.pdf")
	postParseResult(t, srv, mineruSrc, h.owner, doneBody("ocr text", "mineru", 5))
	// parser=pypdf → 现有文本档 charger（wiki-parse-text）。
	pypdfSrc := h.createSource(t, "text.pdf")
	postParseResult(t, srv, pypdfSrc, h.owner, doneBody("text layer", "pypdf", 3))

	got := relay.bodies()
	if len(got) != 2 {
		t.Fatalf("relay calls = %d, want 2", len(got))
	}
	if got[0]["model"] != "wiki-ocr" {
		t.Errorf("mineru charge model = %v, want wiki-ocr", got[0]["model"])
	}
	if got[0]["quantity"] != float64(5) {
		t.Errorf("mineru charge quantity = %v, want 5", got[0]["quantity"])
	}
	if got[0]["idempotency_key"] != "parse:"+mineruSrc.ID.String() {
		t.Errorf("mineru idempotency_key = %v, want parse:<source_id>", got[0]["idempotency_key"])
	}
	if got[1]["model"] != "wiki-parse-text" {
		t.Errorf("pypdf charge model = %v, want wiki-parse-text", got[1]["model"])
	}
}

func TestParseResult_MineruWithNilOCRChargerIsFree(t *testing.T) {
	h := newParseResultHarness(t)
	relay := newFakeRelay(t)
	charger := NewUsageCharger(relay.srv.URL, "tok", "wiki-parse-text", nil)
	// OCRCharger nil = OCR 免费兜底：mineru 解析不扣费，也不回落到文本档。
	srv := mountParseResultServer(t, h.sources, charger, nil)

	src := h.createSource(t, "scan.pdf")
	postParseResult(t, srv, src, h.owner, doneBody("ocr text", "mineru", 5))
	if got := relay.bodies(); len(got) != 0 {
		t.Errorf("relay calls = %d, want 0 (OCR free fallback)", len(got))
	}
}

func TestParseResult_TerminalErrorSetsRetriesToMax(t *testing.T) {
	h := newParseResultHarness(t)
	srv := mountParseResultServer(t, h.sources, nil, nil)
	ctx := context.Background()

	// 终态错误：一次回写即 retries=ParseMaxRetries，出队不再重扫。
	terminal := h.createSource(t, "corrupted.pdf")
	postParseResult(t, srv, terminal, h.owner, map[string]any{
		"parse_status": "error",
		"parse_error":  "[terminal] MinerU rejected: file corrupted",
	})
	got, err := h.sources.GetByID(ctx, terminal.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ParseStatus != "error" {
		t.Errorf("terminal parse_status = %q, want error", got.ParseStatus)
	}

	// 可重试错误：retries 逐次 +1，仍在队列里。
	retryable := h.createSource(t, "flaky.pdf")
	postParseResult(t, srv, retryable, h.owner, map[string]any{
		"parse_status": "error",
		"parse_error":  "mineru connection timeout",
	})

	var terminalRetries, retryableRetries int
	if err := h.pool.QueryRow(ctx,
		`SELECT retries FROM brain.wiki_sources WHERE id = $1`, terminal.ID).
		Scan(&terminalRetries); err != nil {
		t.Fatal(err)
	}
	if terminalRetries != sources.ParseMaxRetries {
		t.Errorf("terminal retries = %d, want %d (出局不再重扫)",
			terminalRetries, sources.ParseMaxRetries)
	}
	if err := h.pool.QueryRow(ctx,
		`SELECT retries FROM brain.wiki_sources WHERE id = $1`, retryable.ID).
		Scan(&retryableRetries); err != nil {
		t.Fatal(err)
	}
	if retryableRetries != 1 {
		t.Errorf("retryable retries = %d, want 1", retryableRetries)
	}

	queue, err := h.sources.ListParseQueue(ctx, sources.ParseMaxRetries, 100)
	if err != nil {
		t.Fatal(err)
	}
	queued := map[uuid.UUID]bool{}
	for _, q := range queue {
		queued[q.ID] = true
	}
	if queued[terminal.ID] {
		t.Error("terminal source must NOT re-enter parse queue")
	}
	if !queued[retryable.ID] {
		t.Error("retryable source must stay in parse queue")
	}
}

func TestParseResult_DoneWritesParserToParseMeta(t *testing.T) {
	h := newParseResultHarness(t)
	srv := mountParseResultServer(t, h.sources, nil, nil)

	src := h.createSource(t, "scan.pdf")
	postParseResult(t, srv, src, h.owner, doneBody("ocr text", "mineru", 7))

	got, err := h.sources.GetByID(context.Background(), src.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ParseStatus != "done" {
		t.Errorf("parse_status = %q, want done", got.ParseStatus)
	}
	if got.ParseMeta["parser"] != "mineru" {
		t.Errorf("parse_meta.parser = %v, want mineru", got.ParseMeta["parser"])
	}
	if got.ParseMeta["page_count"] != float64(7) {
		t.Errorf("parse_meta.page_count = %v, want 7", got.ParseMeta["page_count"])
	}

	// 旧 worker 不上报 parser（空串）→ parse_meta 不写 parser 键。
	legacy := h.createSource(t, "legacy.pdf")
	legacySum := sha256.Sum256([]byte("text"))
	postParseResult(t, srv, legacy, h.owner, map[string]any{
		"parse_status":   "done",
		"extracted_text": "text",
		"content_hash":   hex.EncodeToString(legacySum[:]),
		"page_count":     2,
	})
	gotLegacy, err := h.sources.GetByID(context.Background(), legacy.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := gotLegacy.ParseMeta["parser"]; ok {
		t.Errorf("empty parser must not write parse_meta.parser, got %v",
			gotLegacy.ParseMeta["parser"])
	}
	if gotLegacy.ParseMeta["page_count"] != float64(2) {
		t.Errorf("parse_meta.page_count = %v, want 2", gotLegacy.ParseMeta["page_count"])
	}
}

// 回归：Upsert 传 nil ParseMeta 曾落 jsonb 'null' 标量，之后 UpdateParseStatus
// 的 jsonb_set 报 "cannot set path in scalar"。兜底 '{}' 后全链应畅通。
func TestUpsert_NilParseMetaThenJsonbSet(t *testing.T) {
	h := newParseResultHarness(t)

	src, err := h.sources.Upsert(context.Background(), sources.CreateInput{
		ProjectID: h.pid, UserID: &h.owner, FileID: &h.fileID,
		RelPath: "nil-meta.pdf", Filename: "nil-meta.pdf", Mime: "application/pdf",
		// 刻意 nil（非空 map）——marshal 出 'null' 标量的路径。
		ParseMeta: nil,
	})
	if err != nil {
		t.Fatalf("upsert nil parse_meta: %v", err)
	}
	sum := sha256.Sum256([]byte("nil meta text"))
	if _, err := h.sources.UpdateParseStatus(context.Background(), sources.UpdateParseInput{
		ID: src.ID, ParseStatus: "done", ExtractedText: "nil meta text",
		ContentHash: sum[:], PageCount: 3, Parser: "mineru",
	}); err != nil {
		t.Fatalf("jsonb_set on nil-origin parse_meta: %v", err)
	}
	got, err := h.sources.GetByID(context.Background(), src.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ParseMeta["parser"] != "mineru" || got.ParseMeta["page_count"] != float64(3) {
		t.Errorf("parse_meta = %v, want parser=mineru page_count=3", got.ParseMeta)
	}
}
