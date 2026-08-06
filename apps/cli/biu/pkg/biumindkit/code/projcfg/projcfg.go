// Package projcfg 读写项目级配置 .biu/config.toml。
//
// 每个项目可设默认 agent / 默认权限档 / prompt 前缀 —— 新建任务时作初值,prompt 前缀
// 在任务启动时拼到 prompt 最前(让某仓库的每个任务都带上「遵循 STYLE.md」这类约束)。
//
// 范围:本版只实现 [agent] 段(default / default_permission_mode / prompt_prefix),三项
// 都已接线。[git] 段(commit_prompt / 超时)留待后续 —— 它需把自定义 prompt
// 串进 gitassist.GenerateCommitMessage 两层,改动面更大,先不引入空转字段。
//
// 容错:文件缺失或解析失败都回退默认,不让坏配置阻断任务。
package projcfg

import (
	"fmt"
	"os"
	"path/filepath"

	toml "github.com/pelletier/go-toml/v2"
)

// Agent 是 [agent] 段。json tag 与 toml 同名(snake_case),使 RPC 经 dispatch 走 JSON
// 时键名与文件一致、与 Dart 端对齐。
type Agent struct {
	// Default 是新任务默认 agent:"biu" | "claude" | "codex"。
	Default string `toml:"default" json:"default"`
	// DefaultPermissionMode 是默认权限档:"ask" | "auto_edit" | "full_access"。
	DefaultPermissionMode string `toml:"default_permission_mode" json:"default_permission_mode"`
	// PromptPrefix 自动拼到每个任务 prompt 最前(后跟一个空行)。
	PromptPrefix string `toml:"prompt_prefix" json:"prompt_prefix"`
}

// Config 是 .biu/config.toml 的整体结构。
type Config struct {
	Agent Agent `toml:"agent" json:"agent"`
}

// Default 返回默认配置(biumind 默认 agent 是进程内 biu)。
func Default() Config {
	return Config{Agent: Agent{Default: "biu", DefaultPermissionMode: "ask", PromptPrefix: ""}}
}

func configPath(root string) (string, error) {
	if root == "" {
		return "", fmt.Errorf("projcfg: empty project root")
	}
	return filepath.Join(root, ".biu", "config.toml"), nil
}

// Read 读项目配置。文件缺失或解析失败 → 返回默认(不报错,容坏配置)。
func Read(root string) (Config, error) {
	p, err := configPath(root)
	if err != nil {
		return Default(), err
	}
	data, rerr := os.ReadFile(p)
	if rerr != nil {
		if os.IsNotExist(rerr) {
			return Default(), nil
		}
		return Default(), rerr
	}
	cfg := Default()
	if err := toml.Unmarshal(data, &cfg); err != nil {
		// 坏配置回退默认,不阻断任务。
		return Default(), nil
	}
	cfg.normalize()
	return cfg, nil
}

// Write 写项目配置(建 .biu 目录,原子写)。
func Write(root string, cfg Config) error {
	p, err := configPath(root)
	if err != nil {
		return err
	}
	if derr := os.MkdirAll(filepath.Dir(p), 0o755); derr != nil {
		return derr
	}
	cfg.normalize()
	raw, err := toml.Marshal(cfg)
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// normalize 兜底空字段为默认值,使读到的配置始终可用。
func (c *Config) normalize() {
	if c.Agent.Default == "" {
		c.Agent.Default = "biu"
	}
	if c.Agent.DefaultPermissionMode == "" {
		c.Agent.DefaultPermissionMode = "ask"
	}
}
