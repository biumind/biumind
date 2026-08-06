package usage

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// 解析层是 Claude/Codex 用量唯一可零依赖验证的部分(live 路径要 keychain/网络/codex
// 二进制)。下面覆盖:百分比归一化(小数 vs 整数)、reset 解析(秒/串/RFC3339)、
// Source JSON 形状、codex rateLimits 多级 fallback、Claude HTTP 流(含 429 冷却)。

func TestParsePercentValue_FractionAndInteger(t *testing.T) {
	cases := []struct {
		raw  string
		want int
		ok   bool
	}{
		{`0.45`, 45, true},  // 小数 → ×100
		{`1`, 100, true},    // 边界:1.0 视作 100%
		{`72`, 72, true},    // 已是百分比
		{`"63"`, 63, true},  // 数字串
		{`150`, 100, true},  // 上钳
		{`-5`, 0, true},     // 下钳(负 number ≤1 → ×100 = -500 → 0)
		{`"abc"`, 0, false}, // 非法串
		{`null`, 0, false},  // null
		{``, 0, false},      // 缺字段
	}
	for _, c := range cases {
		got, ok := parsePercentValue(json.RawMessage(c.raw))
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("parsePercentValue(%s) = (%d,%v), want (%d,%v)", c.raw, got, ok, c.want, c.ok)
		}
	}
}

func TestParseResetValue(t *testing.T) {
	rfc := "2026-06-27T12:00:00Z"
	wantRFC, _ := time.Parse(time.RFC3339, rfc)
	cases := []struct {
		raw  string
		want *int64
	}{
		{`1719475200`, ptrI64(1719475200)},
		{`"1719475200"`, ptrI64(1719475200)},
		{`"` + rfc + `"`, ptrI64(wantRFC.Unix())},
		{`null`, nil},
		{``, nil},
		{`"not-a-time"`, nil},
	}
	for _, c := range cases {
		got := parseResetValue(json.RawMessage(c.raw))
		if (got == nil) != (c.want == nil) || (got != nil && *got != *c.want) {
			t.Errorf("parseResetValue(%s) = %v, want %v", c.raw, deref(got), deref(c.want))
		}
	}
}

func TestParseClaudeWindow(t *testing.T) {
	w := parseClaudeWindow(json.RawMessage(`{"utilization":0.4,"resets_at":1719475200}`))
	if w == nil {
		t.Fatal("expected window")
	}
	if w.UsedPercent != 40 || w.RemainingPercent != 60 {
		t.Errorf("used=%d remaining=%d want 40/60", w.UsedPercent, w.RemainingPercent)
	}
	if w.ResetAt == nil || *w.ResetAt != 1719475200 {
		t.Errorf("resetAt=%v", deref(w.ResetAt))
	}
	// 缺 utilization → nil
	if parseClaudeWindow(json.RawMessage(`{"resets_at":1}`)) != nil {
		t.Error("expected nil window when utilization missing")
	}
}

func TestParseCodexWindow_IntegerPercent(t *testing.T) {
	// Codex 给的是 0–100 整数,不是小数 —— 30 必须保持 30,不是 3000。
	w := parseCodexWindow(json.RawMessage(`{"usedPercent":30,"resetsAt":"2026-06-27T12:00:00Z"}`))
	if w == nil {
		t.Fatal("expected window")
	}
	if w.UsedPercent != 30 || w.RemainingPercent != 70 {
		t.Errorf("used=%d remaining=%d want 30/70", w.UsedPercent, w.RemainingPercent)
	}
	if w.ResetAt == nil {
		t.Error("expected resetAt parsed from RFC3339")
	}
}

func TestParseCodexUsage_NestedFallback(t *testing.T) {
	account := json.RawMessage(`{"account":{"email":"u@example.com","planType":"Pro"}}`)
	// rateLimitsByLimitId.codex 优先
	rl := json.RawMessage(`{"rateLimitsByLimitId":{"codex":{"primary":{"usedPercent":10},"secondary":{"usedPercent":20}}}}`)
	d := parseCodexUsage(account, rl)
	if d.Email == nil || *d.Email != "u@example.com" {
		t.Errorf("email=%v", deref2(d.Email))
	}
	if d.PlanType == nil || *d.PlanType != "Pro" {
		t.Errorf("planType=%v", deref2(d.PlanType))
	}
	if d.Primary == nil || d.Primary.UsedPercent != 10 {
		t.Errorf("primary=%v", d.Primary)
	}
	if d.Secondary == nil || d.Secondary.UsedPercent != 20 {
		t.Errorf("secondary=%v", d.Secondary)
	}

	// 顶层 rateLimits fallback(无 byLimitId)
	rl2 := json.RawMessage(`{"rateLimits":{"primary":{"usedPercent":5}}}`)
	d2 := parseCodexUsage(account, rl2)
	if d2.Primary == nil || d2.Primary.UsedPercent != 5 {
		t.Errorf("fallback primary=%v", d2.Primary)
	}
}

func TestSourceJSON_Shape(t *testing.T) {
	// available → {status:"available",data:{...}}
	b, _ := json.Marshal(available(ClaudeData{FiveHour: &Window{UsedPercent: 1, RemainingPercent: 99}}))
	var av struct {
		Status string `json:"status"`
		Data   struct {
			FiveHour *Window `json:"fiveHour"`
		} `json:"data"`
	}
	if err := json.Unmarshal(b, &av); err != nil {
		t.Fatal(err)
	}
	if av.Status != "available" || av.Data.FiveHour == nil || av.Data.FiveHour.UsedPercent != 1 {
		t.Errorf("available shape wrong: %s", b)
	}
	// unavailable → {status:"unavailable",reason:"..."}
	b2, _ := json.Marshal(unavailable("nope"))
	var un struct {
		Status string `json:"status"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(b2, &un); err != nil {
		t.Fatal(err)
	}
	if un.Status != "unavailable" || un.Reason != "nope" {
		t.Errorf("unavailable shape wrong: %s", b2)
	}
}

func TestReadClaude_429Backoff(t *testing.T) {
	// 重置全局冷却态(其他测试可能动过)。
	claude429Mu.Lock()
	claude429Until = time.Time{}
	claude429Mu.Unlock()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	// 注入桩:token 取得 + URL 指向桩服务器。
	restore := stubClaude(srv.URL, "tok")
	defer restore()

	src := readClaude(testCtx(t))
	if src.ok {
		t.Fatal("expected unavailable on 429")
	}
	// 冷却应被置位 → 下一次直接短路,不打 HTTP。
	claude429Mu.Lock()
	set := !claude429Until.IsZero()
	claude429Mu.Unlock()
	if !set {
		t.Error("expected 429 cooldown to be armed")
	}

	src2 := readClaude(testCtx(t))
	if src2.ok {
		t.Fatal("expected unavailable while cooling down")
	}
}

func TestReadClaude_Success(t *testing.T) {
	claude429Mu.Lock()
	claude429Until = time.Time{}
	claude429Mu.Unlock()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("anthropic-beta"); got != claudeBetaHeader {
			t.Errorf("missing anthropic-beta header: %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			t.Errorf("bad auth header: %q", got)
		}
		_, _ = w.Write([]byte(`{"five_hour":{"utilization":0.5,"resets_at":100},"seven_day":{"utilization":0.9}}`))
	}))
	defer srv.Close()
	restore := stubClaude(srv.URL, "tok")
	defer restore()

	src := readClaude(testCtx(t))
	if !src.ok {
		t.Fatalf("expected available, got reason=%q", src.reason)
	}
	data, ok := src.data.(ClaudeData)
	if !ok {
		t.Fatalf("data type %T", src.data)
	}
	if data.FiveHour == nil || data.FiveHour.UsedPercent != 50 {
		t.Errorf("five_hour=%v", data.FiveHour)
	}
	if data.SevenDay == nil || data.SevenDay.UsedPercent != 90 {
		t.Errorf("seven_day=%v", data.SevenDay)
	}
}

// ─── test helpers ────────────────────────────────────────────────────────

// stubClaude 暂时把 Claude 取 token 与 URL 换成桩,返回还原函数。
func stubClaude(url, token string) func() {
	origURL := claudeUsageURLVar
	origTok := claudeTokenFn
	claudeUsageURLVar = url
	claudeTokenFn = func(_ context.Context) (string, error) { return token, nil }
	return func() {
		claudeUsageURLVar = origURL
		claudeTokenFn = origTok
	}
}

func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func ptrI64(v int64) *int64 { return &v }
func deref(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}
func deref2(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}
