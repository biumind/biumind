package sdkproto

import (
	"encoding/json"
	"fmt"
)

// Frame type 字面量。
const (
	TypeControlRequest       = "control_request"
	TypeControlResponse      = "control_response"
	TypeControlCancelRequest = "control_cancel_request"

	ControlSubtypeSuccess = "success"
	ControlSubtypeError   = "error"

	SubtypeInitialize           = "initialize"
	SubtypeInterrupt            = "interrupt"
	SubtypeCanUseTool           = "can_use_tool"
	SubtypeSetModel             = "set_model"
	SubtypeSetPermissionMode    = "set_permission_mode"
	SubtypeSetMaxThinkingTokens = "set_max_thinking_tokens"
	SubtypeMcpStatus            = "mcp_status"
	SubtypeMcpMessage           = "mcp_message"
	SubtypeMcpSetServers        = "mcp_set_servers"
	SubtypeMcpReconnect         = "mcp_reconnect"
	SubtypeMcpToggle            = "mcp_toggle"
	SubtypeGetContextUsage      = "get_context_usage"
	SubtypeHookCallback         = "hook_callback"
	SubtypeRewindFiles          = "rewind_files"
	SubtypeCancelAsyncMessage   = "cancel_async_message"
	SubtypeSeedReadState        = "seed_read_state"
	SubtypeReloadPlugins        = "reload_plugins"
	SubtypeStopTask             = "stop_task"
	SubtypeApplyFlagSettings    = "apply_flag_settings"
	SubtypeGetSettings          = "get_settings"
	SubtypeElicitation          = "elicitation"
)

// SDKControlRequest 包了 type=control_request 帧。Request 是 ControlRequestInner 的具体实现。
type SDKControlRequest struct {
	Type      string              `json:"type"` // "control_request"
	RequestID string              `json:"request_id"`
	Request   ControlRequestInner `json:"request"`
}

// MarshalJSON: Request 字段是接口 — Go 默认能序列化指针接口，但我们要确保
// type/request_id 字段名跟 schema 对得上。这里走默认编码即可。
//
// UnmarshalJSON 才需要 dispatch — 接口字段必须先 peek subtype 再 dispatch 类型。
func (r *SDKControlRequest) UnmarshalJSON(data []byte) error {
	var head struct {
		Type      string          `json:"type"`
		RequestID string          `json:"request_id"`
		Request   json.RawMessage `json:"request"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return fmt.Errorf("control_request: peek: %w", err)
	}
	if head.Type != TypeControlRequest {
		return fmt.Errorf("control_request: type=%q, want %q", head.Type, TypeControlRequest)
	}
	inner, err := UnmarshalControlRequestInner(head.Request)
	if err != nil {
		return err
	}
	r.Type = head.Type
	r.RequestID = head.RequestID
	r.Request = inner
	return nil
}

// UnmarshalControlRequestInner: peek subtype 后 dispatch 到具体 *Type。
func UnmarshalControlRequestInner(data []byte) (ControlRequestInner, error) {
	var head struct {
		Subtype string `json:"subtype"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return nil, fmt.Errorf("control: peek subtype: %w", err)
	}
	var v ControlRequestInner
	switch head.Subtype {
	case SubtypeInitialize:
		v = &Initialize{}
	case SubtypeInterrupt:
		v = &Interrupt{}
	case SubtypeCanUseTool:
		v = &PermissionRequest{}
	case SubtypeSetModel:
		v = &SetModel{}
	case SubtypeSetPermissionMode:
		v = &SetPermissionMode{}
	case SubtypeSetMaxThinkingTokens:
		v = &SetMaxThinkingTokens{}
	case SubtypeMcpStatus:
		v = &McpStatus{}
	case SubtypeMcpMessage:
		v = &McpMessage{}
	case SubtypeMcpSetServers:
		v = &McpSetServers{}
	case SubtypeMcpReconnect:
		v = &McpReconnect{}
	case SubtypeMcpToggle:
		v = &McpToggle{}
	case SubtypeGetContextUsage:
		v = &GetContextUsage{}
	case SubtypeHookCallback:
		v = &HookCallback{}
	case SubtypeRewindFiles:
		v = &RewindFiles{}
	case SubtypeCancelAsyncMessage:
		v = &CancelAsyncMessage{}
	case SubtypeSeedReadState:
		v = &SeedReadState{}
	case SubtypeReloadPlugins:
		v = &ReloadPlugins{}
	case SubtypeStopTask:
		v = &StopTask{}
	case SubtypeApplyFlagSettings:
		v = &ApplyFlagSettings{}
	case SubtypeGetSettings:
		v = &GetSettings{}
	case SubtypeElicitation:
		v = &Elicitation{}
	default:
		return nil, fmt.Errorf("control: unknown subtype %q", head.Subtype)
	}
	if err := json.Unmarshal(data, v); err != nil {
		return nil, fmt.Errorf("control: unmarshal %s: %w", head.Subtype, err)
	}
	return v, nil
}

// 三个 control wrapper 都是合法 Frame —— 实现 isFrame()。
func (*SDKControlRequest) isFrame()       {}
func (*SDKControlResponse) isFrame()      {}
func (*SDKControlCancelRequest) isFrame() {}

// SDKControlResponse 包了 type=control_response 帧。
type SDKControlResponse struct {
	Type     string               `json:"type"` // "control_response"
	Response *ControlResponseBody `json:"response"`
}

// ControlResponseBody 是 success/error 的两态联合 — 用单一 struct 简化 marshal。
// subtype="success" 时填 Response；subtype="error" 时填 Error / PendingPermissionRequests。
type ControlResponseBody struct {
	Subtype                   string             `json:"subtype"`
	RequestID                 string             `json:"request_id"`
	Response                  json.RawMessage    `json:"response,omitempty"`
	Error                     string             `json:"error,omitempty"`
	PendingPermissionRequests []SDKControlRequest `json:"pending_permission_requests,omitempty"`
}

// SDKControlCancelRequest 是单独的 type=control_cancel_request 帧 —— 取消正在处理的 control request。
// 注意跟 CancelAsyncMessage（subtype 内部 ControlRequestInner）不一样。
type SDKControlCancelRequest struct {
	Type      string `json:"type"` // "control_cancel_request"
	RequestID string `json:"request_id"`
}
