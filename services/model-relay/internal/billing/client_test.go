package billing

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestServerWithToken(t *testing.T, h http.HandlerFunc, expectToken string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if expectToken != "" {
			if r.Header.Get("Authorization") != "Bearer "+expectToken {
				http.Error(w, "no auth", http.StatusUnauthorized)
				return
			}
		}
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	return NewClient(srv.URL, expectToken)
}

func TestHold_Success(t *testing.T) {
	c := newTestServerWithToken(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/internal/credit-holds" || r.Method != "POST" {
			http.Error(w, "wrong path", http.StatusBadRequest)
			return
		}
		var body HoldArgs
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.MaxAmount != 1000 {
			http.Error(w, "wrong amount", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"hold": map[string]any{
				"id":         "h-123",
				"user_id":    body.UserID,
				"max_amount": body.MaxAmount,
				"status":     "held",
			},
		})
	}, "secret")

	hold, err := c.Hold(context.Background(), HoldArgs{
		UserID: "u-1", MaxAmount: 1000, RefType: "chat_message",
	})
	if err != nil {
		t.Fatalf("hold: %v", err)
	}
	if hold.ID != "h-123" || hold.MaxAmount != 1000 {
		t.Fatalf("hold = %+v", hold)
	}
}

func TestHold_Insufficient(t *testing.T) {
	c := newTestServerWithToken(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "credits: insufficient balance", http.StatusPaymentRequired)
	}, "")

	_, err := c.Hold(context.Background(), HoldArgs{
		UserID: "u-1", MaxAmount: 1_000_000, RefType: "chat_message",
	})
	if err != ErrInsufficient {
		t.Fatalf("want ErrInsufficient, got %v", err)
	}
}

func TestSettle(t *testing.T) {
	var got struct {
		ActualAmount int64  `json:"actual_amount"`
		Remark       string `json:"remark"`
	}
	c := newTestServerWithToken(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v1/internal/credit-holds/") || !strings.HasSuffix(r.URL.Path, "/settle") {
			t.Fatalf("wrong path: %s", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"hold": map[string]any{"id": "h-1", "status": "settled"},
		})
	}, "")

	if err := c.Settle(context.Background(), "h-1", 250, "ok"); err != nil {
		t.Fatalf("settle: %v", err)
	}
	if got.ActualAmount != 250 || got.Remark != "ok" {
		t.Fatalf("body = %+v", got)
	}
}

func TestSettle_HoldNotFound(t *testing.T) {
	c := newTestServerWithToken(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "hold not found", http.StatusNotFound)
	}, "")

	err := c.Settle(context.Background(), "missing", 100, "")
	if err != ErrHoldNotFound {
		t.Fatalf("want ErrHoldNotFound, got %v", err)
	}
}

func TestRelease(t *testing.T) {
	hits := 0
	c := newTestServerWithToken(t, func(w http.ResponseWriter, r *http.Request) {
		hits++
		if !strings.HasSuffix(r.URL.Path, "/release") {
			t.Fatalf("wrong path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"hold": map[string]any{"status": "released"}})
	}, "")
	if err := c.Release(context.Background(), "h-1"); err != nil {
		t.Fatalf("release: %v", err)
	}
	if hits != 1 {
		t.Fatalf("hits = %d", hits)
	}
}

// stubLookuper — 测试 fixture 实现 PriceLookuper. 真实链路 (registry.Cache
// + PricingRepo + numeric→millicents 换算) 在 internal/billing/local 包的
// 集成测试里覆盖,这里只测 Client 把请求转发给注入的 Lookuper.
type stubLookuper struct {
	entry *PricingEntry
	err   error
	gotRT string
	gotKK string
}

func (s *stubLookuper) Lookup(_ context.Context, refType, modelCode string) (*PricingEntry, error) {
	s.gotRT = refType
	s.gotKK = modelCode
	return s.entry, s.err
}

func TestLookupPrice_DelegatesToInjectedLookuper(t *testing.T) {
	want := &PricingEntry{
		RefType:           "chat",
		PricingKey:        "claude-haiku-4-5",
		CostBasis:         "per_mtok",
		CostInputPerUnit:  7200,
		CostOutputPerUnit: 36000,
		MarkupRatio:       3.0,
		MinCharge:         1000,
	}
	stub := &stubLookuper{entry: want}
	c := (&Client{}).WithPricing(stub)

	got, err := c.LookupPrice(context.Background(), "chat", "claude-haiku-4-5")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if got.CostInputPerUnit != 7200 || got.MarkupRatio != 3.0 {
		t.Fatalf("entry = %+v", got)
	}
	if stub.gotRT != "chat" || stub.gotKK != "claude-haiku-4-5" {
		t.Fatalf("lookuper got refType=%q key=%q (mismatch)", stub.gotRT, stub.gotKK)
	}
}

func TestLookupPrice_NotFound(t *testing.T) {
	stub := &stubLookuper{err: ErrPricingNotFound}
	c := (&Client{}).WithPricing(stub)
	_, err := c.LookupPrice(context.Background(), "chat", "nope")
	if err != ErrPricingNotFound {
		t.Fatalf("want ErrPricingNotFound, got %v", err)
	}
}

func TestLookupPrice_NoLookuperConfigured(t *testing.T) {
	c := &Client{}
	_, err := c.LookupPrice(context.Background(), "chat", "x")
	if err == nil {
		t.Fatalf("expected error when Pricing is nil")
	}
}

func TestPricingEntry_CalculateChat(t *testing.T) {
	// haiku: 7200/M in, 36000/M out, markup 3.0, min 1000
	e := &PricingEntry{
		CostInputPerUnit:  7200,
		CostOutputPerUnit: 36000,
		MarkupRatio:       3.0,
		MinCharge:         1000,
	}
	// 100k prompt, 10k completion: cost = 720+360 = 1080; list = 3240
	got := e.CalculateChat(100_000, 10_000, 0, 0)
	if got != 3240 {
		t.Fatalf("got %d, want 3240", got)
	}
	// 小请求触发 min: 100 prompt, 0 completion → cost ≈ 0 → list = 1000 (min)
	if got := e.CalculateChat(100, 0, 0, 0); got != 1000 {
		t.Fatalf("got %d, want 1000", got)
	}
}

func TestPricingEntry_EstimateChatRange(t *testing.T) {
	e := &PricingEntry{
		CostInputPerUnit:  7200,
		CostOutputPerUnit: 36000,
		MarkupRatio:       3.0,
		MinCharge:         100,
	}
	minList, maxList := e.EstimateChatRange(100_000, 10_000)
	// minList = cost(100k, 0) * 3 = 720 * 3 = 2160
	// maxList = cost(100k, 10k) * 3 = 1080 * 3 = 3240
	if minList != 2160 || maxList != 3240 {
		t.Fatalf("(min,max) = (%d,%d)", minList, maxList)
	}
}

// ─── M5 多模态计费方法 ──────────────────────────────────────────

func TestPricingEntry_CalculateEmbed(t *testing.T) {
	// bge-m3: 假设 ¥0.4/M token cost (4000 millicents), markup 2.5, min 10
	e := &PricingEntry{
		CostInputPerUnit: 4000,
		MarkupRatio:      2.5,
		MinCharge:        10,
	}
	// 100k tokens: cost = 4000 * 100k / 1M = 400 → list = 1000
	if got := e.CalculateEmbed(100_000); got != 1000 {
		t.Fatalf("100k tokens: got %d, want 1000", got)
	}
	// 1k tokens: cost = 4 → list = 10 (触发 min)
	if got := e.CalculateEmbed(1000); got != 10 {
		t.Fatalf("1k tokens (min clamp): got %d, want 10", got)
	}
}

func TestPricingEntry_CalculateRerank(t *testing.T) {
	// rerank: ¥0.05 / search_unit (500 millicents/unit), markup 2.0, min 50
	e := &PricingEntry{
		CostInputPerUnit: 500,
		MarkupRatio:      2.0,
		MinCharge:        50,
	}
	// 10 units: cost = 5000 → list = 10000
	if got := e.CalculateRerank(10); got != 10000 {
		t.Fatalf("10 units: got %d, want 10000", got)
	}
	// 0 units (异常): cost = 0 → list = 50 (min)
	if got := e.CalculateRerank(0); got != 50 {
		t.Fatalf("0 units (min): got %d, want 50", got)
	}
}

func TestPricingEntry_CalculateSpeech(t *testing.T) {
	// cosyvoice: 假设 ¥1/千字符 (10000 millicents/kchar), markup 2.5, min 100
	e := &PricingEntry{
		CostInputPerUnit: 10000,
		MarkupRatio:      2.5,
		MinCharge:        100,
	}
	// 1000 chars (1 kchar): cost = 10000 * 1000/1000 = 10000 → list = 25000
	if got := e.CalculateSpeech(1000); got != 25000 {
		t.Fatalf("1k chars: got %d, want 25000", got)
	}
	// 50 chars: cost = 500 → list = 1250 (1250 > 100 不触发 min)
	if got := e.CalculateSpeech(50); got != 1250 {
		t.Fatalf("50 chars: got %d, want 1250", got)
	}
	// 1 char: cost = 10 → list = 25 → 触发 min 100
	if got := e.CalculateSpeech(1); got != 100 {
		t.Fatalf("1 char (min clamp): got %d, want 100", got)
	}
}

func TestPricingEntry_CalculateImage(t *testing.T) {
	// wanx-2.6-t2i: ¥0.2/张 cost (2000 millicents), markup 1.5, min 0
	e := &PricingEntry{
		CostInputPerUnit: 2000,
		MarkupRatio:      1.5,
	}
	// 1 张: list = 3000
	if got := e.CalculateImage(1); got != 3000 {
		t.Fatalf("1 image: got %d, want 3000", got)
	}
	// 4 张: list = 12000
	if got := e.CalculateImage(4); got != 12000 {
		t.Fatalf("4 images: got %d, want 12000", got)
	}
	// 0 (兜底当 1 张算)
	if got := e.CalculateImage(0); got != 3000 {
		t.Fatalf("n=0 fallback to 1: got %d, want 3000", got)
	}
}

func TestPricingEntry_CalculateVideo(t *testing.T) {
	// wan2.5-t2v: ¥1.5/秒 cost (15000 millicents), markup 1.5, min 1000
	e := &PricingEntry{
		CostInputPerUnit: 15000,
		MarkupRatio:      1.5,
		MinCharge:        1000,
	}
	// 5 秒: cost = 75000 → list = 112500
	if got := e.CalculateVideo(5); got != 112500 {
		t.Fatalf("5s: got %d, want 112500", got)
	}
	// 0 秒 fallback to 1 秒
	if got := e.CalculateVideo(0); got != 22500 {
		t.Fatalf("0s fallback to 1s: got %d, want 22500", got)
	}
}

func TestPricingEntry_MaxChargeClamp(t *testing.T) {
	maxCap := int64(5000)
	e := &PricingEntry{
		CostInputPerUnit:    100_000_000, // 故意巨大
		MarkupRatio:         1.0,
		MaxChargePerRequest: &maxCap,
	}
	// 任何巨额计算都该被 clamp 到 5000
	if got := e.CalculateEmbed(1_000_000); got != 5000 {
		t.Errorf("max clamp embed: got %d", got)
	}
	if got := e.CalculateImage(100); got != 5000 {
		t.Errorf("max clamp image: got %d", got)
	}
}

func TestClient_AuthHeaderAttached(t *testing.T) {
	c := newTestServerWithToken(t, func(w http.ResponseWriter, r *http.Request) {
		// 进入 handler 说明 token 校验通过
		_ = json.NewEncoder(w).Encode(map[string]any{
			"hold": map[string]any{"id": "x", "max_amount": 1, "status": "held"},
		})
	}, "expected-secret")
	_, err := c.Hold(context.Background(), HoldArgs{UserID: "u", MaxAmount: 1, RefType: "chat_message"})
	if err != nil {
		t.Fatalf("expected token-protected call to succeed: %v", err)
	}
}
