// Provider selection — both the legacy chat-path Provider (for the
// pre-engine REPL fallback + headless --json) and the engine-path
// AnthropicEngineProvider. Same flag/config shape feeds both, so
// they share the mode-resolution logic.

package wiring

import (
	"context"
	"errors"
	"net/http"
	"os"

	"github.com/biumind/biumind/apps/cli/biu/internal/client"
	"github.com/biumind/biumind/apps/cli/biu/internal/client/direct"
	"github.com/biumind/biumind/apps/cli/biu/internal/clierr"
	"github.com/biumind/biumind/apps/cli/biu/internal/config"
	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
	"github.com/biumind/biumind/apps/cli/biu/internal/oauth"
)

// BuildProvider constructs the right client.Provider based on
// config + flags. Returns (provider, mode, err); mode is normalized
// to one of cloud / byo_endpoint / direct so callers can log it
// without re-resolving defaults.
func BuildProvider(cfg *config.Config, f Flags) (client.Provider, string, error) {
	mode := firstNonEmpty(f.Mode, cfg.Default.Mode, string(client.ModeCloud))
	if !client.Mode(mode).IsValid() {
		return nil, "", clierr.WithHint(
			clierr.Newf("config", "invalid mode %q", mode),
			"valid modes: cloud | byo_endpoint | direct — set [default].mode in ~/.biu/config.toml or pass --mode")
	}
	switch client.Mode(mode) {
	case client.ModeDirect:
		// Direct: bypass model-relay. Use [providers.<name>] section.
		providerName := firstNonEmpty(cfg.Default.Provider, "anthropic")
		ps, ok := cfg.Providers[providerName]
		if !ok || ps.APIKey == "" {
			return nil, mode, clierr.WithHint(
				clierr.Newf("config", "mode=direct but [providers.%s].api_key is missing", providerName),
				"add the api_key under [providers."+providerName+"] or run `biu init --mode=direct`")
		}
		switch providerName {
		case "anthropic":
			return direct.NewAnthropic(ps.APIKey, ps.Endpoint), mode, nil
		default:
			return nil, mode, clierr.WithHint(
				clierr.Newf("config", "mode=direct: provider %q not supported", providerName),
				"only `anthropic` is supported in direct mode today; use mode=byo_endpoint via a LiteLLM proxy for other providers")
		}

	default: // cloud / byo_endpoint — both go through model-relay
		relayURL := firstNonEmpty(f.RelayURL, os.Getenv("BIUMIND_MODEL_RELAY_URL"), cfg.Relay.Endpoint)
		if relayURL == "" {
			return nil, mode, clierr.WithHint(
				clierr.Newf("config", "mode=%s but model-relay endpoint not set", mode),
				"set [model-relay].endpoint, pass --model-relay-url, set BIUMIND_MODEL_RELAY_URL, or switch to mode=direct")
		}
		tp := TokenProviderFor(cfg, f, relayURL)
		token, err := tp.Token(context.Background())
		if err != nil {
			// ErrNotLoggedIn / ErrLoginExpired 的 message 本身已是完整
			// 引导文案（方案 D8），不再叠加 hint 避免重复。
			if errors.Is(err, oauth.ErrNotLoggedIn) || errors.Is(err, oauth.ErrLoginExpired) {
				return nil, mode, clierr.Wrapf("config", err, "mode=%s", mode)
			}
			return nil, mode, clierr.WithHint(
				clierr.Wrapf("config", err, "mode=%s", mode),
				"run 'biu auth login' to sign in via browser, set [model-relay].virtual_key, pass --token, set BIUMIND_TOKEN, or switch to mode=direct")
		}
		p := client.NewRelayProvider(relayURL, token)
		AttachOAuthRetry(&p.HTTPClient, tp)
		return p, mode, nil
	}
}

// TokenProviderFor 组装 cloud 模式的 token 解析链（方案 D5）：
// --token > BIUMIND_TOKEN > [model-relay].virtual_key > OAuth store。
// OAuth 端点按 C1 规则推导/合并，供惰性刷新与 401 强刷使用。
func TokenProviderFor(cfg *config.Config, f Flags, relayURL string) *oauth.TokenProvider {
	oc, _ := oauth.ConfigFromSources(oauth.Config{
		AuthorizeURL:      cfg.Auth.AuthorizeURL,
		TokenURL:          cfg.Auth.TokenURL,
		RevokeURL:         cfg.Auth.RevokeURL,
		ClientID:          cfg.Auth.ClientID,
		Scopes:            cfg.Auth.Scopes,
		CallbackPort:      cfg.Auth.CallbackPort,
		ManualRedirectURL: cfg.Auth.ManualRedirectURL,
	}, relayURL)
	return oauth.NewTokenProvider(oauth.ResolveOptions{
		FlagToken:  f.Token,
		EnvToken:   os.Getenv("BIUMIND_TOKEN"),
		VirtualKey: cfg.Relay.VirtualKey,
		Config:     oc,
	})
}

// AttachOAuthRetry 仅在 token 来自 OAuth store 时给 http.Client 包
// 401 强刷重试（方案 D5-3）；静态 token（flag/env/virtual_key）没有
// 刷新语义，保持原样。
func AttachOAuthRetry(hc **http.Client, tp *oauth.TokenProvider) {
	if tp == nil || tp.Source() != oauth.SourceStore {
		return
	}
	if *hc == nil {
		*hc = &http.Client{}
	}
	(*hc).Transport = &client.OAuthRefreshTransport{
		Base:      (*hc).Transport,
		TokenFn:   tp.CachedToken,
		RefreshFn: tp.ForceRefresh,
	}
}

// BuildEngineProvider picks the right engine.Provider for the active
// mode. Returns nil when nothing is configured — callers treat that
// as "engine disabled, fall back to legacy chat path".
func BuildEngineProvider(cfg *config.Config, f Flags) engine.Provider {
	mode := firstNonEmpty(f.Mode, cfg.Default.Mode, string(client.ModeCloud))
	switch client.Mode(mode) {
	case client.ModeDirect:
		providerName := firstNonEmpty(cfg.Default.Provider, "anthropic")
		ps, ok := cfg.Providers[providerName]
		if !ok || ps.APIKey == "" {
			return nil
		}
		switch providerName {
		case "anthropic":
			return client.NewAnthropicEngine(ps.APIKey, ps.Endpoint)
		default:
			// Non-Anthropic providers speak OpenAI-compat
			// (/v1/chat/completions) — same max-common-denominator
			// mapping as model-relay byokAdaptorName (anything not
			// "anthropic" → openai adapter).
			return client.NewOpenAIEngine(ps.APIKey, ps.Endpoint)
		}
	default: // cloud / byo_endpoint — both go through model-relay
		relayURL := firstNonEmpty(f.RelayURL, os.Getenv("BIUMIND_MODEL_RELAY_URL"), cfg.Relay.Endpoint)
		if relayURL == "" {
			return nil
		}
		tp := TokenProviderFor(cfg, f, relayURL)
		token, err := tp.Token(context.Background())
		if err != nil {
			// 未登录 / 登录过期：引擎禁用，legacy chat 路径的
			// BuildProvider 会把引导文案抛给用户。
			return nil
		}
		eng := client.NewRelayEngine(relayURL, token)
		AttachOAuthRetry(&eng.HTTP, tp)
		return eng
	}
}

// firstNonEmpty returns the first non-empty string in args, or "" if
// all are empty. Local copy so wiring stays independent of main's
// helpers (same body, different package).
func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}

// or returns s if non-empty, else fallback. Used in stderr summaries.
func or(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
