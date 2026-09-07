package api

// agent_runs GET 端点 + restore if_match 409 的集成测试（§1.2 P2）。
// 真 Postgres（Server.Store 接真库），DATABASE_URL 未设时 skip。

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/biumind/biumind/services/brain/internal/wiki/store"
)

type apiIntHarness struct {
	srv   *Server
	st    *store.Store
	pool  *pgxpool.Pool
	token string
	uid   uuid.UUID
}

func newAPIIntHarness(t *testing.T) *apiIntHarness {
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
	st := store.New(pool)
	verifier := bauth.NewVerifier("test-secret-very-long-string-for-hmac-32", "iss", "aud")
	srv := NewServer(st, nil, verifier, slog.New(slog.NewTextHandler(io.Discard, nil)))
	uid := uuid.New()
	signer := bauth.NewSigner("test-secret-very-long-string-for-hmac-32", "iss", "aud", time.Minute)
	tok, err := signer.Sign(&bauth.Claims{UserID: uid.String()})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return &apiIntHarness{srv: srv, st: st, pool: pool, token: tok, uid: uid}
}

func (h *apiIntHarness) createProject(t *testing.T) uuid.UUID {
	t.Helper()
	p, err := h.st.CreateProjectWithTemplate(context.Background(), h.uid, "api-int-"+uuid.NewString()[:8], "", nil)
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	t.Cleanup(func() {
		if _, err := h.pool.Exec(context.Background(),
			`DELETE FROM brain.projects WHERE id = $1`, p.ID); err != nil {
			t.Fatalf("cleanup: %v", err)
		}
	})
	return p.ID
}

// do 发认证请求，返回 (status, 解码后 body)。
func (h *apiIntHarness) do(t *testing.T, method, path string, body []byte) (int, map[string]any) {
	t.Helper()
	mux := http.NewServeMux()
	h.srv.Mount(mux)
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+h.token)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	out := map[string]any{}
	if rr.Body.Len() > 0 {
		if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode %s %s: %v (%s)", method, path, err, rr.Body.String())
		}
	}
	return rr.Code, out
}

func TestAgentRunsEndpoints(t *testing.T) {
	h := newAPIIntHarness(t)
	pid := h.createProject(t)
	ctx := context.Background()

	// 造一条 done run + 一条 running；done run 关联两页快照。
	if err := h.st.CreateAgentRun(ctx, store.AgentRun{
		RunID: "run-done", ProjectID: pid, OwnerID: h.uid,
		Mode: "deep", Model: "m1", Instruction: "整理",
	}); err != nil {
		t.Fatal(err)
	}
	if err := h.st.CreateAgentRun(ctx, store.AgentRun{
		RunID: "run-live", ProjectID: pid, OwnerID: h.uid, Model: "m1",
	}); err != nil {
		t.Fatal(err)
	}
	p1, err := h.st.CreatePage(ctx, store.CreatePageInput{ProjectID: pid, Title: "p1", ActorID: h.uid.String()})
	if err != nil {
		t.Fatal(err)
	}
	p2, err := h.st.CreatePage(ctx, store.CreatePageInput{ProjectID: pid, Title: "p2", ActorID: h.uid.String()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.st.UpdatePage(ctx, store.UpdatePageInput{
		PageID: p1.ID, Title: strPtrAPI("p1b"), ActorID: h.uid.String(), RunID: "run-done",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.st.UpdatePage(ctx, store.UpdatePageInput{
		PageID: p2.ID, Title: strPtrAPI("p2b"), ActorID: h.uid.String(), RunID: "run-done",
	}); err != nil {
		t.Fatal(err)
	}
	if err := h.st.FinishAgentRun(ctx, "run-done", store.AgentRunDone, ""); err != nil {
		t.Fatal(err)
	}

	// 列表：两条，run-done 排前（后开始）或按 started_at DESC；验聚合数。
	code, body := h.do(t, "GET", "/v1/wiki/projects/"+pid.String()+"/agent/runs", nil)
	if code != http.StatusOK {
		t.Fatalf("list: %d %v", code, body)
	}
	runs, _ := body["runs"].([]any)
	if len(runs) != 2 {
		t.Fatalf("runs = %d, want 2", len(runs))
	}
	var done map[string]any
	for _, r := range runs {
		rm := r.(map[string]any)
		if rm["run_id"] == "run-done" {
			done = rm
		}
	}
	if done == nil {
		t.Fatalf("run-done missing: %v", runs)
	}
	if done["status"] != "done" || done["finished_at"] == nil {
		t.Fatalf("run-done row: %v", done)
	}
	if n, _ := done["changed_pages"].(float64); n != 2 {
		t.Fatalf("changed_pages = %v, want 2", done["changed_pages"])
	}

	// 详情：改动清单两条 update，标题为写前标题。
	code, body = h.do(t, "GET", "/v1/wiki/projects/"+pid.String()+"/agent/runs/run-done", nil)
	if code != http.StatusOK {
		t.Fatalf("detail: %d %v", code, body)
	}
	changes, _ := body["changes"].([]any)
	if len(changes) != 2 {
		t.Fatalf("changes = %d, want 2 (%v)", len(changes), body)
	}
	for _, c := range changes {
		cm := c.(map[string]any)
		if cm["op"] != "update" || cm["revision_id"] == "" || cm["page_id"] == "" {
			t.Fatalf("change row wrong: %v", cm)
		}
	}

	// 未知 run → 404。
	code, _ = h.do(t, "GET", "/v1/wiki/projects/"+pid.String()+"/agent/runs/nope", nil)
	if code != http.StatusNotFound {
		t.Fatalf("unknown run: %d, want 404", code)
	}
}

func TestRestoreIfMatchEndpoint(t *testing.T) {
	h := newAPIIntHarness(t)
	pid := h.createProject(t)
	ctx := context.Background()

	page, err := h.st.CreatePage(ctx, store.CreatePageInput{ProjectID: pid, Title: "occ", ActorID: h.uid.String()})
	if err != nil {
		t.Fatal(err)
	}
	// 造一条快照 + 一次版本推进。
	if _, err := h.st.UpdatePage(ctx, store.UpdatePageInput{
		PageID: page.ID, Title: strPtrAPI("v1"), ActorID: h.uid.String(),
	}); err != nil {
		t.Fatal(err)
	}
	revs, err := h.st.ListPageRevisions(ctx, page.ID, 10, 0)
	if err != nil || len(revs) == 0 {
		t.Fatalf("revisions: %v %d", err, len(revs))
	}
	rid := revs[0].ID.String()
	cur, _ := h.st.GetPage(ctx, page.ID)
	base := "/v1/wiki/projects/" + pid.String() + "/pages/" + page.ID.String() +
		"/revisions/" + rid + "/restore"

	// 过期 if_match → 409 带 server_version + server_payload。
	stale, _ := json.Marshal(map[string]any{"if_match_version": cur.Version + 99})
	code, body := h.do(t, "POST", base, stale)
	if code != http.StatusConflict {
		t.Fatalf("stale if_match: %d %v, want 409", code, body)
	}
	if v, _ := body["server_version"].(float64); int(v) != cur.Version {
		t.Fatalf("409 server_version = %v, want %d", body["server_version"], cur.Version)
	}
	if body["server_payload"] == nil {
		t.Fatalf("409 missing server_payload: %v", body)
	}

	// 正确 if_match → 200。
	ok, _ := json.Marshal(map[string]any{"if_match_version": cur.Version})
	code, _ = h.do(t, "POST", base, ok)
	if code != http.StatusOK {
		t.Fatalf("matching if_match: %d, want 200", code)
	}

	// 无 body → 向后兼容覆盖式 → 200。
	code, _ = h.do(t, "POST", base, nil)
	if code != http.StatusOK {
		t.Fatalf("no body: %d, want 200", code)
	}
}

// revisionOut 的 run_id 字段：agent 快照带，人工快照不带。
func TestRevisionOutRunID(t *testing.T) {
	h := newAPIIntHarness(t)
	pid := h.createProject(t)
	ctx := context.Background()

	page, err := h.st.CreatePage(ctx, store.CreatePageInput{ProjectID: pid, Title: "p", ActorID: h.uid.String()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.st.UpdatePage(ctx, store.UpdatePageInput{
		PageID: page.ID, Title: strPtrAPI("v1"), ActorID: h.uid.String(), RunID: "run-r",
	}); err != nil {
		t.Fatal(err)
	}
	// 过窗口后人工再改 → NULL run_id 快照。
	if _, err := h.pool.Exec(ctx, `
		UPDATE brain.page_revisions SET created_at = now() - interval '6 minutes'
		WHERE page_id = $1
	`, page.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.st.UpdatePage(ctx, store.UpdatePageInput{
		PageID: page.ID, Title: strPtrAPI("v2"), ActorID: h.uid.String(),
	}); err != nil {
		t.Fatal(err)
	}

	code, body := h.do(t, "GET",
		"/v1/wiki/projects/"+pid.String()+"/pages/"+page.ID.String()+"/revisions", nil)
	if code != http.StatusOK {
		t.Fatalf("list revisions: %d", code)
	}
	revs, _ := body["revisions"].([]any)
	if len(revs) != 2 {
		t.Fatalf("revisions = %d, want 2", len(revs))
	}
	// 倒序：第一条人工（无 run_id 键），第二条 agent（run_id=run-r）。
	first, second := revs[0].(map[string]any), revs[1].(map[string]any)
	if _, ok := first["run_id"]; ok {
		t.Fatalf("manual revision must omit run_id: %v", first)
	}
	if second["run_id"] != "run-r" {
		t.Fatalf("agent revision run_id = %v, want run-r", second)
	}
}

func strPtrAPI(s string) *string { return &s }
