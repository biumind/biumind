// Package schema produces JSON-Schema documents (Draft 2020-12) for
// the two config files biu reads:
//
//   - ~/.biu/config.toml      — provider / model-relay / model defaults
//   - settings.json layers    — permissions / hooks / env / model
//
// The schemas serve two masters:
//
//   1. `biu config validate` cross-checks user files at runtime —
//      enum membership, required fields, mutually-exclusive blocks.
//   2. `biu config schema <name>` exports the JSON for IDE consumers
//      (VS Code, IntelliJ, neovim yaml-language-server). Users add
//      `"$schema": "./settings.schema.json"` to settings.json and
//      pick up autocomplete + on-save validation for free.
//
// Hand-written rather than auto-generated from struct tags. Reason:
// the structs use TOML tags for config, JSON tags for settings, and
// the rules / hooks shape doesn't survive a generic reflect-based
// translation. Hand-written keeps it small (≈200 lines) and lets us
// embed prose `description` fields the model and IDEs both consume.

package schema

import (
	"strings"

	"github.com/biumind/biumind/apps/cli/biu/internal/config"
	"github.com/biumind/biumind/apps/cli/biu/internal/settings"
)

// validModes matches client.Mode.IsValid — duplicated rather than
// imported to avoid an internal/client → internal/config/schema cycle.
var validModes = []string{"cloud", "byo_endpoint", "direct"}

// validPermissionModes mirrors permissions.ModeFromString — including
// the legacy aliases (`ask`, `auto_edit`, `full_access`, …) so we
// don't false-positive on a config that's actively used.
//
// Same reason for duplication as validModes (avoid an
// internal/permissions → schema cycle).
var validPermissionModes = []string{
	// canonical
	"default", "acceptEdits", "plan", "bypassPermissions", "dontAskAgain",
	// legacy aliases ModeFromString understands
	"ask", "accept_edits", "bypass", "dontAsk", "dont_ask",
	"auto_edit", "full_access",
}

// validSearchModes is the SearchSection.Mode enum.
var validSearchModes = []string{"model-relay", "direct"}

// ConfigSchema returns a JSON-Schema (Draft 2020-12) for the
// ~/.biu/config.toml file.
func ConfigSchema() map[string]any {
	return map[string]any{
		"$schema":     "https://json-schema.org/draft/2020-12/schema",
		"title":       "biu config.toml",
		"description": "Configuration for the biu CLI; lives at ~/.biu/config.toml.",
		"type":        "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"default": map[string]any{
				"type":        "object",
				"description": "Defaults that apply when subcommands don't override them.",
				"properties": map[string]any{
					"mode": map[string]any{
						"type":        "string",
						"enum":        anyStrings(validModes),
						"description": "Deployment mode; pick `direct` for BYO API key, `cloud` for BiuMind model-relay, `byo_endpoint` for self-hosted proxy.",
					},
					"provider": map[string]any{
						"type":        "string",
						"description": "Default provider name (currently only `anthropic` for direct mode).",
					},
					"model": map[string]any{
						"type":        "string",
						"description": "Default model id (e.g. `claude-sonnet-4-6`).",
					},
				},
			},
			"model-relay": map[string]any{
				"type":        "object",
				"description": "Required when mode=cloud or byo_endpoint.",
				"properties": map[string]any{
					"endpoint":    map[string]any{"type": "string", "format": "uri"},
					"virtual_key": map[string]any{"type": "string"},
				},
			},
			"providers": map[string]any{
				"type":        "object",
				"description": "Per-provider direct-mode configuration. Key is the provider name (e.g. `anthropic`).",
				"additionalProperties": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"api_key":  map[string]any{"type": "string"},
						"endpoint": map[string]any{"type": "string", "format": "uri"},
					},
				},
			},
			"permissions": map[string]any{
				"type":        "object",
				"description": "Default permission mode. Per-tool rules belong in settings.json.",
				"properties": map[string]any{
					"mode":      map[string]any{"type": "string", "enum": anyStrings(validPermissionModes)},
					"allowlist": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				},
			},
			"search": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"mode":        map[string]any{"type": "string", "enum": anyStrings(validSearchModes)},
					"searxng_url": map[string]any{"type": "string", "format": "uri"},
				},
			},
			"auth": map[string]any{
				"type":        "object",
				"description": "OAuth (PKCE) settings for `biu auth login`.",
				"properties": map[string]any{
					"authorize_url":   map[string]any{"type": "string", "format": "uri"},
					"token_url":       map[string]any{"type": "string", "format": "uri"},
					"client_id":       map[string]any{"type": "string"},
					"scopes":          map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"callback_port":   map[string]any{"type": "integer", "minimum": 0, "maximum": 65535},
					"manual_redirect": map[string]any{"type": "string"},
				},
			},
			"mcp_servers": map[string]any{
				"type":        "array",
				"description": "Stdio-launched MCP servers wired into the engine on startup.",
				"items": map[string]any{
					"type":     "object",
					"required": []string{"name", "command"},
					"properties": map[string]any{
						"name":     map[string]any{"type": "string"},
						"command":  map[string]any{"type": "string"},
						"args":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
						"env":      map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
						"cwd":      map[string]any{"type": "string"},
						"disabled": map[string]any{"type": "boolean"},
					},
				},
			},
		},
	}
}

// SettingsSchema returns a JSON-Schema for the settings.json layers
// (user / project / local — same shape across all three).
func SettingsSchema() map[string]any {
	return map[string]any{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"title":                "biu settings.json",
		"description":          "Permissions, hooks, env, and per-project overrides for biu. Same shape applies to ~/.biumind/settings.json, .biumind/settings.json, and .biumind/settings.local.json.",
		"type":                 "object",
		"additionalProperties": true, // forward-compat for unknown keys
		"properties": map[string]any{
			"$schema": map[string]any{
				"type":        "string",
				"description": "Optional pointer to this schema; lets IDEs auto-validate without project config.",
			},
			"permissions": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"defaultMode": map[string]any{
						"type":        "string",
						"enum":        anyStrings(validPermissionModes),
						"description": "Mode applied at session start; can be flipped at runtime with /mode.",
					},
					"allow": ruleArraySchema("Tool(content) rules that pre-approve calls."),
					"deny":  ruleArraySchema("Tool(content) rules that block calls outright; deny wins over allow."),
					"ask":   ruleArraySchema("Tool(content) rules that always prompt regardless of mode."),
					"additionalDirectories": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "Extra cwd ancestors the agent is allowed to read/write.",
					},
				},
			},
			"hooks": map[string]any{
				"type":        "object",
				"description": "Lifecycle commands keyed by event name (PreToolUse, PostToolUse, UserPromptSubmit, …).",
				"additionalProperties": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type":     "object",
						"required": []string{"hooks"},
						"properties": map[string]any{
							"matcher": map[string]any{"type": "string", "description": "Optional tool-name regex."},
							"hooks": map[string]any{
								"type":  "array",
								"items": map[string]any{"$ref": "#/$defs/hookCmd"},
							},
						},
					},
				},
			},
			"model": map[string]any{
				"type":        "string",
				"description": "Override the default model when this layer is active.",
			},
			"env": map[string]any{
				"type":        "object",
				"description": "Environment variables exported into the agent process at session start.",
				"additionalProperties": map[string]any{"type": "string"},
			},
			"claudeMdExcludes": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Glob patterns of BIUMIND.md / CLAUDE.md files to skip when loading project memory.",
			},
			"statusLine": map[string]any{
				"type":        "object",
				"description": "User shell command rendered as the right cluster of the REPL status bar. The command receives a small JSON payload on stdin; first line of stdout becomes the segment text. Errors / non-zero exits silently degrade to no segment.",
				"required":    []string{"command"},
				"properties": map[string]any{
					"type":    map[string]any{"type": "string", "enum": []string{"command"}, "description": "Reserved for future shapes; today only \"command\" is supported."},
					"command": map[string]any{"type": "string", "description": "Shell snippet executed via /bin/sh -c."},
					"timeoutMs": map[string]any{"type": "integer", "minimum": 0, "description": "Cap on execution time in milliseconds. 0 → package default (5 s)."},
				},
			},
			"sandbox": map[string]any{
				"type":        "object",
				"description": "Filesystem allow/deny lists for the Bash tool's sandbox layer. See docs/biu/sandbox.md for a full guide. Path values may use `~` (home), `${VAR}` (env), and `${PROJECT_ROOT}` (project cwd); relative paths are dropped. Three-layer merge is union-only — project / local can ADD entries, none can remove or override earlier layers' entries.",
				"properties": map[string]any{
					"fsReadDeny": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "Paths the sandbox blocks reads to. Use for credentials and secrets (~/.ssh, ~/.aws, ~/.config/gcloud).",
					},
					"fsReadAllowWithinDeny": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "Re-permits reads inside an fsReadDeny entry. Example: block ~/.aws but keep ~/.aws/config readable.",
					},
					"fsWriteAllowExtra": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "Extra writable roots beyond the project cwd. Example: ${PROJECT_ROOT}/build, /tmp/biu-cache.",
					},
					"fsWriteDenyWithinAllow": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "Surgical write denies inside an fsWriteAllowExtra root. The complement of fsReadAllowWithinDeny.",
					},
				},
			},
		},
		"$defs": map[string]any{
			"hookCmd": map[string]any{
				"type":     "object",
				"required": []string{"type", "command"},
				"properties": map[string]any{
					"type":    map[string]any{"type": "string", "enum": []string{"command"}},
					"command": map[string]any{"type": "string"},
					"timeout": map[string]any{"type": "integer", "minimum": 0, "description": "Timeout in seconds (0 = none)."},
				},
			},
		},
	}
}

func ruleArraySchema(desc string) map[string]any {
	return map[string]any{
		"type":        "array",
		"description": desc + " Format: `Tool(pattern)` — Tool ∈ {Bash, Read, Edit, Write, Glob, Grep, …, *}; pattern is a glob/prefix.",
		"items": map[string]any{
			"type":    "string",
			"pattern": "^[A-Za-z*]+(\\(.*\\))?$",
		},
	}
}

// ValidateConfig returns a list of human-readable warnings for cfg.
// Empty slice = clean.
//
// The schema covers structural validation (types, enums); this
// function adds cross-field checks the schema can't express:
//   - mode=direct requires a non-empty providers.<provider>.api_key
//   - mode=cloud / byo_endpoint requires model-relay.endpoint + virtual_key
//   - search.mode=direct requires search.searxng_url
func ValidateConfig(cfg *config.Config) []string {
	var out []string
	mode := strings.ToLower(strings.TrimSpace(cfg.Default.Mode))
	if mode == "" {
		mode = "cloud"
	}
	if !inEnum(mode, validModes) {
		out = append(out, "default.mode="+mode+" — expected one of "+strings.Join(validModes, ", "))
	}
	switch mode {
	case "direct":
		provName := cfg.Default.Provider
		if provName == "" {
			provName = "anthropic"
		}
		ps, ok := cfg.Providers[provName]
		if !ok || ps.APIKey == "" {
			out = append(out, "mode=direct but [providers."+provName+"].api_key is missing")
		}
	case "cloud", "byo_endpoint":
		if cfg.Relay.Endpoint == "" {
			out = append(out, "mode="+mode+" but [model-relay].endpoint is missing")
		}
		if cfg.Relay.VirtualKey == "" {
			out = append(out, "mode="+mode+" but [model-relay].virtual_key is missing")
		}
	}
	if cfg.Search.Mode != "" && !inEnum(cfg.Search.Mode, validSearchModes) {
		out = append(out, "search.mode="+cfg.Search.Mode+" — expected one of "+strings.Join(validSearchModes, ", "))
	}
	if cfg.Search.Mode == "direct" && cfg.Search.SearxNGURL == "" {
		out = append(out, "search.mode=direct but search.searxng_url is missing")
	}
	if cfg.Permissions.Mode != "" && !inEnum(cfg.Permissions.Mode, validPermissionModes) {
		out = append(out, "permissions.mode="+cfg.Permissions.Mode+" — expected one of "+strings.Join(validPermissionModes, ", "))
	}
	return out
}

// ValidateSettings returns warnings for one settings.json layer.
// Same contract as ValidateConfig — empty slice = clean.
func ValidateSettings(s *settings.Settings) []string {
	var out []string
	if s == nil {
		return nil
	}
	if s.Permissions != nil {
		if dm := s.Permissions.DefaultMode; dm != "" && !inEnum(dm, validPermissionModes) {
			out = append(out, "permissions.defaultMode="+dm+" — expected one of "+strings.Join(validPermissionModes, ", "))
		}
		for _, set := range []struct {
			label string
			rules []string
		}{
			{"permissions.allow", s.Permissions.Allow},
			{"permissions.deny", s.Permissions.Deny},
			{"permissions.ask", s.Permissions.Ask},
		} {
			for i, r := range set.rules {
				if !looksLikeRule(r) {
					out = append(out, set.label+"["+itoa(i)+"]="+r+" — expected `Tool(pattern)` or `Tool`")
				}
			}
		}
	}
	return out
}

// inEnum is a tiny helper to keep the validation sites readable.
func inEnum(v string, allowed []string) bool {
	for _, a := range allowed {
		if v == a {
			return true
		}
	}
	return false
}

// looksLikeRule sanity-checks a permissions rule. Forgiving: we
// accept anything that starts with a letter and has balanced parens
// (or no parens at all). Real parsing happens in
// internal/permissions; we don't duplicate it here, just catch the
// obvious typos like trailing `(` or comma-separated rules.
func looksLikeRule(r string) bool {
	r = strings.TrimSpace(r)
	if r == "" {
		return false
	}
	first := r[0]
	if !((first >= 'a' && first <= 'z') || (first >= 'A' && first <= 'Z') || first == '*') {
		return false
	}
	open, close := strings.Count(r, "("), strings.Count(r, ")")
	return open == close
}

// itoa is a stdlib-free strconv.Itoa for the validation message
// formatter — keeps the import set tight.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// anyStrings copies a []string into a []any so it satisfies the
// JSON-Schema enum spec without a per-call allocation in the
// emitter.
func anyStrings(in []string) []any {
	out := make([]any, len(in))
	for i, v := range in {
		out[i] = v
	}
	return out
}
