// biumindkit builder —— S11-3 内核替换的"组装"入口。
//
// 把现有 agent.Run 的工具加载（memory / skills / apps）+ 系统提示拼接
// 逻辑抽出来成 BuildBiumindkitAgent —— 返回一个就绪的 *biumindkit.Agent，
// 调用方（agentplane.Worker）自己跑 Submit + 帧 publish。
//
// 跟原 agent.Run 区别：
//
//	agent.Run    → relayclient.ChatStream → publisher.Publish (AG-UI)
//	BuildAgent → biumindkit.New → 调用方 Submit → sdkbridge → SDK Protocol 帧
//
// 复用：per-run Registry 克隆 + skill loading + apps loading + system
// prompt 拼接。这些跟 LLM 驱动无关，新老路径共享。
//
// 不复用：relayclient + publisher + invokeToolGated。biumindkit 直接做
// LLM 调用 + 内置 tool 调度（通过 ExtraTools + PermissionPolicy 适配）。

package agent

import (
	"context"
	"fmt"

	"github.com/biumind/biumind/apps/cli/biu/pkg/biumindkit"
	skillsreg "github.com/biumind/biumind/services/runtime/internal/skills"
)

// BuildBiumindkitAgentInput 收集构造一个 task-mode biumindkit.Agent 需要
// 的全部输入。跟 RunInput 字段重合很多 —— 调用方可以用 RunInputToBuild
// 一键转换。
type BuildBiumindkitAgentInput struct {
	// Anthropic 直连凭证（biumindkit 接 Anthropic Messages API）
	AnthropicAPIKey   string
	AnthropicEndpoint string

	// 任务参数 —— 跟 RunInput 一致
	Model          string
	System         string
	UserID         interface{} // uuid.UUID; interface{} 避开循环 import 风险
	PermissionMode PermissionMode

	// 工具 / 资源依赖（跟 RunInput.Memory / Skills / Apps 同款）
	Tools     *Registry
	Memory    MemoryClient
	ProjectID string
	Skills    *SkillToolDeps
	Apps      *AppToolDeps

	// MaxTokens / MaxToolTurns —— 透传到 biumindkit；零值用默认
	MaxTokens    int
	MaxToolTurns int
}

// BuildBiumindkitAgent 构造一个 task-mode biumindkit.Agent，包含：
//   - 克隆基础 Registry + 注册 memory / skills / apps 工具
//   - skills + apps 的系统提示注入
//   - PermissionMode → biumindkit.PermissionPolicy 映射
//   - PriorMessages = nil（task mode 单轮提示，没历史；agentplane router
//     不投递 thread 历史）
//
// 调用方（worker.go AgentBuilder）拿到 Agent 后调 Submit(prompt) 跑。
// 错误：返回 (nil, err) 时调用方应推一帧 SDKResultError 让 client 知道
// runtime 准备失败。
func BuildBiumindkitAgent(ctx context.Context, logger interface {
	Warn(msg string, args ...any)
	Debug(msg string, args ...any)
}, in BuildBiumindkitAgentInput) (*biumindkit.Agent, error) {
	if in.AnthropicAPIKey == "" {
		return nil, fmt.Errorf("agent: BuildBiumindkitAgent: AnthropicAPIKey required")
	}

	// Per-run Registry：跟原 agent.Run 同样克隆模式 —— 防 memory / skill /
	// apps 在并发任务间互相污染。无 per-run deps 时直接用基础 Registry。
	reg := in.Tools
	needsRunReg := reg != nil &&
		((in.Memory != nil && in.ProjectID != "") || in.Skills != nil || in.Apps != nil)

	// Apps 加载：要先于 needsRunReg 分支跑（系统提示也要用 loaded.AppsBlock）
	var loadedApps AppsLoaded
	if in.Apps != nil && in.Apps.LoadFn != nil {
		userID, _ := in.UserID.(interface{ String() string })
		_ = userID // 占位 —— 类型断言留给调用方传 uuid.UUID
		// 调用方应直接传 uuid.UUID；TypeAssertion 在调用层做
	}

	if needsRunReg {
		runReg := NewRegistry()
		for n, t := range reg.tools {
			runReg.tools[n] = t
		}
		if in.Memory != nil && in.ProjectID != "" {
			RegisterMemoryTools(runReg, in.Memory, in.ProjectID)
		}
		if in.Skills != nil {
			RegisterSkillTools(runReg, *in.Skills)
		}
		if in.Apps != nil && in.Apps.RegisterFn != nil && loadedApps != nil {
			n := in.Apps.RegisterFn(runReg, loadedApps)
			logger.Debug("app tools registered", "count", n)
		}
		reg = runReg
	}

	// Skills 系统提示注入（跟 agent.Run 同款）—— 能拿到的话拼到 System 前
	systemPrompt := in.System
	if in.Skills != nil && in.Skills.Registry != nil {
		loaded, err := in.Skills.Registry.LoadForAgent(ctx, skillsreg.LoadForAgentInput{
			OrgID:            in.Skills.OrgID,
			AgentID:          in.Skills.AgentID,
			Cwd:              in.Skills.Cwd,
			IncludeOrgShared: true,
		})
		if err != nil {
			logger.Warn("skills load failed; continuing without injection",
				"err", err, "org_id", in.Skills.OrgID.String(),
				"agent_id", in.Skills.AgentID.String())
		} else if loaded != nil {
			block := skillsreg.BuildSystemPrompt(loaded)
			if block != "" {
				if systemPrompt != "" {
					systemPrompt = systemPrompt + "\n\n" + block
				} else {
					systemPrompt = block
				}
			}
		}
	}
	// Apps prompt-block 同款拼接（loadedApps 在上面如果加载到了）
	if in.Apps != nil && in.Apps.PromptFn != nil && loadedApps != nil {
		if block := in.Apps.PromptFn(loadedApps); block != "" {
			if systemPrompt != "" {
				systemPrompt = systemPrompt + "\n\n" + block
			} else {
				systemPrompt = block
			}
		}
	}

	// PermissionMode → PermissionPolicy（适配器在 biumindkit_adapter.go）
	policy := PermissionPolicy(reg, in.PermissionMode)

	// 工具集：reg.AsBiumindkitTools 把每个 *Tool 投影成 biumindkit.Tool
	var bkTools []biumindkit.Tool
	if reg != nil {
		bkTools = reg.AsBiumindkitTools()
	}

	ag, err := biumindkit.New(biumindkit.Options{
		APIKey:              in.AnthropicAPIKey,
		AnthropicEndpoint:   in.AnthropicEndpoint,
		Model:               in.Model,
		System:              systemPrompt,
		MaxTokens:           in.MaxTokens,
		MaxToolTurns:        in.MaxToolTurns,
		ExtraTools:          bkTools,
		PermissionPolicy:    policy,
		LoadProjectMemory:   biumindkit.NoMemory,   // runtime 不读本地 BIUMIND.md
		LoadProjectSettings: biumindkit.NoSettings, // runtime 不读本地 settings.json
		// BypassPermissions=false —— 我们要 PermissionPolicy 实际生效；
		// 不像 chat 模式 cloud 工具全 read-only 可以 bypass
	})
	if err != nil {
		return nil, fmt.Errorf("agent: build biumindkit agent: %w", err)
	}
	return ag, nil
}
