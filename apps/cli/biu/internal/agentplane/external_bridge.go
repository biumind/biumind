// 外部 CLI backend（agent.Event）→ SDK Protocol v1 帧的桥接（Runtime v3 R3/Q3）。
//
// 平行于 sdkbridge.ToSDKFrame（那条吃 biumindkit.Event）。外部 backend
// （Claude Code / Codex）发的是 packages/go-sdk/biu/agent 的规范化 Event，
// 形状不同，但能复用同一批 SDK Protocol variant，client 渲染无缝。
//
// D1：CLI 自执行工具，biumind 只观察——所以这里只是把观察到的 text/tool 活动
// 翻成帧推给 client，不涉及 permission/拦截。

package agentplane

import (
	"encoding/json"

	"github.com/biumind/biumind/apps/cli/biu/pkg/sdkbridge"
	"github.com/biumind/biumind/packages/go-sdk/biu/agent"
	sdkproto "github.com/biumind/biumind/packages/go-sdk/biu/sdkproto/v1"
)

// externalEventToFrame 把一个 agent.Event 翻成 SDK Protocol 帧。返回 nil 表示
// 此事件不产生 wire 帧（system/raw 等纯调试信息）。sessionID 兜底用 caller 值。
func externalEventToFrame(ev agent.Event, sessionID string) sdkproto.Frame {
	if ev.SessionID != "" {
		sessionID = ev.SessionID
	}
	uuid := sdkbridge.NewFrameUUID()

	switch ev.Type {
	case agent.EventText, agent.EventThinking:
		// thinking 暂并入流式文本（R3 不单独区分；client buffer 累加）。
		if ev.Content == "" {
			return nil
		}
		return &sdkproto.SDKStreamlinedText{
			Type:      sdkproto.TypeStreamlinedTxt,
			Text:      ev.Content,
			UUID:      uuid,
			SessionID: sessionID,
		}

	case agent.EventCommand:
		// Codex shell 命令行（Claude 不发此类）。R3 简单呈现为文本；Codex
		// 接线（R8）再细化成 tool 进度。
		if ev.Content == "" {
			return nil
		}
		return &sdkproto.SDKStreamlinedText{
			Type:      sdkproto.TypeStreamlinedTxt,
			Text:      "$ " + ev.Content,
			UUID:      uuid,
			SessionID: sessionID,
		}

	case agent.EventToolUse:
		if ev.Tool == nil {
			return nil
		}
		return &sdkproto.SDKToolProgress{
			Type:               sdkproto.TypeToolProgress,
			ToolUseID:          ev.Tool.ID,
			ToolName:           ev.Tool.Name,
			ElapsedTimeSeconds: 0, // 0 = start
			UUID:               uuid,
			SessionID:          sessionID,
		}

	case agent.EventToolResult:
		if ev.Tool == nil {
			return nil
		}
		summary := ev.Tool.Output
		if ev.Tool.Error != "" {
			summary = ev.Tool.Error
		}
		return &sdkproto.SDKToolUseSummary{
			Type:                sdkproto.TypeToolUseSummary,
			Summary:             summary,
			PrecedingToolUseIDs: []string{ev.Tool.ID},
			UUID:                uuid,
			SessionID:           sessionID,
		}

	case agent.EventDone:
		// 外部 CLI 不回吐 token 用量（A1 走用户订阅，计费不经 biumind）——
		// usage 留零值。client 用此帧 finalize turn。
		return &sdkproto.SDKResultSuccess{
			Type:              sdkproto.TypeResult,
			Subtype:           sdkproto.SubtypeSuccess,
			IsError:           false,
			Result:            "",
			Usage:             json.RawMessage(`{}`),
			ModelUsage:        map[string]sdkproto.ModelUsage{},
			PermissionDenials: []json.RawMessage{},
			UUID:              uuid,
			SessionID:         sessionID,
		}

	case agent.EventError:
		return &sdkproto.SDKResultError{
			Type:              sdkproto.TypeResult,
			Subtype:           "error_during_execution",
			IsError:           true,
			Usage:             json.RawMessage(`{}`),
			ModelUsage:        map[string]sdkproto.ModelUsage{},
			PermissionDenials: []json.RawMessage{},
			Errors: []json.RawMessage{
				sdkbridge.MustMarshalRaw(map[string]any{"message": ev.Content}),
			},
			UUID:      uuid,
			SessionID: sessionID,
		}

	default:
		// EventSystem / EventRaw / 未知 → 不产生 wire 帧（纯调试）。
		return nil
	}
}
