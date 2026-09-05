// POST /ingest 的 content_hash 增量短路集成测试（handler 级，真库）。
// Skips when DATABASE_URL unset.

package ingest

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/biumind/biumind/services/brain/internal/publisher"
	"github.com/biumind/biumind/services/brain/internal/wiki/sources"
	wikistore "github.com/biumind/biumind/services/brain/internal/wiki/store"
	"github.com/google/uuid"
)

type dedupHarness struct {
	*ingestTestHarness
	sources *sources.Store
	mux     *http.ServeMux
	token   string
}

func newDedupHarness(t *testing.T) *dedupHarness {
	t.Helper()
	h := newIngestTestHarness(t)
	sourcesStore := sources.New(h.pool)
	srv := &Server{
		Store:     h.st,
		Wiki:      wikistore.New(h.pool),
		Publisher: publisher.Noop{},
		Verifier:  bauth.NewVerifier("dedup-test-secret", "test", "test"),
		Logger:    slog.Default(),
		Sources:   sourcesStore,
	}
	mux := http.NewServeMux()
	srv.Mount(mux)

	signer := bauth.NewSigner("dedup-test-secret", "test", "test", time.Hour)
	tok, err := signer.Sign(&bauth.Claims{UserID: h.owner.String()})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return &dedupHarness{ingestTestHarness: h, sources: sourcesStore, mux: mux, token: tok}
}

// newSource 建一个 parse_status=done、带 content_hash 的 upload source。
func (d *dedupHarness) newSource(t *testing.T, hash []byte) *sources.Source {
	t.Helper()
	uid := d.owner
	src, err := d.sources.Upsert(context.Background(), sources.CreateInput{
		ProjectID:     d.pid,
		UserID:        &uid,
		RelPath:       "docs/" + uuid.NewString() + ".md",
		Filename:      "f.md",
		ContentHash:   hash,
		ExtractedText: "parsed content",
		ParseStatus:   "done",
	})
	if err != nil {
		t.Fatalf("Upsert source: %v", err)
	}
	return src
}

func (d *dedupHarness) postIngest(t *testing.T, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost,
		fmt.Sprintf("/v1/wiki/projects/%s/ingest", d.pid), strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+d.token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	d.mux.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

// simulateSuccess 把任务推进到 done（含一页），模拟 worker 跑完。
func (d *dedupHarness) simulateSuccess(t *testing.T, taskID string) {
	t.Helper()
	ctx := context.Background()
	tid, err := uuid.Parse(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.st.MarkRunning(ctx, tid, "worker", "wiki-llm-worker"); err != nil {
		t.Fatal(err)
	}
	if err := d.st.AppendResultPage(ctx, tid, uuid.New(), nil, "worker", "wiki-llm-worker"); err != nil {
		t.Fatal(err)
	}
	if err := d.st.MarkTerminal(ctx, tid, StatusDone, "", "worker", "wiki-llm-worker"); err != nil {
		t.Fatal(err)
	}
}

func TestIngestDedup_SkipWhenHashUnchanged(t *testing.T) {
	d := newDedupHarness(t)
	src := d.newSource(t, []byte("hash-v1-0123456789abcdef0123456789ab"))

	// 第一次：正常建任务（201）。
	code, out := d.postIngest(t, fmt.Sprintf(`{"source_id":%q,"title":"t"}`, src.ID))
	if code != http.StatusCreated {
		t.Fatalf("first create = %d %v, want 201", code, out)
	}
	if out["deduplicated"] == true {
		t.Fatal("first create must not be deduplicated")
	}
	d.simulateSuccess(t, out["id"].(string))

	// 第二次：hash 未变 + 上次成功 → 200 deduplicated，复用 result_pages。
	code, out = d.postIngest(t, fmt.Sprintf(`{"source_id":%q,"title":"t"}`, src.ID))
	if code != http.StatusOK {
		t.Fatalf("dedup create = %d %v, want 200", code, out)
	}
	if out["deduplicated"] != true {
		t.Fatalf("deduplicated = %v, want true", out["deduplicated"])
	}
	rp, ok := out["result_pages"].([]any)
	if !ok || len(rp) != 1 {
		t.Fatalf("dedup result_pages = %v, want 1 entry", out["result_pages"])
	}
	if out["status"] != StatusDone {
		t.Errorf("dedup status = %v, want done", out["status"])
	}

	// 短路不得新建任务：项目里这个 source 仍只有 1 个任务。
	var n int
	if err := d.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM brain.ingest_tasks WHERE source_id = $1`, src.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("tasks for source = %d, want 1 (dedup must not create)", n)
	}
}

func TestIngestDedup_RerunWhenHashChanged(t *testing.T) {
	d := newDedupHarness(t)
	src := d.newSource(t, []byte("hash-v1-0123456789abcdef0123456789ab"))

	code, out := d.postIngest(t, fmt.Sprintf(`{"source_id":%q}`, src.ID))
	if code != http.StatusCreated {
		t.Fatalf("first create = %d %v", code, out)
	}
	d.simulateSuccess(t, out["id"].(string))

	// 重新解析后 content_hash 变化 → 短路不命中，正常建任务。
	if _, err := d.sources.UpdateParseStatus(context.Background(), sources.UpdateParseInput{
		ID:            src.ID,
		ParseStatus:   "done",
		ExtractedText: "new content",
		ContentHash:   []byte("hash-v2-0123456789abcdef0123456789ab"),
	}); err != nil {
		t.Fatal(err)
	}
	code, out = d.postIngest(t, fmt.Sprintf(`{"source_id":%q}`, src.ID))
	if code != http.StatusCreated {
		t.Fatalf("re-ingest after hash change = %d %v, want 201", code, out)
	}
	if out["deduplicated"] == true {
		t.Fatal("hash changed must not deduplicate")
	}
}

func TestIngestDedup_RerunWhenLastFailed(t *testing.T) {
	d := newDedupHarness(t)
	src := d.newSource(t, []byte("hash-v1-0123456789abcdef0123456789ab"))

	code, out := d.postIngest(t, fmt.Sprintf(`{"source_id":%q}`, src.ID))
	if code != http.StatusCreated {
		t.Fatalf("first create = %d %v", code, out)
	}
	// 上次失败 → 即使 hash 一致也正常重跑。
	tid, _ := uuid.Parse(out["id"].(string))
	if err := d.st.MarkTerminal(context.Background(), tid, StatusFailed, "boom", "worker", "wiki-llm-worker"); err != nil {
		t.Fatal(err)
	}
	code, out = d.postIngest(t, fmt.Sprintf(`{"source_id":%q}`, src.ID))
	if code != http.StatusCreated {
		t.Fatalf("re-ingest after failure = %d %v, want 201", code, out)
	}
	if out["deduplicated"] == true {
		t.Fatal("last run failed must not deduplicate")
	}
}

func TestIngestDedup_ForceBypasses(t *testing.T) {
	d := newDedupHarness(t)
	src := d.newSource(t, []byte("hash-v1-0123456789abcdef0123456789ab"))

	code, out := d.postIngest(t, fmt.Sprintf(`{"source_id":%q}`, src.ID))
	if code != http.StatusCreated {
		t.Fatalf("first create = %d %v", code, out)
	}
	d.simulateSuccess(t, out["id"].(string))

	code, out = d.postIngest(t, fmt.Sprintf(`{"source_id":%q,"force":true}`, src.ID))
	if code != http.StatusCreated {
		t.Fatalf("force re-ingest = %d %v, want 201", code, out)
	}
	if out["deduplicated"] == true {
		t.Fatal("force=true must bypass dedup")
	}
}

func TestIngestDedup_NoHashFallsThrough(t *testing.T) {
	d := newDedupHarness(t)
	// 老数据：source 无 content_hash → 永不短路。
	src := d.newSource(t, nil)

	code, out := d.postIngest(t, fmt.Sprintf(`{"source_id":%q}`, src.ID))
	if code != http.StatusCreated {
		t.Fatalf("first create = %d %v", code, out)
	}
	d.simulateSuccess(t, out["id"].(string))

	code, out = d.postIngest(t, fmt.Sprintf(`{"source_id":%q}`, src.ID))
	if code != http.StatusCreated {
		t.Fatalf("no-hash re-ingest = %d %v, want 201", code, out)
	}
	if out["deduplicated"] == true {
		t.Fatal("source without hash must not deduplicate")
	}
}

// webclip 链式 ingest 核验：kind=webclip 的 source（content_hash = sha256(url|content)）
// 同样走短路 —— 重剪同内容重 ingest 复用结果，首次/内容变化正常跑。
func TestIngestDedup_WebclipSource(t *testing.T) {
	d := newDedupHarness(t)
	src, dup, err := d.sources.CreateWebclip(context.Background(), sources.CreateWebclipInput{
		ProjectID:   d.pid,
		UserID:      d.owner,
		URL:         "https://example.com/a",
		Title:       "a",
		Raw:         "clip body",
		ContentHash: []byte("webclip-hash-0123456789abcdef0123"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if dup {
		t.Fatal("fresh webclip source must not be dup")
	}

	code, out := d.postIngest(t, fmt.Sprintf(`{"source_id":%q}`, src.ID))
	if code != http.StatusCreated {
		t.Fatalf("webclip first ingest = %d %v, want 201", code, out)
	}
	d.simulateSuccess(t, out["id"].(string))

	code, out = d.postIngest(t, fmt.Sprintf(`{"source_id":%q}`, src.ID))
	if code != http.StatusOK || out["deduplicated"] != true {
		t.Fatalf("webclip re-ingest = %d %v, want 200 deduplicated", code, out)
	}
}
