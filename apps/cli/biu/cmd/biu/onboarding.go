// onboarding.go — CC 式首启引导（零配置启动）。
//
// 触发条件：config 文件不存在 && 交互 TTY && 非 headless。流程是
// `biu init` 的精简复用：
//
//	1. mode 确认（默认 cloud）
//	2. 浏览器 PKCE 登录（复用 initBrowserLogin；失败回落手贴 token）
//	3. 登录成功后从 /v1/me/models 解析默认模型 — is_default_chat 标记
//	   直接采用（不落 config，admin 换默认自动跟随）；无标记则列目录
//	   让用户选（选中的写入 config，纯本地偏好）
//	4. 写 config.toml
//
// 用户拒绝引导 → 返回 false，进入未登录 REPL 懒引导（kimi 式兜底，
// 与 Claude Code 本尊"登录页可跳过"对齐 —— 不锁死）。

package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"

	"github.com/biumind/biumind/apps/cli/biu/internal/client"
	"github.com/biumind/biumind/apps/cli/biu/internal/clierr"
	"github.com/biumind/biumind/apps/cli/biu/internal/config"
	"github.com/biumind/biumind/apps/cli/biu/internal/modelcatalog"
	"github.com/biumind/biumind/apps/cli/biu/internal/oauth"
	"golang.org/x/term"
)

// runFirstLaunchOnboarding runs the first-launch wizard when no config
// file exists. Returns true when a config was written (caller should
// reload). Any decline / failure → false, degraded REPL takes over.
func runFirstLaunchOnboarding(ctx context.Context) bool {
	if _, err := os.Stat(configPath()); err == nil {
		return false // config 已存在（含 BIU_CONFIG 指向的路径）
	}
	// 只在交互 TTY 下引导 —— 管道/脚本喂 stdin 时提示会吞掉管道数据
	// 并卡住，非交互场景直接走懒引导路径（kimi 式）。
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return false
	}
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Welcome to biu — no configuration found.")
	fmt.Fprintln(os.Stderr, "Let's set you up (you can also skip and run /login later).")
	if !promptYesNo("Set up biu now?", true) {
		fmt.Fprintln(os.Stderr, "[biu] skipped — the REPL starts signed-out; run /login inside to sign in.")
		return false
	}

	mode := selectMode("", false)
	cfg := config.Defaults()
	cfg.Default.Mode = mode

	if client.Mode(mode) == client.ModeDirect {
		key := promptSecret("Paste your Anthropic API key (sk-ant-…): ")
		if key == "" {
			fmt.Fprintln(os.Stderr, "[biu] no API key — falling back to the signed-out REPL.")
			return false
		}
		cfg.Providers["anthropic"] = config.ProviderSection{APIKey: key}
	} else {
		url := promptString("model-relay endpoint URL (https://biumind.xxlab.tech): ", "https://biumind.xxlab.tech")
		cfg.Relay.Endpoint = url
		loggedIn := false
		if promptYesNo("Sign in via browser now? (recommended — token goes to the OS keychain)", true) {
			if lerr := initBrowserLogin(ctx, url); lerr != nil {
				fmt.Fprintf(os.Stderr, "[biu] browser login unavailable: %v — falling back to paste-token\n", lerr)
			} else {
				loggedIn = true
			}
		}
		if !loggedIn {
			cfg.Relay.VirtualKey = promptSecret("model-relay auth token: ")
			if cfg.Relay.VirtualKey == "" {
				fmt.Fprintln(os.Stderr, "[biu] no token — falling back to the signed-out REPL.")
				return false
			}
		}

		// 登录/给 token 后解析默认模型。失败不阻断 —— 空模型交给
		// REPL 的懒引导（首条消息时 live 解析或 /model 手选）。
		cfg.Default.Model = resolveModelWithCatalog(ctx, cfg)
	}

	if err := writeConfig(configPath(), cfg); err != nil {
		fmt.Fprintf(os.Stderr, "[biu] onboarding: write config: %v\n", err)
		return false
	}
	fmt.Fprintf(os.Stderr, "[biu] wrote %s — happy hacking!\n", clierr.DisplayPath(configPath()))
	return true
}

// resolveModelWithCatalog 拉模型目录解析默认模型：
// 有 is_default_chat 标记 → 直接返回（不落 config，live 跟随）；
// 无标记 → 编号列表让用户选（用户选择是显式偏好，由调用方写 config）；
// 网络/未登录失败 → 空串，交给 REPL 懒引导。
func resolveModelWithCatalog(ctx context.Context, cfg *config.Config) string {
	token := cfg.Relay.VirtualKey
	if token == "" {
		if store, err := oauth.Open(""); err == nil {
			if tokens, err := store.Load(); err == nil {
				token = tokens.AccessToken
			}
		}
	}
	if token == "" {
		return ""
	}
	models, err := modelcatalog.List(ctx, cfg.Relay.Endpoint, token)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[biu] model catalog unavailable: %v\n", err)
		return ""
	}
	if code, err := modelcatalog.DefaultChat(models); err == nil {
		fmt.Fprintf(os.Stderr, "[biu] platform default model: %s\n", code)
		return code
	}
	return promptModelChoice(modelcatalog.ChatModels(models))
}

// promptModelChoice 打印编号模型列表让用户选。空目录/无效输入 → ""。
func promptModelChoice(models []modelcatalog.Model) string {
	chat := filterPickable(models)
	if len(chat) == 0 {
		return ""
	}
	fmt.Fprintln(os.Stderr, "No platform default model set — pick one:")
	for i, m := range chat {
		fmt.Fprintf(os.Stderr, "  %d) %-28s %s\n", i+1, m.Code, m.DisplayName)
	}
	pick := promptString("Choice [1-"+strconv.Itoa(len(chat))+"]: ", "1")
	n, err := strconv.Atoi(pick)
	if err != nil || n < 1 || n > len(chat) {
		fmt.Fprintln(os.Stderr, "[biu] invalid pick — model left unset (run /model later).")
		return ""
	}
	return chat[n-1].Code
}

// filterPickable 排序 + 去重展示序：默认模型排最前（若有），
// 其余按 code 字典序 —— 稳定列表比 registry 顺序好猜。
func filterPickable(models []modelcatalog.Model) []modelcatalog.Model {
	out := append([]modelcatalog.Model(nil), models...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].IsDefault != out[j].IsDefault {
			return out[i].IsDefault
		}
		return out[i].Code < out[j].Code
	})
	return out
}
