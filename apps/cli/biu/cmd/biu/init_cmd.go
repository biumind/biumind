// `biu init` — interactive setup wizard.
//
// Three goals:
//
//   1. Pick a deployment mode (cloud / byo_endpoint / direct).
//   2. Capture the auth material — either an Anthropic API key (for
//      mode=direct) or model-relay URL+token (for cloud / byo_endpoint).
//   3. Drop the config into ~/.biu/config.toml without clobbering
//      anything that's already there.
//
// We deliberately keep the wizard text-only (no fancy TUI) so it
// works over SSH and through CI provisioning. Anything the user
// answers can also be supplied via flags, making the same code path
// scriptable.

package main

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/internal/client"
	"github.com/biumind/biumind/apps/cli/biu/internal/clierr"
	"github.com/biumind/biumind/apps/cli/biu/internal/config"
	"github.com/biumind/biumind/apps/cli/biu/internal/oauth"
	"github.com/biumind/biumind/apps/cli/biu/internal/permissions"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func newInitCmd(_ *rootFlags) *cobra.Command {
	var (
		modeFlag       string
		apiKeyFlag     string
		relayURLFlag   string
		relayTokenFlag string
		modelFlag      string
		writeMemory    bool
		writeSettings  bool
		nonInteractive bool
	)
	c := &cobra.Command{
		Use:   "init",
		Short: "Interactive setup: write ~/.biu/config.toml + optional BIUMIND.md",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfgPath := configPath()
			if existing, err := os.Stat(cfgPath); err == nil && existing != nil {
				if !nonInteractive {
					ok := promptYesNo(fmt.Sprintf(
						"Config already exists at %s. Overwrite?",
						clierr.DisplayPath(cfgPath)), false)
					if !ok {
						fmt.Fprintln(os.Stderr, "[biu] init: aborted; existing config left in place.")
						return nil
					}
				}
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), 2*time.Minute)
			defer cancel()

			mode := selectMode(modeFlag, nonInteractive)
			cfg := config.Defaults()
			cfg.Default.Mode = mode

			switch client.Mode(mode) {
			case client.ModeDirect:
				key := apiKeyFlag
				if key == "" && !nonInteractive {
					key = promptSecret("Paste your Anthropic API key (sk-ant-…): ")
				}
				if key == "" {
					return clierr.WithHint(
						clierr.Newf("init", "mode=direct needs an Anthropic API key"),
						"pass --api-key, set ANTHROPIC_API_KEY, or re-run without --yes for the interactive prompt")
				}
				cfg.Providers["anthropic"] = config.ProviderSection{APIKey: key}

			default: // cloud / byo_endpoint
				url := relayURLFlag
				if url == "" && !nonInteractive {
					url = promptString("model-relay endpoint URL (https://biumind.xxlab.tech): ", "https://biumind.xxlab.tech")
				}
				if url == "" {
					return clierr.WithHint(
						clierr.Newf("init", "mode=%s needs a model-relay URL", mode),
						"pass --model-relay-url, or re-run without --yes for the interactive prompt")
				}
				cfg.Relay.Endpoint = url
				token := relayTokenFlag
				loggedIn := false
				if token == "" && !nonInteractive {
					// C10（方案 D8）：浏览器登录优先——token 进 OS
					// keychain，不落 config。开浏览器失败/超时回落
					// 手贴 token。
					if promptYesNo("Sign in via browser now? (recommended — token goes to the OS keychain)", true) {
						if lerr := initBrowserLogin(cmd.Context(), url); lerr != nil {
							fmt.Fprintf(os.Stderr, "[biu] browser login unavailable: %v — falling back to paste-token\n", lerr)
						} else {
							loggedIn = true
						}
					}
					if !loggedIn {
						token = promptSecret("model-relay auth token: ")
					}
				}
				if token == "" && !loggedIn {
					return clierr.WithHint(
						clierr.Newf("init", "mode=%s needs model-relay URL + token", mode),
						"pass --model-relay-url and --model-relay-token, run `biu auth login` first, or re-run without --yes for the interactive prompt")
				}
				cfg.Relay.VirtualKey = token
			}

			if modelFlag != "" {
				cfg.Default.Model = modelFlag
			} else if !nonInteractive {
				cfg.Default.Model = promptString(
					fmt.Sprintf("Default model [%s]: ", cfg.Default.Model),
					cfg.Default.Model)
			}

			if err := writeConfig(cfgPath, cfg); err != nil {
				return clierr.Wrapf("init", err, "write %s", clierr.DisplayPath(cfgPath))
			}
			fmt.Fprintf(os.Stderr, "[biu] wrote %s\n", clierr.DisplayPath(cfgPath))

			// Smoke-test the connection so users find out about typos
			// now, not on first prompt.
			if err := smokeTest(ctx, cfg); err != nil {
				fmt.Fprintf(os.Stderr, "[biu] warning: smoke test failed: %v — run `biu doctor` to diagnose\n", err)
			} else {
				fmt.Fprintln(os.Stderr, "[biu] smoke test ok")
			}

			if writeMemory {
				if err := scaffoldMemory(); err != nil {
					fmt.Fprintf(os.Stderr, "[biu] init: BIUMIND.md: %v\n", err)
				} else {
					fmt.Fprintln(os.Stderr, "[biu] wrote BIUMIND.md (project memory)")
				}
			}
			if writeSettings {
				if err := scaffoldSettings(); err != nil {
					fmt.Fprintf(os.Stderr, "[biu] init: settings.json: %v\n", err)
				} else {
					fmt.Fprintln(os.Stderr, "[biu] wrote ~/.biumind/settings.json (default permissions)")
				}
			}
			return nil
		},
	}
	c.Flags().StringVar(&modeFlag, "mode", "", "deployment mode: cloud | byo_endpoint | direct (skip prompt)")
	c.Flags().StringVar(&apiKeyFlag, "api-key", "", "Anthropic API key (for --mode=direct)")
	c.Flags().StringVar(&relayURLFlag, "model-relay-url", "", "model-relay endpoint URL (for --mode=cloud|byo_endpoint)")
	c.Flags().StringVar(&relayTokenFlag, "model-relay-token", "", "model-relay auth token (for --mode=cloud|byo_endpoint)")
	c.Flags().StringVar(&modelFlag, "model", "", "default model (skip prompt)")
	c.Flags().BoolVar(&writeMemory, "with-memory", false, "also drop a BIUMIND.md template in cwd")
	c.Flags().BoolVar(&writeSettings, "with-settings", false, "also write a starter ~/.biumind/settings.json")
	c.Flags().BoolVar(&nonInteractive, "yes", false, "skip every prompt; rely on flags")
	return c
}

// configPath resolves the same path config.Load uses by default.
func configPath() string {
	if env := os.Getenv("BIU_CONFIG"); env != "" {
		return env
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "config.toml"
	}
	return filepath.Join(home, ".biu", "config.toml")
}

func selectMode(flag string, nonInteractive bool) string {
	if flag != "" {
		return flag
	}
	if nonInteractive {
		return string(client.ModeCloud)
	}
	fmt.Fprintln(os.Stderr, "Select deployment mode:")
	fmt.Fprintln(os.Stderr, "  1) cloud         — go through BiuMind model-relay (recommended)")
	fmt.Fprintln(os.Stderr, "  2) byo_endpoint  — model-relay URL + token you control")
	fmt.Fprintln(os.Stderr, "  3) direct        — straight to Anthropic with an API key")
	pick := promptString("Choice [1-3, default 1]: ", "1")
	switch strings.TrimSpace(pick) {
	case "2", "byo_endpoint":
		return string(client.ModeBYOE)
	case "3", "direct":
		return string(client.ModeDirect)
	}
	return string(client.ModeCloud)
}

func smokeTest(ctx context.Context, cfg *config.Config) error {
	switch client.Mode(cfg.Default.Mode) {
	case client.ModeDirect:
		// We don't burn an API key on a real call here — just
		// verify the endpoint resolves.
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
			"https://api.anthropic.com", nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		_ = resp.Body.Close()
		return nil
	default:
		c := client.New(cfg.Relay.Endpoint, cfg.Relay.VirtualKey)
		return c.PingHealth(ctx)
	}
}

// initBrowserLogin 跑与 `biu auth login` 相同的浏览器 PKCE flow
// （C10）。[auth] 覆盖从磁盘上的既有 config 读（init 还没写入新
// config），端点从用户刚输入的 relay URL 推导。
func initBrowserLogin(ctx context.Context, relayEndpoint string) error {
	disk, _, err := config.Load("")
	if err != nil {
		return err
	}
	oc, err := oauth.ConfigFromSources(oauth.Config{
		AuthorizeURL:      disk.Auth.AuthorizeURL,
		TokenURL:          disk.Auth.TokenURL,
		RevokeURL:         disk.Auth.RevokeURL,
		ClientID:          disk.Auth.ClientID,
		Scopes:            disk.Auth.Scopes,
		CallbackPort:      disk.Auth.CallbackPort,
		ManualRedirectURL: disk.Auth.ManualRedirectURL,
	}, relayEndpoint)
	if err != nil {
		return err
	}
	store, err := oauth.Open("")
	if err != nil {
		return err
	}
	lctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	return oauth.BrowserLogin(lctx, oc, store, os.Stderr)
}

// writeConfig serialises cfg as TOML and atomically replaces cfgPath.
// We avoid pulling in a full TOML encoder for one config write — the
// shape is small enough to do by hand.
func writeConfig(cfgPath string, cfg *config.Config) error {
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		return err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[default]\nmode = %q\nprovider = %q\nmodel = %q\n\n",
		cfg.Default.Mode, cfg.Default.Provider, cfg.Default.Model)
	fmt.Fprintf(&b, "[model-relay]\nendpoint = %q\n", cfg.Relay.Endpoint)
	// virtual_key 为空（浏览器登录，token 在 keychain）时不落配置 —
	// 对齐方案 §6.4：登录后用户的 config.toml 不含 virtual_key。
	if cfg.Relay.VirtualKey != "" {
		fmt.Fprintf(&b, "virtual_key = %q\n", cfg.Relay.VirtualKey)
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "[permissions]\nmode = %q\n\n",
		cfg.Permissions.Mode)
	fmt.Fprintf(&b, "[search]\nmode = %q\n", cfg.Search.Mode)
	if cfg.Search.SearxNGURL != "" {
		fmt.Fprintf(&b, "searxng_url = %q\n", cfg.Search.SearxNGURL)
	}
	for name, p := range cfg.Providers {
		fmt.Fprintf(&b, "\n[providers.%s]\n", name)
		if p.APIKey != "" {
			fmt.Fprintf(&b, "api_key = %q\n", p.APIKey)
		}
		if p.Endpoint != "" {
			fmt.Fprintf(&b, "endpoint = %q\n", p.Endpoint)
		}
	}
	tmp := cfgPath + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, cfgPath)
}

func scaffoldMemory() error {
	if _, err := os.Stat("BIUMIND.md"); err == nil {
		return fmt.Errorf("BIUMIND.md already exists")
	}
	body := `# Project notes for biu

Anything written here is injected into every prompt. Use it for:

  * conventions (style, naming) the agent should always follow
  * domain glossary
  * pointers to important files / read-this-first paths

Examples:

  * Use snake_case for environment variables.
  * Tests live under ` + "`./test/`" + ` — never under ` + "`./tests/`" + `.
  * The auth code path starts at ` + "`internal/auth/handler.go`" + `.
`
	return os.WriteFile("BIUMIND.md", []byte(body), 0o644)
}

func scaffoldSettings() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	dir := filepath.Join(home, ".biumind")
	path := filepath.Join(dir, "settings.json")
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%s already exists", clierr.DisplayPath(path))
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	// Template includes the IDE-validation `$schema` pointer + a
	// minimal permissions baseline. We don't ship aggressive sandbox
	// defaults because the right rule set is workflow-dependent — a
	// Python user wants ~/.cache/pip allowed for writes; a Go user
	// wants $GOPATH; a polyglot user wants both. Pointing users at
	// docs/biu/sandbox.md and letting them opt in beats baking in a
	// baseline that breaks half their workflows.
	//
	// JSON has no comment syntax (Go's encoding/json strictly
	// rejects // and /* */) so the template can't carry an inline
	// commented-out sandbox example. The stderr breadcrumb after
	// the file lands tells the user where to look.
	body := fmt.Sprintf(`{
  "$schema": "https://your-biumind.example.com/schemas/biu/settings.schema.json",

  "permissions": {
    "defaultMode": "%s",
    "allow": [
      "Bash(git status)",
      "Bash(git diff:*)",
      "Bash(go build:*)",
      "Bash(go test:*)"
    ],
    "deny": [
      "Bash(rm -rf /)"
    ]
  }
}
`, permissions.ModeDefault)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return err
	}
	// Pointer so the user finds the next layer (sandbox / hooks /
	// statusLine) without having to dig through commands. Routed
	// through stderr so the file's path on stdout (next print)
	// stays scriptable.
	fmt.Fprintln(os.Stderr,
		"[biu] tip: add a `sandbox` block to lock down credential paths "+
			"(see docs/biu/sandbox.md) — the $schema reference enables "+
			"editor autocomplete for every supported field.")
	return nil
}

// ─── prompt helpers ──────────────────────────────────

func promptYesNo(q string, def bool) bool {
	suffix := "[y/N]"
	if def {
		suffix = "[Y/n]"
	}
	fmt.Fprintf(os.Stderr, "%s %s ", q, suffix)
	r := bufio.NewReader(os.Stdin)
	line, _ := r.ReadString('\n')
	line = strings.TrimSpace(strings.ToLower(line))
	if line == "" {
		return def
	}
	return line == "y" || line == "yes"
}

func promptString(q, fallback string) string {
	fmt.Fprint(os.Stderr, q)
	r := bufio.NewReader(os.Stdin)
	line, _ := r.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return fallback
	}
	return line
}

// promptSecret 读敏感输入。stdin 是 TTY 时用 term.ReadPassword 不回
// 显（C11）；管道输入（CI provisioning）对调用方本就可见，退回逐
// 行读。
func promptSecret(q string) string {
	fmt.Fprint(os.Stderr, q)
	if term.IsTerminal(int(os.Stdin.Fd())) {
		raw, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr) // ReadPassword 不回显换行
		if err == nil {
			return strings.TrimSpace(string(raw))
		}
	}
	r := bufio.NewReader(os.Stdin)
	line, _ := r.ReadString('\n')
	return strings.TrimSpace(line)
}
