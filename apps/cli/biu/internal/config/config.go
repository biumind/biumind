// Package config loads ~/.biu/config.toml with sensible defaults.
//
// Lookup order:
//
//  1. flag override (--config <path>)
//  2. $BIU_CONFIG env var
//  3. ~/.biu/config.toml
//  4. defaults (no file required)
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	toml "github.com/pelletier/go-toml/v2"
)

type Config struct {
	Default     DefaultSection             `toml:"default"`
	Relay       HubSection                 `toml:"model-relay"`
	Providers   map[string]ProviderSection `toml:"providers"`
	Permissions PermissionsSection         `toml:"permissions"`
	Search      SearchSection              `toml:"search"`
	MCPServers  []MCPServerSection         `toml:"mcp_servers"`
	Auth        AuthSection                `toml:"auth"`
}

// AuthSection configures OAuth (PKCE) for `biu auth login`. Provider-
// neutral by design: drop in BiuMind model-relay URLs by default, swap for
// Anthropic platform.claude.com via env or local override.
type AuthSection struct {
	AuthorizeURL      string   `toml:"authorize_url"`
	TokenURL          string   `toml:"token_url"`
	ClientID          string   `toml:"client_id"`
	Scopes            []string `toml:"scopes"`
	CallbackPort      int      `toml:"callback_port"`
	ManualRedirectURL string   `toml:"manual_redirect"`
}

// MCPServerSection — one Model Context Protocol server biu launches
// at startup. The tools it exposes are merged into the local tool
// registry under namespaced names (mcp__<name>__<tool>).
//
// Example ~/.biu/config.toml:
//
//	[[mcp_servers]]
//	name = "filesystem"
//	command = "npx"
//	args = ["-y", "@modelcontextprotocol/server-filesystem", "/tmp"]
//
//	[[mcp_servers]]
//	name = "github"
//	command = "docker"
//	args = ["run", "-i", "--rm", "-e", "GITHUB_TOKEN",
//	        "ghcr.io/github/github-mcp-server"]
//	env = { GITHUB_TOKEN = "ghp_…" }
type MCPServerSection struct {
	Name     string `toml:"name"`
	Disabled bool   `toml:"disabled"`

	// Transport selects the wire protocol. Empty / "stdio" → spawn a
	// subprocess via Command + Args + Env (the original biu behaviour).
	// "http" → connect to a Streamable HTTP MCP endpoint at URL with
	// optional Headers (typically Authorization for hosted services).
	// Mixed-transport configs are supported — biu dispatches per row.
	Transport string `toml:"transport"`

	// stdio fields
	Command string            `toml:"command"`
	Args    []string          `toml:"args"`
	Env     map[string]string `toml:"env"`
	Cwd     string            `toml:"cwd"`

	// http fields
	URL     string            `toml:"url"`
	Headers map[string]string `toml:"headers"`

	// OAuth (P20.49b) configures automatic PKCE flow for HTTP MCP
	// servers that respond with 401 + Bearer challenge. Without this
	// block, biu falls back to the phase-1 stub: it tells the user
	// to obtain a token manually and paste it into Headers.
	//
	//   [[mcp_servers]]
	//   name = "github"
	//   transport = "http"
	//   url = "https://api.githubcopilot.com/mcp/"
	//
	//     [mcp_servers.oauth]
	//     client_id     = "Iv1.b507a08c87ecfe98"
	//     authorize_url = "https://github.com/login/oauth/authorize"
	//     token_url     = "https://github.com/login/oauth/access_token"
	//     scopes        = ["read:user", "repo"]
	//     callback_port = 0   # 0 = ephemeral; pin if your provider requires it
	OAuth *MCPOAuthSection `toml:"oauth,omitempty"`

	// DeferTools opts this server's tools into the ToolSearch deferred
	// catalog (P20.51 Phase 2). Default false: every tool's full
	// JSONSchema lands in the system prompt up front. Set true for
	// servers with large tool counts (Slack, GitHub, Notion) so only
	// tool *names* announce up front and full schemas load on demand
	// via the ToolSearch pseudo-tool.
	//
	//   [[mcp_servers]]
	//   name        = "github"
	//   defer_tools = true
	DeferTools bool `toml:"defer_tools,omitempty"`
}

// MCPOAuthSection configures the PKCE flow biu drives when an MCP HTTP
// server replies 401. All four URL/ID fields are typically required —
// RFC 9728 metadata auto-discovery from the WWW-Authenticate header is
// a future refinement; today the user copies these from the provider's
// developer console.
type MCPOAuthSection struct {
	ClientID     string   `toml:"client_id"`
	AuthorizeURL string   `toml:"authorize_url"`
	TokenURL     string   `toml:"token_url"`
	Scopes       []string `toml:"scopes,omitempty"`
	CallbackPort int      `toml:"callback_port,omitempty"` // 0 = ephemeral
}

type DefaultSection struct {
	Provider string `toml:"provider"`
	Model    string `toml:"model"`
	Mode     string `toml:"mode"` // "cloud" (default) | "byo_endpoint" | "direct"
}

type HubSection struct {
	Endpoint   string `toml:"endpoint"`
	VirtualKey string `toml:"virtual_key"`
}

type ProviderSection struct {
	APIKey   string `toml:"api_key"`
	Endpoint string `toml:"endpoint"`
}

type PermissionsSection struct {
	Mode      string   `toml:"mode"` // ask | auto_edit | full_access
	Allowlist []string `toml:"allowlist"`

	// PlanDriftThreshold gates when the engine surfaces a `<plan-drift>`
	// system attachment after ExitPlanMode. 0 = surface on first
	// drifted call (default). Negative = observe but never surface
	// (still useful for `biu plan diff`-style inspection).
	PlanDriftThreshold int `toml:"plan_drift_threshold"`

	// SuggestPlanFor lists trigger keywords (English / Chinese / any
	// language) that, when found in a user prompt, fold a system
	// note suggesting `EnterPlanMode` into the next turn. Empty
	// inherits planhint.DefaultKeywords.
	SuggestPlanFor []string `toml:"suggest_plan_for"`

	// SuggestPlanDisabled disables the suggestion analyser entirely
	// for users who find it noisy. Suggested defaults are chosen
	// conservatively — leaving this false should be safe for most.
	SuggestPlanDisabled bool `toml:"suggest_plan_disabled"`
}

// SearchSection controls how the `websearch` tool resolves queries.
type SearchSection struct {
	Mode       string `toml:"mode"`        // "model-relay" (default) | "direct"
	SearxNGURL string `toml:"searxng_url"` // required when mode=direct
}

func Defaults() *Config {
	return &Config{
		Search: SearchSection{Mode: "model-relay"},
		Default: DefaultSection{
			Mode:     "cloud",
			Provider: "anthropic",
			Model:    "claude-sonnet-4-6",
		},
		Relay: HubSection{
			Endpoint: "https://api.biu.app",
		},
		Providers:   map[string]ProviderSection{},
		Permissions: PermissionsSection{Mode: "ask"},
	}
}

// Load returns config from disk merged over defaults. Missing file is OK.
func Load(explicit string) (*Config, string, error) {
	cfg := Defaults()
	path := pickPath(explicit)
	if path == "" {
		return cfg, "", nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, "", nil
		}
		return nil, path, fmt.Errorf("config: read %s: %w", displayPath(path), err)
	}
	if err := toml.Unmarshal(raw, cfg); err != nil {
		return nil, path, fmt.Errorf("config: parse %s: %w — fix the TOML syntax then re-run", displayPath(path), err)
	}
	return cfg, path, nil
}

func pickPath(explicit string) string {
	if explicit != "" {
		return explicit
	}
	if env := os.Getenv("BIU_CONFIG"); env != "" {
		return env
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	p := filepath.Join(home, ".biu", "config.toml")
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return ""
}

// displayPath collapses $HOME → "~" so error output stays portable
// across machines. Inlined to avoid an import cycle with the clierr
// package (which is allowed to depend on config but not the reverse).
func displayPath(p string) string {
	if p == "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if p == home {
		return "~"
	}
	if len(p) > len(home) && p[:len(home)] == home && p[len(home)] == os.PathSeparator {
		return "~" + p[len(home):]
	}
	return p
}

// SessionsDir returns ~/.biu/sessions; created if missing.
func SessionsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".biu", "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}
