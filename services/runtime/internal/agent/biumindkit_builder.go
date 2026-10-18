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
	Model  string
	System string
	UserID interface{} // uuid.UUID; interface{} 避开循环 import 风险
	// PermissionMode 是 brain WorkPayload.permission_mode 的用户面权限
	// 模式字符串（不再是 runtime 自有的 agent.PermissionMode 枚举）：
	//
	//	"auto"                 → 共享权限内核 ModeAuto（deny 规则之上全放行）
	//	"plan"                 → 内核 ModePlan（只读）
	//	"manual" / "default" / 未知值 → 内核 default 模式
	//	""                     → 旧 payload 兼容路径：PermissionPolicy(reg,
	//	                         PermSafe) 的 Risk 分级策略，行为与改造前一致
	//
	// 非空模式统一配 PermissionDeny() 兜底：沙箱无 client 审批回路，
	// 残余 ask 落 fail-closed，模型收到 deny 原因（设计文档
	// BiuMind-Auto-Permission-Mode-Design §4.3 的明确决策）。
	PermissionMode string

	// ResolveDefaultModel —— Model 为空时注入 biumindkit 的默认模型
	// 解析钩子(见 default_model.go)。nil 时 biumindkit.New 对空 Model
	// 直接报错,不再有硬编码兜底。
	ResolveDefaultModel func(ctx context.Context) (string, error)

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
//   - PermissionMode（payload 用户面模式）→ 内核模式 + PermissionPolicy
//     映射（resolvePermissionWiring）
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

	// PermissionMode → 内核模式 + PermissionPolicy（映射逻辑收在
	// resolvePermissionWiring，可单测）。
	kernelMode, policy := resolvePermissionWiring(reg, in.PermissionMode)

	// 工具集：reg.AsBiumindkitTools 把每个 *Tool 投影成 biumindkit.Tool
	var bkTools []biumindkit.Tool
	if reg != nil {
		bkTools = reg.AsBiumindkitTools()
	}

	ag, err := biumindkit.New(biumindkit.Options{
		APIKey:              in.AnthropicAPIKey,
		AnthropicEndpoint:   in.AnthropicEndpoint,
		Model:               in.Model,
		ResolveDefaultModel: in.ResolveDefaultModel,
		System:              systemPrompt,
		MaxTokens:           in.MaxTokens,
		MaxToolTurns:        in.MaxToolTurns,
		ExtraTools:          bkTools,
		PermissionMode:      kernelMode,
		PermissionPolicy:    policy,
		LoadProjectMemory:   biumindkit.NoMemory,   // runtime 不读本地 BIUMIND.md
		LoadProjectSettings: biumindkit.NoSettings, // runtime 不读本地 settings.json
		// BypassPermissions=false —— 我们要 PermissionPolicy 实际生效；
		// 不像 chat 模式 cloud 工具全 read-only 可以 bypass
		//
		// AskUser 本期（agent-ask-form P2-b）刻意不传：提问表单只接通了
		// daemon worker（apps/cli/biu worker.askUserFor，elicitation 控制帧
		// 经 brain control queue 回包）。runtime 没有对应的 control 回包通路，
		// 传 nil = AskUserQuestion 工具不进目录（P0 安全默认，模型看不到就
		// 不会调用）。runtime 要支持时照 daemon 侧模式接 pending map +
		// elicitation_response 回包路由。
	})
	if err != nil {
		return nil, fmt.Errorf("agent: build biumindkit agent: %w", err)
	}
	return ag, nil
}

// resolvePermissionWiring 把 payload 的用户面权限模式映射成
// (biumindkit Options.PermissionMode, Options.PermissionPolicy)：
//
//	""        → ("", PermissionPolicy(reg, PermSafe)) —— 旧 payload 兼容
//	            路径，Risk 分级策略，行为与改造前一字不差（内核模式留空 =
//	            biumindkit 不动 Context mode，全部决策由 policy 应答）。
//	"auto"    → ("auto", PermissionDeny()) —— 内核 ModeAuto 在策略链
//	            deny 规则之后全量放行；deny 兜底只接残余 ask。
//	"plan"    → ("plan", PermissionDeny()) —— 内核 ModePlan 只读。
//	"manual" / "default" / 未知值 → ("default", PermissionDeny()) ——
//	            内核 default 模式：只读工具链内放行，写/危险工具落
//	            fallback-ask → 沙箱无 client 回路，deny 兜底 fail-closed，
//	            模型收到 deny 原因（设计文档 §4.3：提示用户改用 auto 或
//	            配置规则）。未知字符串按 ModeFromString 同款语义回落
//	            default，不报错。
func resolvePermissionWiring(reg *Registry, payloadMode string) (string, biumindkit.PermissionPolicyFn) {
	if payloadMode == "" {
		return "", PermissionPolicy(reg, PermSafe)
	}
	switch payloadMode {
	case "auto":
		return "auto", biumindkit.PermissionDeny()
	case "plan":
		return "plan", biumindkit.PermissionDeny()
	default:
		return "default", biumindkit.PermissionDeny()
	}
}
