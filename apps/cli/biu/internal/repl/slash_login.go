// /login + /logout slash — surface OAuth state from inside the REPL.
//
// Store 统一用 oauth.Open("")（keychain 优先，方案 D8 修复：
// 历史上这里用 file-only 的 NewStore，keychain 迁移后 REPL 会误报
// not signed in）。
//
// /login 在未登录时直接触发浏览器登录 flow（与 `biu auth login`
// 共用 oauth.BrowserLogin）；浏览器打不开 / flow 失败时回落到引导
// 文案（manual 粘贴流仍在 `biu auth login --manual`，REPL 内不做
// 逐行交互）。
//
// 注意：flow 会阻塞 slash handler 直到回调到达或超时（5min）——
// bubbletea 的 owned-screen 模型下这是已知取舍，URL 打到 stderr，
// 用户在浏览器完成后 UI 恢复。

package repl

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/biumind/biumind/apps/cli/biu/internal/config"
	"github.com/biumind/biumind/apps/cli/biu/internal/oauth"
	"golang.org/x/term"
)

// handleLogin surfaces the active OAuth token state; 未登录时直接发起
// 浏览器登录。返回 (note, cmd) —— cmd 非 nil 时是登录成功后的 rewire
// 命令（零配置懒引导：REPL 内重建 provider + 解析模型，免重启）。
func (m model) handleLogin(parts []string) (string, tea.Cmd) {
	store, err := oauth.Open("")
	if err != nil {
		return "/login: " + err.Error(), nil
	}
	tokens, err := store.Load()
	if err != nil {
		return fmt.Sprintf("/login: load tokens: %v", err), nil
	}

	if tokens.AccessToken == "" {
		// 只在交互 TTY 下直接发起浏览器登录 —— 管道/headless 场景
		// 阻塞 5min 等回调没有意义，给引导文案即可（也保持
		// slash_login_test 的非交互语义）。
		if !term.IsTerminal(int(os.Stdin.Fd())) {
			return "/login: not signed in.\n\n" + loginGuidance(), nil
		}
		oc, cerr := replOAuthConfig()
		if cerr != nil {
			return "/login: not signed in.\n\n" +
				"Cannot derive OAuth endpoints: " + cerr.Error() + "\n\n" +
				loginGuidance(), nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		if lerr := oauth.BrowserLogin(ctx, oc, store, os.Stderr); lerr != nil {
			return fmt.Sprintf("/login: browser login failed: %v\n\n", lerr) +
				loginGuidance(), nil
		}
		// 零配置懒引导：启动时因未登录降级（provider nil）的 REPL 在
		// 这里原地接回 —— 重建 provider + live 解析默认模型，用户不
		// 用重启。已就绪的 REPL 不需要 rewire。
		if m.provider == nil && m.engine == nil && m.rewireAfterLogin != nil {
			return "/login: signed in — credentials stored in " + store.Path() + ".\n" +
				"Wiring the provider…", m.rewireCmd()
		}
		return "/login: signed in — credentials stored in " + store.Path() + ".\n" +
			"Tokens are picked up on the next API call (or after restart).", nil
	}

	var b strings.Builder
	b.WriteString("/login: signed in.\n")
	fmt.Fprintf(&b, "  backend:     %s (%s)\n", store.Backend(), store.Path())
	if tokens.Provider != "" {
		fmt.Fprintf(&b, "  provider:    %s\n", tokens.Provider)
	}
	if tokens.Scope != "" {
		fmt.Fprintf(&b, "  scope:       %s\n", tokens.Scope)
	}
	if tokens.TokenType != "" {
		fmt.Fprintf(&b, "  token type:  %s\n", tokens.TokenType)
	}
	if !tokens.ExpiresAt.IsZero() {
		left := time.Until(tokens.ExpiresAt).Round(time.Minute)
		if left < 0 {
			fmt.Fprintf(&b, "  expires:     %s ago — refresh required\n", -left)
		} else {
			fmt.Fprintf(&b, "  expires in:  %s (%s)\n",
				left, tokens.ExpiresAt.Local().Format(time.RFC3339))
		}
	}
	if tokens.Expired() {
		b.WriteString("\n  ! token expired or about to expire — biu refreshes lazily " +
			"on the next API call. Force re-login with: /logout && biu auth login.")
	}
	if tokens.RefreshToken == "" {
		b.WriteString("\n  ! no refresh token — when access expires you'll need to /logout " +
			"+ biu auth login again.")
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// rewireCmd 登录成功后在后台重建 provider + 解析模型，结果经
// rewiredMsg 回到 Update（零配置懒引导的免重启接回路径）。
func (m model) rewireCmd() tea.Cmd {
	fn := m.rewireAfterLogin
	return func() tea.Msg {
		p, model, err := fn()
		return rewiredMsg{provider: p, model: model, err: err}
	}
}

// handleLogout 吊销上游 refresh_token（尽力而为）后删本地凭证。
// 网络/服务端失败不阻塞本地登出。
func (m model) handleLogout(parts []string) string {
	store, err := oauth.Open("")
	if err != nil {
		return "/logout: " + err.Error()
	}
	tokens, _ := store.Load()
	if tokens.AccessToken == "" {
		return "/logout: not signed in (nothing to do)"
	}
	revoked := false
	if tokens.RefreshToken != "" {
		if oc, cerr := replOAuthConfig(); cerr == nil && oc.RevokeURL != "" {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			revoked = oauth.Revoke(ctx, oc, tokens.RefreshToken, nil) == nil
			cancel()
		}
	}
	if err := store.Delete(); err != nil {
		return "/logout: delete tokens: " + err.Error()
	}
	msg := "/logout: tokens deleted from local store. Run `biu auth login` to sign in again."
	if tokens.RefreshToken != "" && !revoked {
		msg += "\nNote: upstream revoke failed or unavailable — if the refresh " +
			"token was leaked, rotate it on the IdP side."
	}
	return msg
}

func loginGuidance() string {
	return "Run from your shell:\n" +
		"  biu auth login                    # interactive OAuth\n" +
		"  biu auth login --manual           # paste-flow for headless boxes\n\n" +
		"After login, return to this REPL — the new tokens are picked up automatically."
}

// replOAuthConfig 从 ~/.biu/config.toml 推导 OAuth 端点（与
// `biu auth login` 同一套 C1 规则；repl 拿不到 rootFlags，relay
// endpoint 走 env > config）。
func replOAuthConfig() (oauth.Config, error) {
	cfg, _, err := config.Load("")
	if err != nil {
		return oauth.Config{}, err
	}
	relayEndpoint := cfg.Relay.Endpoint
	if env := os.Getenv("BIUMIND_MODEL_RELAY_URL"); env != "" {
		relayEndpoint = env
	}
	return oauth.ConfigFromSources(oauth.Config{
		AuthorizeURL:      cfg.Auth.AuthorizeURL,
		TokenURL:          cfg.Auth.TokenURL,
		RevokeURL:         cfg.Auth.RevokeURL,
		ClientID:          cfg.Auth.ClientID,
		Scopes:            cfg.Auth.Scopes,
		CallbackPort:      cfg.Auth.CallbackPort,
		ManualRedirectURL: cfg.Auth.ManualRedirectURL,
	}, relayEndpoint)
}
