// AgentLoop v2 — biumindkit-driven implementation.
//
// S4-3: 把 chat 模式的 LLM 驱动从 brain 自己的 88 行 for-loop 切到
// biumindkit.Agent.Submit。biumindkit 内部自己跑 tool-call 循环 —— brain
// 只负责：
//
//	1. 把 thread 历史翻译成 biumindkit.Message[] (PriorMessages)
//	2. 把 brain.tools 的 4 个 cloud 工具适配为 biumindkit.Tool[]
//	3. 把 biumindkit Submit 流出的事件翻译成 BlockEmitter 调用
//
// **协议**：biumindkit 直接说 Anthropic Messages SSE 协议。model-relay
// 已支持 verbatim Anthropic SSE —— 请求带 `X-Stream-Format: anthropic`
// (biumindkit 的 relay engine 会自动加) 时,relay 把 unified frame 翻成
// Anthropic 原生 SSE 输出(见 model-relay/internal/api/anthropic_stream.go)。
// 所以 RunV2 的生产路径:
//
//   - **model-relay PassThrough** —— user JWT 做 Bearer 透传
//     (UseRelayAuth=true),relay 做 channel 路由 + 用户级配额;BYOK 由
//     relay 侧按 identity 的 protocol 选 adaptor 统一接住
//
// chat 去 env 化后,ChatRunner 一律走这条路;进程内 BYOK 直连与 legacy
// env 直连已删除。

package chat

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/pkg/biumindkit"
	"github.com/biumind/biumind/services/brain/internal/tools"
)

// AgentRunInputV2 收集 RunV2 需要的输入。区别于 v1 的 AgentRunInput：
//
//   - AnthropicAPIKey / AnthropicEndpoint：biumindkit 直连 LLM 的凭证
//   - History 仍是 hubMessage 数组 —— 保持调用方代码兼容；内部翻译到
//     biumindkit.Message
//   - Emitter 跟 v1 共用 —— 这样 SSE 输出对客户端零差异
type AgentRunInputV2 struct {
	// Anthropic 直连凭证。ApiKey 必填；Endpoint 空时走 api.anthropic.com 默认。
	AnthropicAPIKey   string
	AnthropicEndpoint string
	// UseRelayAuth 让 biumindkit 把 ApiKey 当 model-relay Bearer token
	// (`Authorization: Bearer` + `X-Stream-Format: anthropic`),打
	// verbatim Anthropic SSE 的 model-relay endpoint。
	UseRelayAuth bool

	Model         string
	System        string
	Mode          tools.ExecutionMode
	History       []hubMessage
	MaxTokens     int
	Temperature   *float64
	TopP          *float64
	StopSequences []string

	// Images 是当前 turn 用户附带的图片(视觉模型才有效)。
	// 跟 History 解耦:History 内嵌的图片走 PriorMessages 路径,Images 仅
	// 拼到本轮 last user message 的 ContentBlock 末尾。
	Images []ImageInput

	// Emitter 是 RunV2 把 biumindkit 事件投递的目标。EventEmitter 接口让
	// chat WS 路径（agentplane.ChatRunner）传 NATS 帧 publisher，老 SSE
	// 路径（HandleSend）继续传 *BlockEmitter —— RunV2 内核共用一份。
	Emitter EventEmitter

	// AskUser 非 nil 时启用 AskUserQuestion 工具（agent 提问表单，
	// agent-ask-form P1）：模型调用后引擎阻塞等本回调返答案。nil =
	// 工具不进 catalog（无应答链路时模型不可见，防 Decision channel
	// 死锁）。当前只有 agentplane chat WS 路径接线（elicitation 控制帧
	// + pending map），SSE / wiki agent run 路径保持 nil。
	AskUser biumindkit.AskUserFn
}

// EventEmitter 是 RunV2 跑过程中 fire 事件的接收口子。BlockEmitter 实现
// SSE wire（老 chat 路径），FrameEmitter 实现 SDK Protocol JSON 帧（新 chat
// WS 路径，发到 biu.session.<sid>.out）。
//
// RunV2 只调这 5 个方法 —— 其它 BlockEmitter 方法（accumulated text /
// EmitRaw / 等）是 SSE 路径自己用的，不属于内核共享接口。
type EventEmitter interface {
	TextDelta(text string)
	CloseActiveText()
	ToolStarted(name string, input any) string
	ToolCompleted(blockID string, result any, ms int64)
	ToolFailed(blockID string, errMsg string, ms int64)
}

// SingleTurnInput 是 RunSingleTurn 的输入 —— 跨包调用方（agentplane chat
// 路由）用这个公共类型，避免 hubMessage 暴露成跨包契约。
type SingleTurnInput struct {
	AnthropicAPIKey   string
	AnthropicEndpoint string
	// UseRelayAuth=true → biumindkit 用 `Authorization: Bearer <APIKey>`
	//   header (符合 model-relay PassThrough);=false → `x-api-key`
	//   header (Anthropic 原生 / BYOK 直连场景)。
	// agentplane chat_runner.resolveCreds 决定。
	UseRelayAuth bool
	Model        string
	System       string
	Prompt       string // 当前 turn user prompt
	// History 是当前 turn **之前**的对话历史(按时间升序,user/assistant 交替)。
	// 空 = 单轮(向后兼容,老行为)。Runtime v3 R4：WS chat 多轮上下文由 Flutter
	// 经 WorkPayload.History 带入(brain 不持久化 WS chat 消息,维持 Agent Plane
	// 与 chat.Store 解耦)。RunSingleTurn 把 History + Prompt 拼成 RunV2 的 History。
	History []PriorTurn
	Images  []ImageInput // 图片附件(空 = 纯文本)。视觉模型才有效。
	Emitter EventEmitter

	// AskUser 透传进 RunV2 → biumindkit.Options.AskUser。非 nil 时 chat
	// 模式模型可见 AskUserQuestion（提问走 elicitation 控制帧回客户端）。
	// nil = 工具隐藏（无应答链路，防死锁）。
	AskUser biumindkit.AskUserFn
}

// PriorTurn 是一轮历史消息(公开类型,跨包契约——避免暴露私有 hubMessage)。
// Role: "user" | "assistant"。
type PriorTurn struct {
	Role    string
	Content string
}

// ImageInput 是单张图片附件。Data 是 base64-encoded 字节(不带 data: 前缀)。
// MimeType 形如 "image/png" / "image/jpeg" / "image/webp"。
type ImageInput struct {
	MimeType string
	Data     string
}

// RunSingleTurn 是 chat WS 路径的单 turn 入口 —— prompt 直接进 biumindkit.Submit，
// 没历史。多 turn / 带 history 的复杂场景仍走内部 RunV2 + 私有 hubMessage 数组。
//
// 这是 chat 模式 v1 唯一对外公开的入口，agentplane.ChatRunner 调用它。
func (a *AgentLoop) RunSingleTurn(ctx context.Context, in SingleTurnInput) (*AgentRunResult, error) {
	// 纯图问"这是什么?"也合法 —— prompt 可空,但 prompt+图至少有一边。
	if in.Prompt == "" && len(in.Images) == 0 {
		return nil, fmt.Errorf("agent_v2: SingleTurnInput requires Prompt or Images")
	}
	// 拼历史：prior turns（升序）+ 当前 user prompt 作为最后一条。RunV2 要求
	// 最后一条 role=user（满足），之前的进 PriorMessages。History 空 → 退化
	// 为单轮（老行为）。仅接受 user/assistant 历史轮，跳过空/非法 role。
	hist := make([]hubMessage, 0, len(in.History)+1)
	for _, t := range in.History {
		if t.Content == "" || (t.Role != "user" && t.Role != "assistant") {
			continue
		}
		hist = append(hist, hubMessage{Role: t.Role, Content: t.Content})
	}
	hist = append(hist, hubMessage{Role: "user", Content: in.Prompt})
	return a.RunV2(ctx, AgentRunInputV2{
		AnthropicAPIKey:   in.AnthropicAPIKey,
		AnthropicEndpoint: in.AnthropicEndpoint,
		UseRelayAuth:      in.UseRelayAuth,
		Model:             in.Model,
		System:            in.System,
		Mode:              tools.ExecutionCloud,
		History:           hist,
		Images:            in.Images,
		Emitter:           in.Emitter,
		AskUser:           in.AskUser,
	})
}

// RunV2 走 biumindkit 内核跑一轮多回合。返回的 result 跟 v1 形状一样
// 让 send.go 的持久化路径不需要分叉。
//
// 跟 v1 Run 的差异：
//   - 不调 a.Relay.callHubStream —— biumindkit 内部直连 Anthropic
//   - 不在这层管 tool 调用循环 —— biumindkit 自己 loop 到 stop_reason!=tool_use
//   - 没有 Run 里的 turns cap —— biumindkit 用 MaxToolTurns（默认 25）
//
// 调用方法：S4-5 router 在 mode=chat 且 BYOK Anthropic key 存在时调用。
func (a *AgentLoop) RunV2(ctx context.Context, in AgentRunInputV2) (*AgentRunResult, error) {
	if in.AnthropicAPIKey == "" {
		return nil, fmt.Errorf("agent_v2: AnthropicAPIKey required (BYOK or platform key)")
	}
	if in.Emitter == nil {
		return nil, fmt.Errorf("agent_v2: Emitter required")
	}
	if len(in.History) == 0 {
		return nil, fmt.Errorf("agent_v2: empty History (need at least the user prompt)")
	}

	// 拆 history：最后一条 user message 当 prompt 喂 Submit；之前的
	// 都进 PriorMessages。biumindkit 不接受 history 里包含未回答的 user
	// turn —— 它假设 PriorMessages 是已经 settled 的对话。
	last := in.History[len(in.History)-1]
	if last.Role != "user" {
		return nil, fmt.Errorf("agent_v2: last message must be role=user, got %q", last.Role)
	}
	prompt := last.Content
	prior := convertHistoryToPrior(in.History[:len(in.History)-1])

	// Tool catalog：注册表里 cloud-runtime 工具（time/web/wiki/memory.recall），
	// 通过适配器变 biumindkit.Tool[]。chat 模式白名单（default-deny when
	// ChatToolAllowlist set，见 tools/chatmode.go）。
	//
	// P2 #19（agent-42 遗留）：RetrievalBudget > 0 时把关卡包进检索工具的
	// Invoker（retrievalGuard.WrapTool）—— biumindkit 内核里跑的 tool 循环
	// 同样受预算 / signature 去重 / 连空早停约束，拒绝形态与 v1 invoke 一致
	// （soft tool error 喂回模型 + Emitter.ToolFailed 可见步骤）。0 = 关闭。
	var bkTools []biumindkit.Tool
	if a.Registry != nil {
		if a.RetrievalBudget > 0 {
			guard := newRetrievalGuard(a.RetrievalBudget, a.NoYieldStreakLimit)
			bkTools = a.Registry.AvailableForBiumindkitGuarded(a.ChatToolAllowlist, guard.WrapTool)
		} else {
			bkTools = a.Registry.AvailableForBiumindkit(a.ChatToolAllowlist)
		}
	}

	endpoint := in.AnthropicEndpoint
	// 默认 api.anthropic.com（biumindkit NewAnthropicEngine 内部兜底）
	ag, err := biumindkit.New(biumindkit.Options{
		APIKey:              in.AnthropicAPIKey,
		AnthropicEndpoint:   endpoint,
		UseRelayAuth:        in.UseRelayAuth,
		Model:               in.Model,
		System:              in.System,
		MaxTokens:           in.MaxTokens,
		ExtraTools:          bkTools,
		PriorMessages:       prior,
		PermissionPolicy:    biumindkit.PermissionAllow(), // chat 模式 cloud 工具全部 read-only，不询问
		LoadProjectMemory:   biumindkit.NoMemory,          // brain 不读本地 BIUMIND.md
		LoadProjectSettings: biumindkit.NoSettings,        // brain 不读本地 settings.json
		BypassPermissions:   true,
		AskUser:             in.AskUser, // nil → AskUserQuestion 不进 catalog（防死锁）
	})
	if err != nil {
		return nil, fmt.Errorf("agent_v2: build agent: %w", err)
	}
	defer ag.Close()

	result := &AgentRunResult{}

	// Tool tracking：把 ToolStart 的 blockID 记下来 → ToolResult 用同
	// blockID 调 ToolCompleted/ToolFailed。
	toolBlockID := map[string]string{}
	toolStart := map[string]time.Time{}

	// 把 ImageInput 翻成 biumindkit ContentBlock(image type)。biumindkit
	// SubmitContent 会把它们拼到当前 turn 的 user message 文本之后。
	var imageBlocks []biumindkit.ContentBlock
	if len(in.Images) > 0 {
		imageBlocks = make([]biumindkit.ContentBlock, 0, len(in.Images))
		for _, img := range in.Images {
			imageBlocks = append(imageBlocks, biumindkit.ContentBlock{
				Type:          biumindkit.ContentImage,
				ImageMimeType: img.MimeType,
				ImageData:     img.Data,
			})
		}
	}

	if err := pumpBiumindkitEvents(ctx, ag, prompt, imageBlocks, in.Emitter, result, toolBlockID, toolStart); err != nil {
		return result, err
	}

	if result.StopReason == "" {
		result.StopReason = "end_turn"
	}
	return result, nil
}

// pumpBiumindkitEvents 把 biumindkit Submit channel 翻译到 BlockEmitter。
// RunV2 跟 chat WS runner 都走这条 —— chat WS runner 那边 emitter 是
// publish-to-NATS 的版本（agentplane chat_runner 用 JsonFrameEmitter 实现
// BlockEmitterAdapter，本质都是把事件转成 wire 帧）。
func pumpBiumindkitEvents(
	ctx context.Context,
	ag *biumindkit.Agent,
	prompt string,
	attachments []biumindkit.ContentBlock,
	emitter EventEmitter,
	result *AgentRunResult,
	toolBlockID map[string]string,
	toolStart map[string]time.Time,
) error {
	for ev := range ag.SubmitContent(ctx, prompt, attachments) {
		switch e := ev.(type) {
		case biumindkit.StreamingText:
			emitter.TextDelta(e.Text)
		case biumindkit.AssistantBlock:
			if e.Block.Type == biumindkit.ContentToolUse {
				emitter.CloseActiveText()
				blockID := emitter.ToolStarted(e.Block.ToolUseName, e.Block.ToolUseInput)
				toolBlockID[e.Block.ToolUseID] = blockID
				toolStart[e.Block.ToolUseID] = time.Now()
			}
		case biumindkit.ToolStart:
			if _, exists := toolBlockID[e.ID]; !exists {
				blockID := emitter.ToolStarted(e.Name, e.Input)
				toolBlockID[e.ID] = blockID
				toolStart[e.ID] = time.Now()
			}
		case biumindkit.ToolResult:
			blockID, ok := toolBlockID[e.ID]
			if !ok {
				continue
			}
			start := toolStart[e.ID]
			dur := time.Since(start).Milliseconds()
			if e.IsError {
				emitter.ToolFailed(blockID, e.Output, dur)
			} else {
				emitter.ToolCompleted(blockID, e.Output, dur)
			}
			delete(toolBlockID, e.ID)
			delete(toolStart, e.ID)
		case biumindkit.AssistantText:
			result.StopReason = e.StopReason
		case biumindkit.Done:
			result.StopReason = e.StopReason
			result.PromptTokens = e.InputTokens
			result.CompletionTokens = e.OutputTokens
		case biumindkit.Error:
			return fmt.Errorf("agent_v2: %w", e.Err)
		}
	}
	return nil
}

// convertHistoryToPrior 把 brain 的 hubMessage 数组翻译成 biumindkit
// PriorMessages。Anthropic Messages API 接受 user/assistant 两种角色；
// brain 的 "tool" role 翻译成 user message with tool_result content
// block —— biumindkit 内部 state.RoleToolResult 也是这个语义。
func convertHistoryToPrior(history []hubMessage) []biumindkit.Message {
	out := make([]biumindkit.Message, 0, len(history))
	for _, h := range history {
		switch h.Role {
		case "user":
			out = append(out, biumindkit.Message{
				Role:    "user",
				Content: []biumindkit.ContentBlock{{Type: biumindkit.ContentText, Text: h.Content}},
			})
		case "assistant":
			content := []biumindkit.ContentBlock{}
			if h.Content != "" {
				content = append(content, biumindkit.ContentBlock{
					Type: biumindkit.ContentText, Text: h.Content,
				})
			}
			for _, tc := range h.ToolCalls {
				var input map[string]any
				if len(tc.Input) > 0 {
					_ = jsonUnmarshalLoose(tc.Input, &input)
				}
				content = append(content, biumindkit.ContentBlock{
					Type:         biumindkit.ContentToolUse,
					ToolUseID:    tc.ID,
					ToolUseName:  tc.Name,
					ToolUseInput: input,
				})
			}
			out = append(out, biumindkit.Message{
				Role: "assistant", Content: content,
			})
		case "tool":
			// brain 把 tool_result 当独立 message；biumindkit/Anthropic 把
			// 它作为下一条 user message 的 content block。聚合到 prev user
			// 不安全（顺序可能跟 user prompt 混），所以保留为独立 user msg。
			text := strings.TrimSpace(h.Content)
			out = append(out, biumindkit.Message{
				Role: "user",
				Content: []biumindkit.ContentBlock{{
					Type:              biumindkit.ContentToolResult,
					ToolResultID:      h.ToolCallID,
					ToolResultContent: []biumindkit.ContentBlock{{Type: biumindkit.ContentText, Text: text}},
				}},
				ToolUseID: h.ToolCallID,
			})
		}
	}
	return out
}

// jsonUnmarshalLoose 简化 import —— RunV2 不想为这一处单独 import json，
// 复用本包既有的 encoding/json 吃口。
func jsonUnmarshalLoose(raw []byte, dst *map[string]any) error {
	return jsonUnmarshalLooseImpl(raw, dst)
}
