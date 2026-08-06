// Remote-device tool-policy floor (Runtime v3 R6.3 / D7).
//
// 纵深防御：手机/web 端经 brain 投下来的 work 在本机 daemon 执行 Bash/Edit/Read。
// daemon 不盲信 brain——本地有独立硬地板：
//   - 能力轴：--tool-policy preset（readonly / workspace-write / full）映射成
//     biumindkit.ToolFloor，禁掉越权的危险工具（Context deny 规则，见 sdk.go）。
//   - 路径轴：--allowed-roots 限定文件工具可触达的根；越界路径硬拒，不询问。
//
// brain 还会按 per-device policy 在 WorkPayload.ToolPolicy stamp 一个 preset；
// daemon 取 daemon-flag 与 brain-stamp 的**交集**（flag 是上限，brain 只能收窄）。
package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/biumind/biumind/apps/cli/biu/pkg/biumindkit"
)

// tool-policy preset 名（已拍板，对齐 openclaw deny/allowlist/full 思路）。
const (
	policyReadonly       = "readonly"
	policyWorkspaceWrite = "workspace-write"
	policyFull           = "full"
)

// presetRank 给 preset 定序，交集取更严（rank 小）的那个。
var presetRank = map[string]int{
	policyReadonly:       0,
	policyWorkspaceWrite: 1,
	policyFull:           2,
}

// normalizePreset 把任意输入收敛到合法 preset；非法 / 空 → workspace-write
// （安全默认，非 full）。
func normalizePreset(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	if _, ok := presetRank[s]; ok {
		return s
	}
	return policyWorkspaceWrite
}

// intersectPreset 返回两个 preset 中更严格者（rank 较小）。空串视为"无约束"
// （另一方说了算）——brain stamp 为空（task 模式 / 老 brain）时 daemon flag 生效。
func intersectPreset(daemonFlag, brainStamp string) string {
	if brainStamp == "" {
		return normalizePreset(daemonFlag)
	}
	d, b := normalizePreset(daemonFlag), normalizePreset(brainStamp)
	if presetRank[b] < presetRank[d] {
		return b
	}
	return d
}

// resolveToolFloor 把 preset 映射成 biumindkit.ToolFloor。full → nil（无能力
// 地板）；其余给出允许的危险工具集（biumindkit 会 deny 补集）。
func resolveToolFloor(preset string) *biumindkit.ToolFloor {
	switch normalizePreset(preset) {
	case policyFull:
		return nil
	case policyWorkspaceWrite:
		// 读 + 写文件（写仍受 allowed-roots 路径约束）；shell / 子 agent 禁。
		return &biumindkit.ToolFloor{AllowedTools: toSet(
			"Edit", "edit", "Write", "write", "MultiEdit", "NotebookEdit",
		)}
	default: // readonly
		// 一个危险工具都不允许——全部 deny；只读 / 搜索 / 计划等安全工具不受限。
		return &biumindkit.ToolFloor{AllowedTools: map[string]struct{}{}}
	}
}

// resolveAllowedRoots 把 flag 值收敛为绝对路径根集合。空 → daemon 当前 cwd
// （安全默认，绝不返回空——空集会让 withinRoots 拒绝一切）。
func resolveAllowedRoots(flag []string) []string {
	var out []string
	for _, r := range flag {
		if r = strings.TrimSpace(r); r == "" {
			continue
		}
		if abs, err := filepath.Abs(r); err == nil {
			out = append(out, abs)
		}
	}
	if len(out) == 0 {
		if cwd, err := os.Getwd(); err == nil {
			out = []string{cwd}
		}
	}
	return out
}

func toSet(names ...string) map[string]struct{} {
	m := make(map[string]struct{}, len(names))
	for _, n := range names {
		m[n] = struct{}{}
	}
	return m
}

// floorPolicy wrap askPerm：第二道防线（belt-and-suspenders）。能力越权 / 路径
// 越界**直接 Deny 不询问**（不把明显违规的请求抛给用户）；否则委托 delegate
// （走 brain 交互授权）。Context 层 deny 规则是第一道；这里再兜一层，且把
// 越界路径从 Ask 升级成 Deny。
func floorPolicy(roots []string, floor *biumindkit.ToolFloor, delegate biumindkit.PermissionPolicyFn) biumindkit.PermissionPolicyFn {
	return func(ctx context.Context, req biumindkit.PermissionRequest) biumindkit.PermissionDecision {
		// 能力轴：只对危险工具（shell/写/子agent）查 preset；安全工具（读/搜索/
		// 计划等）放行——它们的触达由路径轴约束。危险工具不在 preset 内 → 硬拒。
		if floor != nil && biumindkit.IsFloorDangerousTool(req.ToolName) && !floor.Allows(req.ToolName) {
			return biumindkit.PermDeny
		}
		// 路径轴：文件类工具的路径参数必须落在 roots 内。
		if p := toolPath(req.ToolName, req.Input); p != "" && !withinRoots(p, roots) {
			return biumindkit.PermDeny
		}
		if delegate == nil {
			return biumindkit.PermDeny
		}
		return delegate(ctx, req)
	}
}

// toolPath 取工具调用里的文件路径参数（按工具名选 key）。无路径语义 → ""。
// 与 permissions.stringField 的取参顺序一致。
func toolPath(toolName string, input map[string]any) string {
	if input == nil {
		return ""
	}
	switch strings.ToLower(toolName) {
	case "read", "edit", "write", "multiedit":
		return stringArg(input, "file_path")
	case "notebookread", "notebookedit":
		return stringArg(input, "notebook_path", "file_path")
	case "glob", "grep":
		return stringArg(input, "path")
	}
	return ""
}

func stringArg(input map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := input[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

// withinRoots 报告 path 是否落在任一 root 内。解析符号链接 + 清理 .. 防逃逸。
// roots 为空 → false（无根即一切越界，调用方负责保证 roots 非空）。
func withinRoots(path string, roots []string) bool {
	if len(roots) == 0 {
		return false
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	abs = resolveSymlinks(abs)
	for _, r := range roots {
		ra, err := filepath.Abs(r)
		if err != nil {
			continue
		}
		ra = resolveSymlinks(ra)
		if abs == ra || strings.HasPrefix(abs, ra+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// resolveSymlinks 解析符号链接；对不存在的路径（如待写入的新文件、尚未创建的
// 嵌套目录）解析其**最长存在祖先**再拼回剩余段，避免 /var↔/private 这类
// 软链导致已解析 root 与未解析 path 前缀不匹配（macOS 临时目录即如此）。
func resolveSymlinks(p string) string {
	p = filepath.Clean(p)
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	dir := p
	var tail []string
	for {
		parent := filepath.Dir(dir)
		if parent == dir { // 到达根
			return p
		}
		tail = append([]string{filepath.Base(dir)}, tail...)
		if resolved, err := filepath.EvalSymlinks(parent); err == nil {
			return filepath.Join(append([]string{resolved}, tail...)...)
		}
		dir = parent
	}
}
