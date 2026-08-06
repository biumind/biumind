package agentplane

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"

	providerspkg "github.com/biumind/biumind/services/brain/internal/chat/providers"
)

// fakeProviderResolver 实现 agentplane.ProviderResolver 接口,让我们在不
// 起真 Postgres 的前提下覆盖 ChatRunner.resolveCreds 的所有分支。
type fakeProviderResolver struct {
	provider *providerspkg.Provider
	err      error
	// 计数:确保只在 providerID 非空时被调用,空时直接走 fast path 不查。
	calls int
}

func (f *fakeProviderResolver) GetByProviderID(
	_ context.Context, _ uuid.UUID, _ string,
) (*providerspkg.Provider, error) {
	f.calls++
	return f.provider, f.err
}

// newRunnerForCreds wires a ChatRunner with platform creds + the given
// resolver (provider metadata) + keyResolver (identity key, P3). Either
// may be nil to exercise fallback paths.
func newRunnerForCreds(resolver ProviderResolver, keyResolver BYOKKeyResolver) *ChatRunner {
	return &ChatRunner{
		AnthropicAPIKey:   "platform-key",
		AnthropicEndpoint: "https://api.platform.example",
		ProvidersStore:    resolver,
		KeyResolver:       keyResolver,
		Logger:            slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

// 一个常驻 keyResolver fake,返固定 key (无 base_url);BYOK-命中测试复用。
func keyRes(apiKey string) *fakeKeyResolver {
	return &fakeKeyResolver{key: &providerspkg.IdentityBYOKKey{APIKey: apiKey}}
}

// 空 providerID 直接走 fast path,不调用 resolver。
func TestResolveCreds_NoProviderID_PlatformFallback(t *testing.T) {
	f := &fakeProviderResolver{}
	r := newRunnerForCreds(f, keyRes("sk"))
	_, e, _ := r.resolveCreds(context.Background(), uuid.New(), "", "")
	// platform endpoint retained; resolver untouched.
	if f.calls != 0 {
		t.Errorf("resolver should not be called when providerID empty; got %d calls", f.calls)
	}
	if e != "https://api.platform.example" {
		t.Fatalf("expect platform endpoint; got %q", e)
	}
}

// resolver nil 也不查表,直接 fallback。
func TestResolveCreds_NilResolver_PlatformFallback(t *testing.T) {
	r := newRunnerForCreds(nil, keyRes("sk"))
	k, e, _ := r.resolveCreds(context.Background(), uuid.New(), "anthropic", "")
	if k != "platform-key" || e != "https://api.platform.example" {
		t.Fatalf("expect platform creds with nil resolver; got (%q,%q)", k, e)
	}
}

// provider 不存在(被删 / 跨用户)→ 平台 fallback,不抛错。
func TestResolveCreds_ProviderNotFound_PlatformFallback(t *testing.T) {
	f := &fakeProviderResolver{err: providerspkg.ErrNotFound}
	r := newRunnerForCreds(f, keyRes("sk"))
	k, e, _ := r.resolveCreds(context.Background(), uuid.New(), "anthropic", "")
	if k != "platform-key" || e != "https://api.platform.example" {
		t.Fatalf("expect platform creds on not-found; got (%q,%q)", k, e)
	}
}

// resolver 内部错(DB down)→ 平台 fallback + log warn,不阻塞 turn。
func TestResolveCreds_ResolverError_PlatformFallback(t *testing.T) {
	f := &fakeProviderResolver{err: errors.New("db connection refused")}
	r := newRunnerForCreds(f, keyRes("sk"))
	k, e, _ := r.resolveCreds(context.Background(), uuid.New(), "anthropic", "")
	if k != "platform-key" || e != "https://api.platform.example" {
		t.Fatalf("expect platform creds on DB err; got (%q,%q)", k, e)
	}
}

// official provider 走平台池,即使有 key 也用平台 key (BiuMind Cloud 走订阅)。
func TestResolveCreds_OfficialProvider_PlatformFallback(t *testing.T) {
	f := &fakeProviderResolver{
		provider: &providerspkg.Provider{
			Source:  providerspkg.SourceOfficial,
			Enabled: true,
		},
	}
	r := newRunnerForCreds(f, keyRes("sk"))
	k, e, _ := r.resolveCreds(context.Background(), uuid.New(), "biumind-official", "")
	if k != "platform-key" || e != "https://api.platform.example" {
		t.Fatalf("official should fall back to platform creds; got (%q,%q)", k, e)
	}
}

// 用户禁用了 provider → 平台 fallback。
func TestResolveCreds_DisabledProvider_PlatformFallback(t *testing.T) {
	f := &fakeProviderResolver{
		provider: &providerspkg.Provider{
			Source:  "builtin",
			Enabled: false, // ← 禁用
		},
	}
	r := newRunnerForCreds(f, keyRes("sk"))
	k, e, _ := r.resolveCreds(context.Background(), uuid.New(), "anthropic", "")
	if k != "platform-key" || e != "https://api.platform.example" {
		t.Fatalf("disabled provider should fall back; got (%q,%q)", k, e)
	}
}

// keyResolver nil (dev / 未配 IDENTITY_URL) → 平台 fallback。
func TestResolveCreds_NilKeyResolver_PlatformFallback(t *testing.T) {
	f := &fakeProviderResolver{
		provider: &providerspkg.Provider{Source: "builtin", Enabled: true},
	}
	r := newRunnerForCreds(f, nil)
	k, e, _ := r.resolveCreds(context.Background(), uuid.New(), "anthropic", "")
	if k != "platform-key" || e != "https://api.platform.example" {
		t.Fatalf("nil keyResolver should fall back; got (%q,%q)", k, e)
	}
}

// identity 无 key (未配 / status!=valid) → 平台 fallback。
func TestResolveCreds_NoIdentityKey_PlatformFallback(t *testing.T) {
	f := &fakeProviderResolver{
		provider: &providerspkg.Provider{Source: "builtin", Enabled: true},
	}
	r := newRunnerForCreds(f, &fakeKeyResolver{err: providerspkg.ErrIdentityKeyNotFound})
	k, e, _ := r.resolveCreds(context.Background(), uuid.New(), "anthropic", "")
	if k != "platform-key" || e != "https://api.platform.example" {
		t.Fatalf("no identity key should fall back; got (%q,%q)", k, e)
	}
}

// 完整 BYOK happy path:identity key + base_url → 替换 endpoint;APIKey 替换。
func TestResolveCreds_BYOKWithBaseURL_OverridesBoth(t *testing.T) {
	url := "https://api.byok.example/v1"
	f := &fakeProviderResolver{
		provider: &providerspkg.Provider{Source: "builtin", Enabled: true},
	}
	r := newRunnerForCreds(f, &fakeKeyResolver{key: &providerspkg.IdentityBYOKKey{
		APIKey: "sk-byok-real", BaseURL: url,
	}})
	k, e, _ := r.resolveCreds(context.Background(), uuid.New(), "anthropic", "")
	if k != "sk-byok-real" {
		t.Errorf("APIKey should be replaced with BYOK key; got %q", k)
	}
	if e != url {
		t.Errorf("Endpoint should be replaced with BYOK base_url; got %q", e)
	}
}

// BYOK 没自定义 base_url → 用平台 endpoint,只换 key。
func TestResolveCreds_BYOKWithoutBaseURL_KeepsPlatformEndpoint(t *testing.T) {
	f := &fakeProviderResolver{
		provider: &providerspkg.Provider{Source: "builtin", Enabled: true},
	}
	r := newRunnerForCreds(f, keyRes("sk-byok-real"))
	k, e, _ := r.resolveCreds(context.Background(), uuid.New(), "anthropic", "")
	if k != "sk-byok-real" {
		t.Errorf("APIKey should be replaced with BYOK key; got %q", k)
	}
	if e != "https://api.platform.example" {
		t.Errorf("Endpoint should remain platform when BYOK has no base_url; got %q", e)
	}
}

// ── PassThrough 路径测试 ─────────────────────────────

// RelayURL 设 + UserBearer 非空 + 非 BYOK → 走 PassThrough,用 user JWT
// 当 APIKey, model-relay URL 当 endpoint。这是生产 BiuMind Cloud 路径。
func TestResolveCreds_PassThrough_UsesBearerAndRelay(t *testing.T) {
	r := &ChatRunner{
		AnthropicAPIKey:   "platform-legacy-key",
		AnthropicEndpoint: "https://api.legacy.example",
		RelayURL:          "http://model-relay:7001",
		ProvidersStore:    nil,
		Logger:            nopLogger(),
	}
	k, e, _ := r.resolveCreds(context.Background(), uuid.New(),
		"biumind-official", "user-jwt-xyz")
	if k != "user-jwt-xyz" {
		t.Errorf("APIKey should be user JWT; got %q", k)
	}
	if e != "http://model-relay:7001" {
		t.Errorf("Endpoint should be RelayURL; got %q", e)
	}
}

// RelayURL 设但 UserBearer 空 → 走 legacy direct fallback (不抛错)。
func TestResolveCreds_PassThroughEmptyBearer_FallsBackLegacy(t *testing.T) {
	r := &ChatRunner{
		AnthropicAPIKey:   "platform-legacy-key",
		AnthropicEndpoint: "https://api.legacy.example",
		RelayURL:          "http://model-relay:7001",
		ProvidersStore:    nil,
		Logger:            nopLogger(),
	}
	k, e, _ := r.resolveCreds(context.Background(), uuid.New(),
		"biumind-official", "")
	if k != "platform-legacy-key" {
		t.Errorf("Empty bearer should fall back; got %q", k)
	}
	if e != "https://api.legacy.example" {
		t.Errorf("Empty bearer should fall back; got %q", e)
	}
}

// BYOK 命中时优先级高于 PassThrough — RelayURL 设了, BYOK 自己的 key +
// base_url 仍然胜出。
func TestResolveCreds_BYOKWinsOverPassThrough(t *testing.T) {
	url := "https://api.byok.example/v1"
	f := &fakeProviderResolver{
		provider: &providerspkg.Provider{Source: "builtin", Enabled: true},
	}
	r := &ChatRunner{
		AnthropicAPIKey:   "platform-legacy-key",
		AnthropicEndpoint: "https://api.legacy.example",
		RelayURL:          "http://model-relay:7001",
		ProvidersStore:    f,
		KeyResolver:       &fakeKeyResolver{key: &providerspkg.IdentityBYOKKey{APIKey: "sk-byok-real", BaseURL: url}},
		Logger:            nopLogger(),
	}
	k, e, _ := r.resolveCreds(context.Background(), uuid.New(),
		"anthropic", "user-jwt-xyz")
	if k != "sk-byok-real" {
		t.Errorf("BYOK should win; got APIKey %q", k)
	}
	if e != url {
		t.Errorf("BYOK should win; got Endpoint %q", e)
	}
}

// BYOK 命中但没填 base_url + RelayURL 配了 → BYOK key + RelayURL。
func TestResolveCreds_BYOKNoBase_UsesRelayURL(t *testing.T) {
	f := &fakeProviderResolver{
		provider: &providerspkg.Provider{Source: "builtin", Enabled: true},
	}
	r := &ChatRunner{
		AnthropicAPIKey:   "platform-legacy-key",
		AnthropicEndpoint: "https://api.legacy.example",
		RelayURL:          "http://model-relay:7001",
		ProvidersStore:    f,
		KeyResolver:       keyRes("sk-byok-real"),
		Logger:            nopLogger(),
	}
	k, e, _ := r.resolveCreds(context.Background(), uuid.New(),
		"anthropic", "user-jwt-xyz")
	if k != "sk-byok-real" {
		t.Errorf("APIKey=%q want sk-byok-real", k)
	}
	if e != "http://model-relay:7001" {
		t.Errorf("Endpoint should fall to RelayURL; got %q", e)
	}
}

// useBearer 信号:三条路径必须返回正确的 header 形态。
func TestResolveCreds_UseBearerSignal(t *testing.T) {
	url := "https://api.byok.example/v1"
	byok := &fakeProviderResolver{
		provider: &providerspkg.Provider{Source: "builtin", Enabled: true},
	}
	cases := []struct {
		name     string
		runner   *ChatRunner
		provID   string
		userJWT  string
		wantBear bool
	}{
		{
			name: "BYOK uses x-api-key (Anthropic native)",
			runner: &ChatRunner{
				AnthropicAPIKey: "legacy", AnthropicEndpoint: "https://legacy",
				RelayURL: "http://model-relay:7001", ProvidersStore: byok,
				KeyResolver: &fakeKeyResolver{key: &providerspkg.IdentityBYOKKey{APIKey: "sk-ant-byok", BaseURL: url}},
				Logger:      nopLogger(),
			},
			provID: "anthropic", userJWT: "user-jwt", wantBear: false,
		},
		{
			name: "PassThrough uses Bearer (model-relay PassThrough)",
			runner: &ChatRunner{
				AnthropicAPIKey: "legacy", AnthropicEndpoint: "https://legacy",
				RelayURL: "http://model-relay:7001", ProvidersStore: nil,
				Logger: nopLogger(),
			},
			provID: "biumind-official", userJWT: "user-jwt", wantBear: true,
		},
		{
			name: "Legacy direct uses x-api-key",
			runner: &ChatRunner{
				AnthropicAPIKey: "legacy", AnthropicEndpoint: "https://legacy",
				RelayURL: "", ProvidersStore: nil, Logger: nopLogger(),
			},
			provID: "biumind-official", userJWT: "", wantBear: false,
		},
		{
			name: "PassThrough degraded (empty bearer) → legacy x-api-key",
			runner: &ChatRunner{
				AnthropicAPIKey: "legacy", AnthropicEndpoint: "https://legacy",
				RelayURL: "http://model-relay:7001", ProvidersStore: nil,
				Logger: nopLogger(),
			},
			provID: "biumind-official", userJWT: "", wantBear: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, gotBear := c.runner.resolveCreds(
				context.Background(), uuid.New(), c.provID, c.userJWT)
			if gotBear != c.wantBear {
				t.Errorf("useBearer=%v want %v", gotBear, c.wantBear)
			}
		})
	}
}

// bearerFromAuthHeader 单独测,直接保护 createChatSession 入口的 token
// 抽取逻辑。"Bearer " 前缀大小写敏感(JWT 客户端规范都首字母大写)。
func TestBearerFromAuthHeader(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"Bearer ", ""},
		{"Bearer abc", "abc"},
		{"Bearer eyJhb.xyz.zzz", "eyJhb.xyz.zzz"},
		{"bearer abc", ""},
		{"Token abc", ""},
		{"abc", ""},
	}
	for _, c := range cases {
		if got := bearerFromAuthHeader(c.in); got != c.want {
			t.Errorf("bearerFromAuthHeader(%q)=%q want %q", c.in, got, c.want)
		}
	}
}
