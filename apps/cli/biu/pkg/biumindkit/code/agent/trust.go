package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// EnsureClaudeTrust 在启动 claude 前,把 cwd 在 ~/.claude.json 标记为已信任
// (projects[cwd].hasTrustDialogAccepted = true),从而跳过 Claude Code 首次进目录
// 的 folder-trust 交互询问 —— 用户在 BiuMind 里选定 workspace 即视为信任。
//
// 只跳 folder-trust;按 permission_mode 的逐工具审批(ask / auto_edit 在 claude
// TUI 内的提示)不受影响 —— 故不用 --dangerously-skip-permissions(那会连工具
// 权限一起跳过,毁掉 ask 模式)。Claude 无"只信任目录"的 CLI flag,信任态纯由
// ~/.claude.json 控制,只能改这里。
//
// 设计要点(避免损坏用户全局配置):
//   - 仅在 ~/.claude.json 已存在且可解析时改写,不替 claude 凭空造配置。
//   - 完整 read-modify-write:保留所有其它字段,只动目标布尔。
//   - 原子写(同目录临时文件 + rename),并保留原文件权限位。
//   - 幂等:已为 true 直接返回,不重写。
//
// 任何步骤失败都返回 err 供调用方记日志,**调用方不应据此阻塞任务** —— 最坏退回
// 到 claude 自己弹 trust 询问,与改动前等价。
func EnsureClaudeTrust(cwd string) error {
	if cwd == "" {
		return fmt.Errorf("agent: EnsureClaudeTrust empty cwd")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	cfgPath := filepath.Join(home, ".claude.json")

	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", cfgPath, err)
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return fmt.Errorf("parse %s: %w", cfgPath, err)
	}

	projects, _ := root["projects"].(map[string]any)
	if projects == nil {
		projects = map[string]any{}
		root["projects"] = projects
	}
	proj, _ := projects[cwd].(map[string]any)
	if proj == nil {
		proj = map[string]any{}
		projects[cwd] = proj
	}
	// 已信任 → 幂等返回。
	if v, ok := proj["hasTrustDialogAccepted"].(bool); ok && v {
		return nil
	}
	proj["hasTrustDialogAccepted"] = true

	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}

	// 原子写:同目录临时文件 → 保留原权限 → rename 覆盖。
	mode := os.FileMode(0o600)
	if fi, serr := os.Stat(cfgPath); serr == nil {
		mode = fi.Mode().Perm()
	}
	tmp, err := os.CreateTemp(home, ".claude.json.tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // rename 成功后为 no-op
	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return err
	}
	if err := os.Rename(tmpName, cfgPath); err != nil {
		return err
	}
	return nil
}
