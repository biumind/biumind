// biumindkit.Event → SDK Protocol v1 wire 帧的桥接。
//
// 为什么放在 bridge 包：bridge 是协议边界（biumindkit.Event 是引擎内部抽象，
// SDK Protocol 是 wire 协议），转换逻辑天然属于这一层。Brain / runtime 之后
// 各自在自己的 wire layer 调用同样的函数（届时移到 packages/go-sdk）。
//
// 映射表（详见 docs/BiuMind-Agent-Plane-Schema-Mapping.md）：
//
//   biumindkit.AssistantText  → sdkproto.SDKStreamlinedText
//   biumindkit.ToolStart      → sdkproto.SDKToolProgress（elapsed=0 表示 start）
//   biumindkit.ToolResult     → sdkproto.SDKToolUseSummary
//   biumindkit.Done           → sdkproto.SDKResultSuccess
//   biumindkit.Error          → sdkproto.SDKResultError
//   biumindkit.CompactStarted → sdkproto.BiumindCompactStarted（BiuMind 扩展）
//   biumindkit.CompactFinished→ sdkproto.BiumindCompactFinished（同上）
//
// 每个 SDKMessage 都需要 uuid + session_id —— uuid 由 bridge 生成（biumindkit
// 不提供），session_id 来自 sessionRec.id。
//
// SDKResultSuccess required 字段（usage / modelUsage / permission_denials /
// num_turns）biumindkit Done 没全提供 —— 用零值/空切片填，等 brain 集成时再
// 用 agent.Cost() 完整数据。

package sdkbridge

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/biumind/biumind/apps/cli/biu/pkg/biumindkit"
	sdkproto "github.com/biumind/biumind/packages/go-sdk/biu/sdkproto/v1"
)

// ToSDKFrame 把 biumindkit.Event 翻译成 SDK Protocol v1 帧。返回 sdkproto.Frame
// 接口（编译期已经被锁定为合法 wire 类型集合，见 sdkproto/v1/service.go）。
//
// session_id / parent_tool_use_id 直接从 event 自带元数据读（PR-1: F10/F13
// 之后 biumindkit Event 接口暴露了 SessionID() / ParentToolUseID() 方法,
// engine 的 stamping forwarder 保证这两个字段已经被填好）。callerSessionID
// 入参保留是为了向下兼容老调用 —— 如果 event 自身的 SessionID 为空就用入参
// 兜底,否则忽略入参。新代码直接 ToSDKFrame(ev, "") 即可。
//
// parent_tool_use_id 仅在 sub-agent 场景下非空：外层 AgentTool 启动子 engine
// 时 spawner 把自己的 tool_use_id 传给 engine.Options.ParentToolUseID,
// 子 engine 发的所有事件都 carry 这个 id, 这层 mapping 把它写进 sdkproto
// 字段 — 客户端就能画完整的「外层 AgentTool → 内层 Read/Bash」调用树。
//
// 不会返回 nil + nil error —— 未知事件类型走 fallback 用 SDKStreamlinedText
// 包一层 raw JSON 提示，避免静默丢帧。
func ToSDKFrame(ev biumindkit.Event, callerSessionID string) sdkproto.Frame {
	uuid := NewFrameUUID()
	// 防御 nil event：测试 / 空 channel close 后竞态可能传 nil；按 unknown
	// 走 fallback 而不是 panic。
	if ev == nil {
		return &sdkproto.SDKStreamlinedText{
			Type:      sdkproto.TypeStreamlinedTxt,
			Text:      "[unsupported biumindkit event <nil>]",
			UUID:      uuid,
			SessionID: callerSessionID,
		}
	}
	sessionID := ev.SessionID()
	if sessionID == "" {
		sessionID = callerSessionID
	}
	parentToolUseID := optStr(ev.ParentToolUseID())
	switch e := ev.(type) {
	case biumindkit.StreamingText:
		// 流式增量文本 —— 每个 chunk 一帧，客户端 buffer 累加。AssistantText
		// 和 AssistantBlock(text) 都是同一份内容的"完整快照"重复，不能再发
		// SDKStreamlinedText（client 累加会变 2x / 3x 重复）。
		return &sdkproto.SDKStreamlinedText{
			Type:      sdkproto.TypeStreamlinedTxt,
			Text:      e.Text,
			UUID:      uuid,
			SessionID: sessionID,
		}
	case biumindkit.AssistantText:
		// 整段 assembled 文本，跟 StreamingText 增量是同一份内容，**丢弃**避免
		// 重复渲染。Done event 会触发 SDKResultSuccess 让客户端 finalize。
		return nil
	case biumindkit.AssistantBlock:
		// Per-block 视图。tool_use 走 ToolStart 路径单独发，这里跳过；text
		// 块跟 StreamingText / AssistantText 重复，**也丢弃**。其它块类型
		// （image / thinking）暂未走这条路。
		if e.Block.Type == biumindkit.ContentToolUse {
			return &sdkproto.SDKToolProgress{
				Type:               sdkproto.TypeToolProgress,
				ToolUseID:          e.Block.ToolUseID,
				ToolName:           e.Block.ToolUseName,
				ParentToolUseID:    parentToolUseID,
				ElapsedTimeSeconds: 0,
				UUID:               uuid,
				SessionID:          sessionID,
			}
		}
		return nil
	case biumindkit.ToolStart:
		// elapsed=0 表示 tool 刚开始执行；ToolUseID = biumindkit 的 ID 字段。
		// Input 字段在 SDKToolProgress 里没位置（input 已被包在前一条
		// SDKAssistantMessage 里，这里不重复发）—— 暂时丢弃，等 brain 集成补
		// SDKAssistantMessage 时一并 emit content blocks。
		return &sdkproto.SDKToolProgress{
			Type:               sdkproto.TypeToolProgress,
			ToolUseID:          e.ID,
			ToolName:           e.Name,
			ParentToolUseID:    parentToolUseID,
			ElapsedTimeSeconds: 0,
			UUID:               uuid,
			SessionID:          sessionID,
		}
	case biumindkit.ToolResult:
		// Summary 直接放 Output 字符串。SDKToolUseSummary 设计上本是简短摘要，
		// 但 biumindkit 已经把完整 output 编码为 string —— 可读性更好。
		// preceding_tool_use_ids 单元素：当前 tool。
		return &sdkproto.SDKToolUseSummary{
			Type:                sdkproto.TypeToolUseSummary,
			Summary:             e.Output,
			PrecedingToolUseIDs: []string{e.ID},
			UUID:                uuid,
			SessionID:           sessionID,
		}
	case biumindkit.CompactStarted:
		return &sdkproto.BiumindCompactStarted{
			Type:         sdkproto.TypeBiumindCompactStarted,
			SessionID:    sessionID,
			Reason:       e.Reason,
			TokensBefore: e.TokensBefore,
		}
	case biumindkit.CompactFinished:
		return &sdkproto.BiumindCompactFinished{
			Type:         sdkproto.TypeBiumindCompactFinished,
			SessionID:    sessionID,
			TokensBefore: e.TokensBefore,
			TokensAfter:  e.TokensAfter,
			TokensSaved:  e.TokensSaved,
		}
	case biumindkit.Error:
		// SDKResultError 严格 schema 要求一堆字段；用零值/空切片填。
		// subtype="error_during_execution" 是该 schema 下标准的执行错误码。
		return &sdkproto.SDKResultError{
			Type:              sdkproto.TypeResult,
			Subtype:           "error_during_execution",
			DurationMs:        0,
			DurationAPIMs:     0,
			IsError:           true,
			NumTurns:          0,
			TotalCostUSD:      0,
			Usage:             json.RawMessage(`{}`),
			ModelUsage:        map[string]sdkproto.ModelUsage{},
			PermissionDenials: []json.RawMessage{},
			Errors: []json.RawMessage{
				MustMarshalRaw(map[string]any{
					"message":     e.Err.Error(),
					"recoverable": e.Recoverable,
				}),
			},
			UUID:      uuid,
			SessionID: sessionID,
		}
	case biumindkit.Done:
		// SDKResultSuccess required: result / stop_reason / total_cost_usd /
		// usage / modelUsage / permission_denials / num_turns. biumindkit
		// Done 没 result 字符串（流式 assistant_text 已经走过；result 是
		// 摘要）—— 留空，brain 集成补。
		return &sdkproto.SDKResultSuccess{
			Type:          sdkproto.TypeResult,
			Subtype:       sdkproto.SubtypeSuccess,
			DurationMs:    int(e.Elapsed.Milliseconds()),
			DurationAPIMs: int(e.Elapsed.Milliseconds()),
			IsError:       false,
			NumTurns:      0,
			Result:        "",
			StopReason:    StrPtr(e.StopReason),
			TotalCostUSD:  0,
			Usage: MustMarshalRaw(map[string]any{
				"input_tokens":                e.InputTokens,
				"output_tokens":               e.OutputTokens,
				"cache_read_input_tokens":     e.CacheReadTokens,
				"cache_creation_input_tokens": e.CacheWriteTokens,
			}),
			ModelUsage:        map[string]sdkproto.ModelUsage{},
			PermissionDenials: []json.RawMessage{},
			UUID:              uuid,
			SessionID:         sessionID,
		}
	default:
		// 兜底：把 raw 类型描述塞进 streamlined_text，方便排查；不丢帧。
		return &sdkproto.SDKStreamlinedText{
			Type:      sdkproto.TypeStreamlinedTxt,
			Text:      fmt.Sprintf("[unsupported biumindkit event %T]", ev),
			UUID:      uuid,
			SessionID: sessionID,
		}
	}
}

// newFrameUUID 生成 16 byte hex 字符串作为帧级 uuid。bridge 不需要 RFC 4122
// 严格格式 —— 全局唯一就够。同 sessionRec.id 用 crypto/rand 路径。
func NewFrameUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// mustMarshalRaw 是给已知不会 fail 的内联 map 用的简化包装。
// 真出错（不可能）回退空对象，避免 panic 影响活会话。
func MustMarshalRaw(v any) json.RawMessage {
	buf, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return json.RawMessage(buf)
}

// strPtr 把 string 转 *string。用于可选指针字段。
func StrPtr(s string) *string { return &s }

// optStr 把空字符串转成 nil *string,非空转成 &s。SDK Protocol 字段
// (parent_tool_use_id 等) 是 *string + omitempty,空字符串和缺字段语义
// 不同 —— 子 agent 一定有值,顶层 agent 一定没值,用指针自然区分。
func optStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
