// Provider selection — both the legacy chat-path Provider (for the
// pre-engine REPL fallback + headless --json) and the engine-path
// AnthropicEngineProvider. Same flag/config shape feeds both, so
// they share the mode-resolution logic.

package wiring

import (
	"os"

	"github.com/biumind/biumind/apps/cli/biu/internal/client"
	"github.com/biumind/biumind/apps/cli/biu/internal/client/direct"
	"github.com/biumind/biumind/apps/cli/biu/internal/clierr"
	"github.com/biumind/biumind/apps/cli/biu/internal/config"
	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
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
		token := firstNonEmpty(f.Token, os.Getenv("BIUMIND_TOKEN"), cfg.Relay.VirtualKey)
		if relayURL == "" {
			return nil, mode, clierr.WithHint(
				clierr.Newf("config", "mode=%s but model-relay endpoint not set", mode),
				"set [model-relay].endpoint, pass --model-relay-url, set BIUMIND_MODEL_RELAY_URL, or switch to mode=direct")
		}
		if token == "" {
			return nil, mode, clierr.WithHint(
				clierr.Newf("config", "mode=%s but auth token not set", mode),
				"set [model-relay].virtual_key, pass --token, set BIUMIND_TOKEN, or switch to mode=direct")
		}
		return client.NewRelayProvider(relayURL, token), mode, nil
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
		token := firstNonEmpty(f.Token, os.Getenv("BIUMIND_TOKEN"), cfg.Relay.VirtualKey)
		if relayURL == "" || token == "" {
			return nil
		}
		return client.NewRelayEngine(relayURL, token)
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
