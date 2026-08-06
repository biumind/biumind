// biumindkit adapter —— S11-3 内核替换基础设施。
//
// 把 runtime 已有的 agent.Tool / Registry / PermissionMode 投影到
// biumindkit 的 Tool / PermissionPolicy 接口，让新 RunV2（biumindkit
// 驱动）可以复用现有 file/bash/skill/app 工具实现，不必各重写一遍。
//
// 与 brain.tools 的 BiumindkitAdapter 不同（runtime 工具有 Risk 分级
// 而 brain cloud tools 都是 read-only）：
//
//	 Risk    → biumindkit IsReadOnly / IsDestructive 决策
//	 RiskLow → IsReadOnly=true, IsDestructive=false
//	 RiskMedium → IsReadOnly=false, IsDestructive=false
//	 RiskHigh   → IsReadOnly=false, IsDestructive=true
//
// PermissionMode → biumindkit.PermissionPolicyFn 映射：
//
//	 PermAuto    → PermissionAllow （所有 risk 都通过）
//	 PermReadOnly → 自定义：只允许 RiskLow / 拒掉 Med+High
//	 PermSafe / PermAsk → 自定义：拒 RiskHigh，其它通过
//	 默认（空）→ PermSafe 同款
//
// PermissionPolicyFn 拿到的 biumindkit.PermissionRequest 没有 Risk 字段
// （biumindkit 不知道这个分类）—— 我们用 ToolName 反查 Registry
// 拿原 Risk。这样跟 runtime 现有 PermissionMode 行为完全一致。

package agent

import (
	"context"

	"github.com/biumind/biumind/apps/cli/biu/pkg/biumindkit"
)

// BiumindkitAdapter 把单条 agent.Tool 包成 biumindkit.Tool。t.Invoke
// 函数签名跟 biumindkit.ToolDef.Run 完全一致，直接透传不需要二次包装。
//
// 返回 nil 时表示工具没 Invoke（agent.Tool 必须有 Invoke，不像 brain
// 有 descriptor-only 工具，所以这条路径理论上不会触发；保留 nil 检查
// 防御传入异常 Tool）。
func BiumindkitAdapter(t *Tool) biumindkit.Tool {
	if t == nil || t.Invoke == nil {
		return nil
	}
	return biumindkit.NewTool(biumindkit.ToolDef{
		Name:              t.Name,
		Description:       t.Description,
		InputSchema:       t.Parameters,
		IsReadOnly:        t.Risk == RiskLow,
		IsDestructive:     t.Risk == RiskHigh,
		IsConcurrencySafe: t.Risk == RiskLow, // safe to fan-out parallel reads
		Run:               t.Invoke,
	})
}

// AsBiumindkitTools 把整个 Registry 投影成 biumindkit.Tool 切片，喂给
// biumindkit.Options.ExtraTools。worker per-task agent 构造用。
func (r *Registry) AsBiumindkitTools() []biumindkit.Tool {
	out := make([]biumindkit.Tool, 0, len(r.tools))
	for _, t := range r.tools {
		ad := BiumindkitAdapter(t)
		if ad != nil {
			out = append(out, ad)
		}
	}
	return out
}

// PermissionPolicy 根据 mode + Registry 构造一个 biumindkit
// PermissionPolicyFn。policy 拿到 biumindkit.PermissionRequest 时
// 用 ToolName 反查 Registry 拿 Risk，再用 mode.AllowsRisk 决策。
//
// reg 不能为 nil（policy 需要查 Risk）；mode 空 = 默认 PermSafe。
//
// 用法：
//
//	policy := agent.PermissionPolicy(reg, agent.PermSafe)
//	bkOpts.PermissionPolicy = policy
func PermissionPolicy(reg *Registry, mode PermissionMode) biumindkit.PermissionPolicyFn {
	if mode == "" {
		mode = PermSafe
	}
	if mode == PermAuto {
		// PermAuto = 一律放行，避开 Registry 查找开销
		return biumindkit.PermissionAllow()
	}
	return func(_ context.Context, req biumindkit.PermissionRequest) biumindkit.PermissionDecision {
		// 未知工具默认 deny —— 安全兜底，跟旧 invokeToolGated 行为一致
		if reg == nil {
			return biumindkit.PermDeny
		}
		t, ok := reg.Get(req.ToolName)
		if !ok {
			return biumindkit.PermDeny
		}
		if mode.AllowsRisk(t.Risk) {
			return biumindkit.PermAllow
		}
		return biumindkit.PermDeny
	}
}
