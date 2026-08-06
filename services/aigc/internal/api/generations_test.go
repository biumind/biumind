package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/biumind/biumind/services/aigc/internal/billing"
	"github.com/biumind/biumind/services/aigc/internal/store"
	"github.com/google/uuid"
)

// fakeBilling 是测试用的内存 billing client. 真 Client 走 HTTP, 这里直接复用 *Client
// 字段, 调 httptest 起的 fake identity 比单独 mock 更可靠 (验证完整 HTTP 链).
//
// 用 newFakeBillingServer 起一个简易 identity stub, 同 user 全局额度.
type fakeBilling struct {
	balance     int64
	consumeFail string // "" / "402" / "500"
}

func newFakeBillingServer(fb *fakeBilling) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/internal/credits/consume", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Amount int64 `json:"amount"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch fb.consumeFail {
		case "402":
			http.Error(w, "insufficient", http.StatusPaymentRequired)
			return
		case "500":
			http.Error(w, "boom", 500)
			return
		}
		if body.Amount > fb.balance {
			http.Error(w, "insufficient", http.StatusPaymentRequired)
			return
		}
		fb.balance -= body.Amount
		fmt.Fprintf(w, `{"log":{"id":"11111111-1111-1111-1111-111111111111"},"balance":{"permanent_balance":%d,"time_limited_balance":0}}`, fb.balance)
	})
	mux.HandleFunc("POST /v1/internal/credits/refund", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Amount int64 `json:"amount"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		fb.balance += body.Amount
		fmt.Fprintf(w, `{"log":{"id":"22222222-2222-2222-2222-222222222222"},"balance":{"permanent_balance":%d,"time_limited_balance":0}}`, fb.balance)
	})
	// 段3.6: submit 改读余额 (GetBalanceTotal) 而非 Consume。
	mux.HandleFunc("GET /v1/internal/credits/{uid}/balance", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"balance":{"permanent_balance":%d,"time_limited_balance":0}}`, fb.balance)
	})
	return httptest.NewServer(mux)
}

func newServerWithBilling(t *testing.T, fb *fakeBilling) (*Server, *http.ServeMux, *httptest.Server) {
	t.Helper()
	srv, mux := newTestServer(t)
	billSrv := newFakeBillingServer(fb)
	srv.Billing = billing.NewClient(billSrv.URL, "test-internal-token")
	t.Cleanup(billSrv.Close)
	return srv, mux, billSrv
}

// 通用 helper: 发 POST + Bearer.
func doJSON(t *testing.T, mux *http.ServeMux, method, path, token string, body any) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	var out map[string]any
	if w.Body.Len() > 0 {
		_ = json.Unmarshal(w.Body.Bytes(), &out)
	}
	return w, out
}

// ════════════════════════════════════════════════════════════
// POST /v1/generations
// ════════════════════════════════════════════════════════════

func TestSubmitGeneration_HappyPath(t *testing.T) {
	fb := &fakeBilling{balance: 100}
	srv, mux, _ := newServerWithBilling(t, fb)
	ensureSeedTestModel(t, srv)

	uid := uuid.New()
	tok := issueToken(t, uid, "pro", []string{"member"})

	w, body := doJSON(t, mux, "POST", "/v1/generations", tok, map[string]any{
		"type":            "image",
		"model_code":      "test-img-model",
		"prompt":          "柯基",
		"idempotency_key": "task-test-1",
	})
	if w.Code != 200 {
		t.Fatalf("status = %d body = %s", w.Code, w.Body.String())
	}
	task := body["task"].(map[string]any)
	if task["status"] != "pending" {
		t.Errorf("status = %v", task["status"])
	}
	if int(body["cost_credits"].(float64)) != 30 {
		t.Errorf("cost = %v, want 30 (展示估值)", body["cost_credits"])
	}
	// 段3.6: submit 不再扣费 (计费归 model-relay 生成时)。balance_after =
	// 当前余额 (未变), fb.balance 也不变。
	if int(body["balance_after"].(float64)) != 100 {
		t.Errorf("balance_after = %v, want 100 (未扣)", body["balance_after"])
	}
	if fb.balance != 100 {
		t.Errorf("billing balance = %d, want 100 (submit 不扣费)", fb.balance)
	}
}

// 段3.6: 提交不再因余额不足被拒 — 计费已移到 model-relay 生成时
// (Hold 余额不足才返 402)。这里验证 submit 即使余额很低也成功入队。
func TestSubmitGeneration_NoBillingAtSubmit(t *testing.T) {
	fb := &fakeBilling{balance: 0} // 余额 0 也应能提交 (relay 在生成时才扣)
	srv, mux, _ := newServerWithBilling(t, fb)
	ensureSeedTestModel(t, srv)

	uid := uuid.New()
	tok := issueToken(t, uid, "free", []string{"member"})
	w, body := doJSON(t, mux, "POST", "/v1/generations", tok, map[string]any{
		"type":       "image",
		"model_code": "test-img-model",
		"prompt":     "x",
	})
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200 (submit 不再做余额门槛), body=%s", w.Code, w.Body.String())
	}
	if fb.balance != 0 {
		t.Errorf("balance = %d, want 0 (submit 不扣费)", fb.balance)
	}
	if body["task"] == nil {
		t.Error("应入队返回 task")
	}
}

func TestSubmitGeneration_ModelNotFound(t *testing.T) {
	fb := &fakeBilling{balance: 100}
	_, mux, _ := newServerWithBilling(t, fb)

	uid := uuid.New()
	tok := issueToken(t, uid, "pro", []string{"member"})
	w, _ := doJSON(t, mux, "POST", "/v1/generations", tok, map[string]any{
		"type":       "image",
		"model_code": "nonexistent-model",
		"prompt":     "x",
	})
	if w.Code != 404 {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestSubmitGeneration_TypeMismatch(t *testing.T) {
	fb := &fakeBilling{balance: 100}
	srv, mux, _ := newServerWithBilling(t, fb)
	ensureSeedTestModel(t, srv) // type=image

	uid := uuid.New()
	tok := issueToken(t, uid, "pro", []string{"member"})
	w, _ := doJSON(t, mux, "POST", "/v1/generations", tok, map[string]any{
		"type":       "video", // 与模型 type 不匹配
		"model_code": "test-img-model",
		"prompt":     "x",
	})
	if w.Code != 400 {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

// 门禁: digital_human 暂无生成链路, 服务端早返 501 防绕过建必失败任务。
func TestSubmitGeneration_DigitalHumanGated(t *testing.T) {
	fb := &fakeBilling{balance: 100}
	_, mux, _ := newServerWithBilling(t, fb)

	uid := uuid.New()
	tok := issueToken(t, uid, "pro", []string{"member"})
	w, body := doJSON(t, mux, "POST", "/v1/generations", tok, map[string]any{
		"type":       "digital_human",
		"model_code": "whatever",
		"prompt":     "x",
	})
	if w.Code != http.StatusNotImplemented {
		t.Errorf("status = %d, want 501; body=%s", w.Code, w.Body.String())
	}
	errObj, _ := body["error"].(map[string]any)
	if errObj == nil || errObj["code"] != "type_not_available" {
		t.Errorf("error = %v, want code=type_not_available", body["error"])
	}
}

// hotparse 已放开: 不再 501, 越过门禁进到模型查找 (此处无 seed 模型 → 404),
// 证明门禁已对 hotparse 打开 (真实链路由 worker HotparseProvider 执行)。
func TestSubmitGeneration_HotparseOpened(t *testing.T) {
	fb := &fakeBilling{balance: 100}
	_, mux, _ := newServerWithBilling(t, fb)

	uid := uuid.New()
	tok := issueToken(t, uid, "pro", []string{"member"})
	w, _ := doJSON(t, mux, "POST", "/v1/generations", tok, map[string]any{
		"type":       "hotparse",
		"model_code": "no-such-hotparse-model",
		"prompt":     "",
	})
	if w.Code == http.StatusNotImplemented {
		t.Fatalf("hotparse still gated (501); want past-gate (404 model_not_found)")
	}
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (model not found, 证明越过门禁); body=%s", w.Code, w.Body.String())
	}
}

func TestSubmitGeneration_NoAuth(t *testing.T) {
	fb := &fakeBilling{balance: 100}
	_, mux, _ := newServerWithBilling(t, fb)

	w, _ := doJSON(t, mux, "POST", "/v1/generations", "", map[string]any{
		"type":       "image",
		"model_code": "test-img-model",
		"prompt":     "x",
	})
	if w.Code != 401 {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

// ════════════════════════════════════════════════════════════
// GET /v1/generations/{id}  +  /mine
// ════════════════════════════════════════════════════════════

func TestGetTask_Owner(t *testing.T) {
	fb := &fakeBilling{balance: 100}
	srv, mux, _ := newServerWithBilling(t, fb)
	ensureSeedTestModel(t, srv)

	uid := uuid.New()
	tok := issueToken(t, uid, "pro", []string{"member"})

	// submit
	w, body := doJSON(t, mux, "POST", "/v1/generations", tok, map[string]any{
		"type":       "image",
		"model_code": "test-img-model",
		"prompt":     "x",
	})
	if w.Code != 200 {
		t.Fatalf("submit: %d %s", w.Code, w.Body.String())
	}
	tid := body["task"].(map[string]any)["id"].(string)

	// get
	w2, body2 := doJSON(t, mux, "GET", "/v1/generations/"+tid, tok, nil)
	if w2.Code != 200 {
		t.Fatalf("get: %d body = %s", w2.Code, w2.Body.String())
	}
	if body2["task"].(map[string]any)["id"] != tid {
		t.Errorf("id roundtrip mismatch")
	}
}

func TestGetTask_NotFound(t *testing.T) {
	fb := &fakeBilling{balance: 100}
	_, mux, _ := newServerWithBilling(t, fb)
	tok := issueToken(t, uuid.New(), "pro", []string{"member"})
	w, _ := doJSON(t, mux, "GET", "/v1/generations/"+uuid.New().String(), tok, nil)
	if w.Code != 404 {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestListMyTasks(t *testing.T) {
	fb := &fakeBilling{balance: 1000}
	srv, mux, _ := newServerWithBilling(t, fb)
	ensureSeedTestModel(t, srv)

	uid := uuid.New()
	tok := issueToken(t, uid, "pro", []string{"member"})

	for i := 0; i < 3; i++ {
		w, _ := doJSON(t, mux, "POST", "/v1/generations", tok, map[string]any{
			"type":       "image",
			"model_code": "test-img-model",
			"prompt":     fmt.Sprintf("p%d", i),
		})
		if w.Code != 200 {
			t.Fatalf("submit %d: %d", i, w.Code)
		}
	}

	w, body := doJSON(t, mux, "GET", "/v1/generations/mine", tok, nil)
	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	tasks := body["tasks"].([]any)
	if len(tasks) != 3 {
		t.Errorf("tasks = %d, want 3", len(tasks))
	}
}

// ════════════════════════════════════════════════════════════
// PATCH visibility, DELETE, POST cancel
// ════════════════════════════════════════════════════════════

func TestSetVisibility_AndDelete(t *testing.T) {
	fb := &fakeBilling{balance: 100}
	srv, mux, _ := newServerWithBilling(t, fb)
	ensureSeedTestModel(t, srv)

	uid := uuid.New()
	tok := issueToken(t, uid, "pro", []string{"member"})

	_, body := doJSON(t, mux, "POST", "/v1/generations", tok, map[string]any{
		"type":       "image",
		"model_code": "test-img-model",
		"prompt":     "x",
	})
	tid := body["task"].(map[string]any)["id"].(string)

	// 改公开
	w, _ := doJSON(t, mux, "PATCH", "/v1/generations/"+tid+"/visibility", tok,
		map[string]any{"is_public": true})
	if w.Code != 200 {
		t.Fatalf("patch: %d %s", w.Code, w.Body.String())
	}

	// 软删
	w, _ = doJSON(t, mux, "DELETE", "/v1/generations/"+tid, tok, nil)
	if w.Code != 200 {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}

	// 默认 ListMine 不返回软删的
	w, lb := doJSON(t, mux, "GET", "/v1/generations/mine", tok, nil)
	if w.Code != 200 {
		t.Fatal()
	}
	tasks := lb["tasks"].([]any)
	if len(tasks) != 0 {
		t.Errorf("tasks after delete = %d, want 0", len(tasks))
	}
}

func TestCancelTask(t *testing.T) {
	fb := &fakeBilling{balance: 100}
	srv, mux, _ := newServerWithBilling(t, fb)
	ensureSeedTestModel(t, srv)

	uid := uuid.New()
	tok := issueToken(t, uid, "pro", []string{"member"})

	_, body := doJSON(t, mux, "POST", "/v1/generations", tok, map[string]any{
		"type":       "image",
		"model_code": "test-img-model",
		"prompt":     "x",
	})
	tid := body["task"].(map[string]any)["id"].(string)

	w, _ := doJSON(t, mux, "POST", "/v1/generations/"+tid+"/cancel", tok, nil)
	if w.Code != 200 {
		t.Fatalf("cancel: %d %s", w.Code, w.Body.String())
	}

	w, gb := doJSON(t, mux, "GET", "/v1/generations/"+tid, tok, nil)
	if w.Code != 200 {
		t.Fatal()
	}
	if gb["task"].(map[string]any)["status"] != "cancelled" {
		t.Errorf("status = %v", gb["task"].(map[string]any)["status"])
	}
}

func TestCancelTask_AlreadyCompleted(t *testing.T) {
	fb := &fakeBilling{balance: 100}
	srv, mux, _ := newServerWithBilling(t, fb)
	ensureSeedTestModel(t, srv)

	uid := uuid.New()
	tok := issueToken(t, uid, "pro", []string{"member"})

	_, body := doJSON(t, mux, "POST", "/v1/generations", tok, map[string]any{
		"type":       "image",
		"model_code": "test-img-model",
		"prompt":     "x",
	})
	tid := body["task"].(map[string]any)["id"].(string)
	taskID, _ := uuid.Parse(tid)

	// 把状态推到 completed
	now := time.Now()
	prog := int16(100)
	if err := srv.Store.UpdateTaskStatus(context.Background(), store.UpdateTaskStatusArgs{
		ID: taskID, Status: "completed", Progress: &prog, CompletedAt: &now,
	}); err != nil {
		t.Fatal(err)
	}

	w, _ := doJSON(t, mux, "POST", "/v1/generations/"+tid+"/cancel", tok, nil)
	if w.Code != 409 {
		t.Errorf("cancel completed: status = %d, want 409", w.Code)
	}
}
