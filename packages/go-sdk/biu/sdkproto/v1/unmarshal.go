package sdkproto

import (
	"encoding/json"
	"fmt"
)

// Type / Subtype 字面量 — 一处定义避免散落各处的 magic string。
const (
	TypeUser           = "user"
	TypeAssistant      = "assistant"
	TypeStreamEvent    = "stream_event"
	TypeResult         = "result"
	TypeSystem         = "system"
	TypeAuthStatus     = "auth_status"
	TypeRateLimitEvent = "rate_limit_event"
	TypePromptSuggest  = "prompt_suggestion"
	TypeToolProgress   = "tool_progress"
	TypeToolUseSummary = "tool_use_summary"
	TypeStreamlinedTxt = "streamlined_text"
	TypeStreamlinedTUS = "streamlined_tool_use_summary"

	SubtypeInit                 = "init"
	SubtypeStatus               = "status"
	SubtypeCompactBoundary      = "compact_boundary"
	SubtypeAPIRetry             = "api_retry"
	SubtypeLocalCommandOutput   = "local_command_output"
	SubtypeHookStarted          = "hook_started"
	SubtypeHookProgress         = "hook_progress"
	SubtypeHookResponse         = "hook_response"
	SubtypeFilesPersisted       = "files_persisted"
	SubtypeTaskNotification     = "task_notification"
	SubtypeTaskStarted          = "task_started"
	SubtypeTaskProgress         = "task_progress"
	SubtypeSessionStateChanged  = "session_state_changed"
	SubtypeElicitationComplete  = "elicitation_complete"
	SubtypePostTurnSummary      = "post_turn_summary"
	SubtypeSuccess              = "success"
)

// SDKMessage 是数据平面 union 标记接口。所有 28 个 variant 都实现 isSDKMessage()。
// 嵌入 Frame —— 数据平面消息也是合法 WS 帧 wire 类型。
type SDKMessage interface {
	Frame
	isSDKMessage()
}

func (*SDKUserMessage) isSDKMessage()               {}
func (*SDKAssistantMessage) isSDKMessage()          {}
func (*SDKPartialAssistantMessage) isSDKMessage()   {}
func (*SDKResultSuccess) isSDKMessage()             {}
func (*SDKResultError) isSDKMessage()               {}
func (*SDKSystemInit) isSDKMessage()                {}
func (*SDKSystemStatus) isSDKMessage()              {}
func (*SDKCompactBoundary) isSDKMessage()           {}
func (*SDKAPIRetry) isSDKMessage()                  {}
func (*SDKLocalCommandOutput) isSDKMessage()        {}
func (*SDKHookStarted) isSDKMessage()               {}
func (*SDKHookProgress) isSDKMessage()              {}
func (*SDKHookResponse) isSDKMessage()              {}
func (*SDKAuthStatus) isSDKMessage()                {}
func (*SDKFilesPersisted) isSDKMessage()            {}
func (*SDKTaskNotification) isSDKMessage()          {}
func (*SDKTaskStarted) isSDKMessage()               {}
func (*SDKTaskProgress) isSDKMessage()              {}
func (*SDKSessionStateChanged) isSDKMessage()       {}
func (*SDKRateLimitEvent) isSDKMessage()            {}
func (*SDKElicitationComplete) isSDKMessage()       {}
func (*SDKPromptSuggestion) isSDKMessage()          {}
func (*SDKToolProgress) isSDKMessage()              {}
func (*SDKToolUseSummary) isSDKMessage()            {}
func (*SDKPostTurnSummary) isSDKMessage()           {}
func (*SDKStreamlinedText) isSDKMessage()           {}
func (*SDKStreamlinedToolUseSummary) isSDKMessage() {}

// isFrame() —— SDKMessage 的所有 variant 也都是 Frame。
func (*SDKUserMessage) isFrame()               {}
func (*SDKAssistantMessage) isFrame()          {}
func (*SDKPartialAssistantMessage) isFrame()   {}
func (*SDKResultSuccess) isFrame()             {}
func (*SDKResultError) isFrame()               {}
func (*SDKSystemInit) isFrame()                {}
func (*SDKSystemStatus) isFrame()              {}
func (*SDKCompactBoundary) isFrame()           {}
func (*SDKAPIRetry) isFrame()                  {}
func (*SDKLocalCommandOutput) isFrame()        {}
func (*SDKHookStarted) isFrame()               {}
func (*SDKHookProgress) isFrame()              {}
func (*SDKHookResponse) isFrame()              {}
func (*SDKAuthStatus) isFrame()                {}
func (*SDKFilesPersisted) isFrame()            {}
func (*SDKTaskNotification) isFrame()          {}
func (*SDKTaskStarted) isFrame()               {}
func (*SDKTaskProgress) isFrame()              {}
func (*SDKSessionStateChanged) isFrame()       {}
func (*SDKRateLimitEvent) isFrame()            {}
func (*SDKElicitationComplete) isFrame()       {}
func (*SDKPromptSuggestion) isFrame()          {}
func (*SDKToolProgress) isFrame()              {}
func (*SDKToolUseSummary) isFrame()            {}
func (*SDKPostTurnSummary) isFrame()           {}
func (*SDKStreamlinedText) isFrame()           {}
func (*SDKStreamlinedToolUseSummary) isFrame() {}

// UnmarshalSDKMessage 用 type+subtype 两步法 dispatch 到具体 struct。
//
// 见 Schema-Mapping §3.3。result 用 is_error 区分 success/error 而不是 subtype，
// 因为 error_* 子类型跟 success 共享大部分字段。
func UnmarshalSDKMessage(data []byte) (SDKMessage, error) {
	var head struct {
		Type    string `json:"type"`
		Subtype string `json:"subtype"`
		IsError *bool  `json:"is_error,omitempty"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return nil, fmt.Errorf("sdkproto: peek discriminator: %w", err)
	}

	switch head.Type {
	case TypeUser:
		var m SDKUserMessage
		return &m, json.Unmarshal(data, &m)
	case TypeAssistant:
		var m SDKAssistantMessage
		return &m, json.Unmarshal(data, &m)
	case TypeStreamEvent:
		var m SDKPartialAssistantMessage
		return &m, json.Unmarshal(data, &m)
	case TypeResult:
		if head.IsError != nil && *head.IsError {
			var m SDKResultError
			return &m, json.Unmarshal(data, &m)
		}
		var m SDKResultSuccess
		return &m, json.Unmarshal(data, &m)
	case TypeAuthStatus:
		var m SDKAuthStatus
		return &m, json.Unmarshal(data, &m)
	case TypeRateLimitEvent:
		var m SDKRateLimitEvent
		return &m, json.Unmarshal(data, &m)
	case TypePromptSuggest:
		var m SDKPromptSuggestion
		return &m, json.Unmarshal(data, &m)
	case TypeToolProgress:
		var m SDKToolProgress
		return &m, json.Unmarshal(data, &m)
	case TypeToolUseSummary:
		var m SDKToolUseSummary
		return &m, json.Unmarshal(data, &m)
	case TypeStreamlinedTxt:
		var m SDKStreamlinedText
		return &m, json.Unmarshal(data, &m)
	case TypeStreamlinedTUS:
		var m SDKStreamlinedToolUseSummary
		return &m, json.Unmarshal(data, &m)
	case TypeSystem:
		return unmarshalSystem(head.Subtype, data)
	default:
		return nil, fmt.Errorf("sdkproto: unknown type %q", head.Type)
	}
}

func unmarshalSystem(subtype string, data []byte) (SDKMessage, error) {
	switch subtype {
	case SubtypeInit:
		var m SDKSystemInit
		return &m, json.Unmarshal(data, &m)
	case SubtypeStatus:
		var m SDKSystemStatus
		return &m, json.Unmarshal(data, &m)
	case SubtypeCompactBoundary:
		var m SDKCompactBoundary
		return &m, json.Unmarshal(data, &m)
	case SubtypeAPIRetry:
		var m SDKAPIRetry
		return &m, json.Unmarshal(data, &m)
	case SubtypeLocalCommandOutput:
		var m SDKLocalCommandOutput
		return &m, json.Unmarshal(data, &m)
	case SubtypeHookStarted:
		var m SDKHookStarted
		return &m, json.Unmarshal(data, &m)
	case SubtypeHookProgress:
		var m SDKHookProgress
		return &m, json.Unmarshal(data, &m)
	case SubtypeHookResponse:
		var m SDKHookResponse
		return &m, json.Unmarshal(data, &m)
	case SubtypeFilesPersisted:
		var m SDKFilesPersisted
		return &m, json.Unmarshal(data, &m)
	case SubtypeTaskNotification:
		var m SDKTaskNotification
		return &m, json.Unmarshal(data, &m)
	case SubtypeTaskStarted:
		var m SDKTaskStarted
		return &m, json.Unmarshal(data, &m)
	case SubtypeTaskProgress:
		var m SDKTaskProgress
		return &m, json.Unmarshal(data, &m)
	case SubtypeSessionStateChanged:
		var m SDKSessionStateChanged
		return &m, json.Unmarshal(data, &m)
	case SubtypeElicitationComplete:
		var m SDKElicitationComplete
		return &m, json.Unmarshal(data, &m)
	case SubtypePostTurnSummary:
		var m SDKPostTurnSummary
		return &m, json.Unmarshal(data, &m)
	default:
		return nil, fmt.Errorf("sdkproto: unknown system subtype %q", subtype)
	}
}
