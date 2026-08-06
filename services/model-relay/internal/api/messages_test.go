package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/biumind/biumind/packages/go-sdk/biu/quota"
	"github.com/biumind/biumind/services/model-relay/internal/relay/provider"
	"github.com/biumind/biumind/services/model-relay/internal/relay/provider/openai"
	"github.com/biumind/biumind/services/model-relay/internal/router"
)

func TestReportUsage_ChargesTPMBucket(t *testing.T) {
	l := quota.NewInMemoryLimiter(map[string]quota.Spec{
		"hub.tpm": {Window: time.Minute, Limit: 1000, Unit: "tokens"},
	})
	h := &MessagesHandler{Limiter: l}

	r := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	r = r.WithContext(bauth.WithClaims(r.Context(), &bauth.Claims{UserID: "u1"}))

	h.reportUsage(r, provider.Usage{
		PromptTokens:     100,
		CompletionTokens: 50,
		CacheReadTokens:  10,
		CacheWriteTokens: 5,
	}, "claude-haiku-4-5", "anthropic", time.Now(), true)

	d := l.Snapshot("hub.tpm", "u1")
	want := int64(1000 - (100 + 50 + 10 + 5))
	if d.Remaining != want {
		t.Errorf("remaining tokens: got %d, want %d", d.Remaining, want)
	}
}

func TestReportUsage_NilLimiterIsNoop(t *testing.T) {
	h := &MessagesHandler{Limiter: nil}
	r := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	r = r.WithContext(bauth.WithClaims(r.Context(), &bauth.Claims{UserID: "u1"}))
	// must not panic
	h.reportUsage(r, provider.Usage{PromptTokens: 100}, "claude-haiku-4-5", "anthropic", time.Now(), true)
}

func TestReportUsage_NoClaimsIsNoop(t *testing.T) {
	l := quota.NewInMemoryLimiter(map[string]quota.Spec{
		"hub.tpm": {Window: time.Minute, Limit: 100},
	})
	h := &MessagesHandler{Limiter: l}
	r := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	// no WithClaims — handler must not panic, must not charge.
	h.reportUsage(r, provider.Usage{PromptTokens: 50}, "claude-haiku-4-5", "anthropic", time.Now(), true)

	d := l.Snapshot("hub.tpm", "")
	if d.Remaining != 100 {
		t.Errorf("anonymous request charged the bucket: remaining=%d", d.Remaining)
	}
}

func TestReportUsage_ZeroUsageIsNoop(t *testing.T) {
	l := quota.NewInMemoryLimiter(map[string]quota.Spec{
		"hub.tpm": {Window: time.Minute, Limit: 100},
	})
	h := &MessagesHandler{Limiter: l}
	r := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	r = r.WithContext(bauth.WithClaims(r.Context(), &bauth.Claims{UserID: "u1"}))
	h.reportUsage(r, provider.Usage{}, "claude-haiku-4-5", "anthropic", time.Now(), true) // all zeros

	d := l.Snapshot("hub.tpm", "u1")
	if d.Remaining != 100 {
		t.Errorf("zero-usage request mutated bucket: remaining=%d", d.Remaining)
	}
}

// TestReportUsage_OverBudgetDoesNotErrorThisRequest ensures post-hoc
// accounting never fails the in-flight response — the future-call
// gate handles enforcement, not this one.
func TestReportUsage_OverBudgetDoesNotErrorThisRequest(t *testing.T) {
	l := quota.NewInMemoryLimiter(map[string]quota.Spec{
		"hub.tpm": {Window: time.Minute, Limit: 50},
	})
	h := &MessagesHandler{Limiter: l}
	r := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	r = r.WithContext(bauth.WithClaims(r.Context(), &bauth.Claims{UserID: "u1"}))

	// 200 tokens > 50 limit. reportUsage MUST NOT panic / error.
	h.reportUsage(r, provider.Usage{PromptTokens: 200}, "claude-haiku-4-5", "anthropic", time.Now(), true)

	// Limiter rejected the over-reservation, so bucket is untouched
	// and Remaining == limit. The next request will still be denied
	// because the bucket count + any new charge will exceed 50 — but
	// that's checked at the middleware layer, not here.
	d := l.Snapshot("hub.tpm", "u1")
	if d.Remaining < 0 {
		t.Errorf("limiter went negative: %d", d.Remaining)
	}
}

// ─── context helper sanity check ────────────────────────

func TestClaimsFromContext_Roundtrip(t *testing.T) {
	ctx := bauth.WithClaims(context.Background(), &bauth.Claims{UserID: "u-test"})
	c, ok := bauth.ClaimsFrom(ctx)
	if !ok || c.UserID != "u-test" {
		t.Errorf("claims roundtrip: ok=%v user=%q", ok, c.UserID)
	}
}

// ─── planFromClaims ─────────────────────────────────────

func TestPlanFromClaims_PrefersClaimsPlan(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/x", nil)
	r = r.WithContext(bauth.WithClaims(r.Context(),
		&bauth.Claims{UserID: "u1", Plan: "pro", Roles: []string{"user"}}))
	if got := planFromClaims(r); got != "pro" {
		t.Errorf("got %q, want pro", got)
	}
}

func TestPlanFromClaims_FallbackRoleToAdmin(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/x", nil)
	r = r.WithContext(bauth.WithClaims(r.Context(),
		&bauth.Claims{UserID: "u1", Roles: []string{"ops"}})) // 老 token 没 plan
	if got := planFromClaims(r); got != "admin" {
		t.Errorf("got %q, want admin (role fallback)", got)
	}
}

func TestPlanFromClaims_FallbackEmptyToFree(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/x", nil)
	r = r.WithContext(bauth.WithClaims(r.Context(),
		&bauth.Claims{UserID: "u1"})) // 既无 plan 又无 role
	if got := planFromClaims(r); got != "free" {
		t.Errorf("got %q, want free", got)
	}
}

func TestPlanFromClaims_NoClaimsToFree(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/x", nil) // ctx 没 claims
	if got := planFromClaims(r); got != "free" {
		t.Errorf("got %q, want free", got)
	}
}

// ─── 回归: 发上游的 model 用 channel.upstream_model, 不是 client 的 code ──
//
// 复现并锁定 DeepSeek-V4-Pro bug: client 提交 code=deepseek-v4-pro, admin
// 给 channel 配 upstream_model=DeepSeek-V4-Pro。修复前 chat 路径把 code 原样
// 发上游被拒 "Invalid model name"; 各 modality handler 早已用 out.UpstreamModel,
// chat 漏了。这里断言上游收到的 model 是替换后的 upstream_model。
func TestMessages_UsesUpstreamModelNotCode(t *testing.T) {
	var gotModel string
	upstream := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				Model string `json:"model"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			gotModel = body.Model
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"x","model":"DeepSeek-V4-Pro",` +
				`"choices":[{"index":0,"message":{"role":"assistant",` +
				`"content":"hi"},"finish_reason":"stop"}],` +
				`"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
		}))
	defer upstream.Close()

	reg := provider.NewRegistry()
	reg.Register(openai.New())

	h := &MessagesHandler{
		Registry:   reg,
		HTTPClient: http.DefaultClient,
		// CredsResolver 模拟 buildCredsResolver: stamp ResolveOutput(含
		// UpstreamModel) 到 ctx, 正是漏掉的那一环。
		CredsResolver: func(r *http.Request, modelName string) (string, *provider.Credentials, *http.Request, error) {
			scoped := r.WithContext(router.WithResolveOutput(r.Context(),
				&router.ResolveOutput{UpstreamModel: "DeepSeek-V4-Pro"}))
			return "openai", &provider.Credentials{APIKey: "k", BaseURL: upstream.URL}, scoped, nil
		},
	}

	reqBody := `{"model":"deepseek-v4-pro","messages":[{"role":"user",` +
		`"content":"hi"}],"max_tokens":16}`
	r := httptest.NewRequest(http.MethodPost, "/v1/messages",
		strings.NewReader(reqBody))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	if gotModel != "DeepSeek-V4-Pro" {
		t.Errorf("upstream got model %q, want %q (channel.upstream_model)",
			gotModel, "DeepSeek-V4-Pro")
	}
}

// 无 ResolveOutput (如 BYOK fast-path) → 保持 client 提交的 model code。
func TestMessages_NoResolveOutputKeepsCode(t *testing.T) {
	var gotModel string
	upstream := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				Model string `json:"model"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			gotModel = body.Model
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"x","choices":[{"index":0,` +
				`"message":{"role":"assistant","content":"hi"},` +
				`"finish_reason":"stop"}],"usage":{"prompt_tokens":1,` +
				`"completion_tokens":1,"total_tokens":2}}`))
		}))
	defer upstream.Close()

	reg := provider.NewRegistry()
	reg.Register(openai.New())

	h := &MessagesHandler{
		Registry:   reg,
		HTTPClient: http.DefaultClient,
		CredsResolver: func(r *http.Request, modelName string) (string, *provider.Credentials, *http.Request, error) {
			// 不 stamp ResolveOutput (BYOK 路径形态)。
			return "openai", &provider.Credentials{APIKey: "k", BaseURL: upstream.URL}, nil, nil
		},
	}

	reqBody := `{"model":"my-byok-model","messages":[{"role":"user",` +
		`"content":"hi"}],"max_tokens":16}`
	r := httptest.NewRequest(http.MethodPost, "/v1/messages",
		strings.NewReader(reqBody))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	if gotModel != "my-byok-model" {
		t.Errorf("upstream got model %q, want %q (unchanged code)",
			gotModel, "my-byok-model")
	}
}
