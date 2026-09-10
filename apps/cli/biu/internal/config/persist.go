// persist.go — write-side helpers for ~/.biu/config.toml.
//
// SetDefaultModel 用于 REPL /model 选择后把用户偏好落盘。与 init 的
// writeConfig（全量手写、只覆盖已知 section）不同，这里必须保住用户
// 文件里可能存在的所有内容（[mcp_servers]、[auth]、[repo-app] 甚至未
// 来新版本加的 section），所以走 generic map round-trip 而不是 Config
// struct re-marshal —— struct 会丢掉 CLI 版本尚未认识的字段。
//
// 已知取舍: go-toml/v2 的 Marshal 不保留注释。config.toml 是机器管理
// 文件（init / /model 写入），注释丢失可接受；用户手写注释属于罕见
// 场景，权衡后不为此引入 toml edit 库。

package config

import (
	"fmt"
	"os"
	"path/filepath"

	toml "github.com/pelletier/go-toml/v2"
)

// SetDefaultModel persists [default].model into the config file,
// creating it (with the standard defaults) when absent. model == "" 删
// 除该键（回落到平台默认 live 解析）。
func SetDefaultModel(model string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	dir := filepath.Join(home, ".biu")
	path := filepath.Join(dir, "config.toml")

	doc := map[string]any{}
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := toml.Unmarshal(raw, &doc); err != nil {
			return fmt.Errorf("config: parse %s: %w", displayPath(path), err)
		}
	case os.IsNotExist(err):
		// 无文件 → 以 Defaults 为底，走与 init 相同的落盘行为
		def := Defaults()
		doc = map[string]any{
			"default":     map[string]any{"mode": def.Default.Mode, "provider": def.Default.Provider},
			"model-relay": map[string]any{"endpoint": def.Relay.Endpoint},
			"permissions": map[string]any{"mode": def.Permissions.Mode},
			"search":      map[string]any{"mode": def.Search.Mode},
		}
	default:
		return err
	}

	sec, _ := doc["default"].(map[string]any)
	if sec == nil {
		sec = map[string]any{}
		doc["default"] = sec
	}
	if model == "" {
		delete(sec, "model")
	} else {
		sec["model"] = model
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	out, err := toml.Marshal(doc)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
