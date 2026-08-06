// FrameEmitter — 把 RunV2 的事件回调翻成 SDK Protocol v1 wire 帧推到
// 一个 publisher 里。chat 模式 WS 路径用：agentplane.ChatRunner 给 publisher
// 传 NATS PublishSessionFrame，FrameEmitter 把每条 emitter 调用变成一帧
// 发到 biu.session.<sid>.out，ingress 转给客户端 WS。
//
// 跟 BlockEmitter 平行实现 EventEmitter 接口，让 RunV2 内核（agent_v2.go）
// 不知道下游 wire 是 SSE 还是 WS / NATS。
//
// 跟 sdkbridge.ToSDKFrame 的关系：sdkbridge 翻译 biumindkit.Event → 帧；
// 这里翻译 emitter 调用（已经是 BlockEmitter 抽象层）→ 帧。两条路径都通
// 向同样的 sdkproto.Frame 类型，但抽象层不同 —— FrameEmitter 留给 chat
// WS 用因为 RunV2 已经解过 biumindkit.Event 了，不需要再回 biumindkit
// 视图。

package chat

import (
	"encoding/json"
	"fmt"

	"github.com/biumind/biumind/apps/cli/biu/pkg/sdkbridge"
	sdkproto "github.com/biumind/biumind/packages/go-sdk/biu/sdkproto/v1"
	"github.com/google/uuid"
)

// FramePublisher 是 FrameEmitter 推帧的下游。chat WS 路径里实际是
// `func(payload []byte) error { return queue.PublishSessionFrame(ctx, sid, payload) }`。
// 错误只 log，不阻塞 emitter —— LLM 流不能因下游 broker 抖动停下。
type FramePublisher func(payload []byte) error

// FrameEmitter 实现 EventEmitter，每次调用合成一帧 SDK Protocol JSON 推
// 给 publisher。
type FrameEmitter struct {
	SessionID string
	Publish   FramePublisher

	// onError 在 publish / marshal 失败时 fire；nil 时静默吞错（chat WS
	// 默认行为，broker 短抖动不影响 LLM turn）。
	OnError func(err error)
}

// NewFrameEmitter 构造一个 FrameEmitter；publisher 必填，session_id 用
// session 表的 PK。
func NewFrameEmitter(sessionID string, pub FramePublisher) *FrameEmitter {
	return &FrameEmitter{SessionID: sessionID, Publish: pub}
}

// TextDelta 推一帧 streamlined_text。
func (e *FrameEmitter) TextDelta(text string) {
	if text == "" {
		return
	}
	e.publishFrame(&sdkproto.SDKStreamlinedText{
		Type:      sdkproto.TypeStreamlinedTxt,
		Text:      text,
		UUID:      sdkbridge.NewFrameUUID(),
		SessionID: e.SessionID,
	})
}

// CloseActiveText 在 SDK Protocol 帧序列里没有对应概念（每帧独立），
// no-op；保留方法只为了实现接口。
func (e *FrameEmitter) CloseActiveText() {}

// ToolStarted 推一帧 tool_progress 标记 start，返回一个 blockID 让后续
// ToolCompleted/Failed 关联。SDK Protocol 没有"block id"概念 —— 我们
// 内部用 uuid 占位（后续 Completed/Failed 没用到这个 id，因为帧自己带
// tool_use_id 关联到 LLM 那一侧）。
func (e *FrameEmitter) ToolStarted(name string, input any) string {
	id := uuid.NewString()
	e.publishFrame(&sdkproto.SDKToolProgress{
		Type:               sdkproto.TypeToolProgress,
		ToolUseID:          id,
		ToolName:           name,
		ElapsedTimeSeconds: 0,
		UUID:               sdkbridge.NewFrameUUID(),
		SessionID:          e.SessionID,
	})
	return id
}

// ToolCompleted 推一帧 streamlined_tool_use_summary（成功）。result 序列
// 化成 string —— BlockEmitter 同款逻辑：string 透传，其它 JSON marshal。
func (e *FrameEmitter) ToolCompleted(blockID string, result any, ms int64) {
	summary := stringifyResult(result)
	e.publishFrame(&sdkproto.SDKToolUseSummary{
		Type:                sdkproto.TypeToolUseSummary,
		Summary:             summary,
		PrecedingToolUseIDs: []string{blockID},
		UUID:                sdkbridge.NewFrameUUID(),
		SessionID:           e.SessionID,
	})
	_ = ms // 当前 SDKToolUseSummary 没耗时字段；ms 留给后续 SDKToolProgress final 用
}

// ToolFailed 同 ToolCompleted 但 summary 带 error 标记 —— SDK Protocol
// SDKToolUseSummary 自身没 isError 字段，把 errMsg prefix `error: ` 让客户
// 端能识别。
func (e *FrameEmitter) ToolFailed(blockID, errMsg string, ms int64) {
	e.publishFrame(&sdkproto.SDKToolUseSummary{
		Type:                sdkproto.TypeToolUseSummary,
		Summary:             "error: " + errMsg,
		PrecedingToolUseIDs: []string{blockID},
		UUID:                sdkbridge.NewFrameUUID(),
		SessionID:           e.SessionID,
	})
	_ = ms
}

func (e *FrameEmitter) publishFrame(frame sdkproto.Frame) {
	if e.Publish == nil {
		return
	}
	raw, err := json.Marshal(frame)
	if err != nil {
		if e.OnError != nil {
			e.OnError(fmt.Errorf("FrameEmitter: marshal frame: %w", err))
		}
		return
	}
	if err := e.Publish(raw); err != nil {
		if e.OnError != nil {
			e.OnError(fmt.Errorf("FrameEmitter: publish: %w", err))
		}
	}
}

// stringifyResult 跟 BlockEmitter / agent_v2 用的同一惯例：string 直接，
// 其它 JSON marshal 一遍；marshal 错就 %v fallback。
func stringifyResult(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	default:
		b, err := json.Marshal(x)
		if err != nil {
			return fmt.Sprintf("%v", x)
		}
		return string(b)
	}
}
