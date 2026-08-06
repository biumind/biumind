package sdkproto

import "encoding/json"

// 21 个 ControlRequestInner — 每个用 subtype 区分。字段名严格对齐上游 schema。

// ControlRequestInner 是所有 21 个 control request body 的标记接口。
type ControlRequestInner interface {
	isControlRequestInner()
	// Subtype 返回 subtype 字面量；用于 dispatch + 调试。
	Subtype() string
}

// ── initialize ──────────────────────────────────────────────

type Initialize struct {
	SubtypeF               string          `json:"subtype"` // "initialize"
	Hooks                  json.RawMessage `json:"hooks,omitempty"`
	SDKMcpServers          json.RawMessage `json:"sdkMcpServers,omitempty"`
	JSONSchema             json.RawMessage `json:"jsonSchema,omitempty"`
	SystemPrompt           string          `json:"systemPrompt,omitempty"`
	AppendSystemPrompt     string          `json:"appendSystemPrompt,omitempty"`
	Agents                 json.RawMessage `json:"agents,omitempty"`
	PromptSuggestions      *bool           `json:"promptSuggestions,omitempty"`
	AgentProgressSummaries *bool           `json:"agentProgressSummaries,omitempty"`
}

func (*Initialize) isControlRequestInner() {}
func (*Initialize) Subtype() string         { return "initialize" }

// ── interrupt ───────────────────────────────────────────────

type Interrupt struct {
	SubtypeF string `json:"subtype"` // "interrupt"
}

func (*Interrupt) isControlRequestInner() {}
func (*Interrupt) Subtype() string         { return "interrupt" }

// ── permission (can_use_tool) ───────────────────────────────

type PermissionRequest struct {
	SubtypeF              string          `json:"subtype"` // "can_use_tool"
	ToolName              string          `json:"tool_name"`
	Input                 json.RawMessage `json:"input"`
	PermissionSuggestions json.RawMessage `json:"permission_suggestions,omitempty"`
	BlockedPath           string          `json:"blocked_path,omitempty"`
	DecisionReason        string          `json:"decision_reason,omitempty"`
	Title                 string          `json:"title,omitempty"`
	DisplayName           string          `json:"display_name,omitempty"`
	ToolUseID             string          `json:"tool_use_id"`
	AgentID               string          `json:"agent_id,omitempty"`
	Description           string          `json:"description,omitempty"`
}

func (*PermissionRequest) isControlRequestInner() {}
func (*PermissionRequest) Subtype() string         { return "can_use_tool" }

// ── set_model / set_permission_mode / set_max_thinking_tokens ─

type SetModel struct {
	SubtypeF string `json:"subtype"` // "set_model"
	Model    string `json:"model,omitempty"`
}

func (*SetModel) isControlRequestInner() {}
func (*SetModel) Subtype() string         { return "set_model" }

type SetPermissionMode struct {
	SubtypeF  string `json:"subtype"` // "set_permission_mode"
	Mode      string `json:"mode"`
	Ultraplan *bool  `json:"ultraplan,omitempty"`
}

func (*SetPermissionMode) isControlRequestInner() {}
func (*SetPermissionMode) Subtype() string         { return "set_permission_mode" }

type SetMaxThinkingTokens struct {
	SubtypeF          string `json:"subtype"` // "set_max_thinking_tokens"
	MaxThinkingTokens *int   `json:"max_thinking_tokens"`
}

func (*SetMaxThinkingTokens) isControlRequestInner() {}
func (*SetMaxThinkingTokens) Subtype() string         { return "set_max_thinking_tokens" }

// ── mcp_status / mcp_message / mcp_set_servers / mcp_reconnect / mcp_toggle ─

type McpStatus struct {
	SubtypeF string `json:"subtype"` // "mcp_status"
}

func (*McpStatus) isControlRequestInner() {}
func (*McpStatus) Subtype() string         { return "mcp_status" }

type McpMessage struct {
	SubtypeF   string          `json:"subtype"` // "mcp_message"
	ServerName string          `json:"server_name"`
	Message    json.RawMessage `json:"message"`
}

func (*McpMessage) isControlRequestInner() {}
func (*McpMessage) Subtype() string         { return "mcp_message" }

type McpSetServers struct {
	SubtypeF string                     `json:"subtype"` // "mcp_set_servers"
	Servers  map[string]json.RawMessage `json:"servers"`
}

func (*McpSetServers) isControlRequestInner() {}
func (*McpSetServers) Subtype() string         { return "mcp_set_servers" }

type McpReconnect struct {
	SubtypeF   string `json:"subtype"` // "mcp_reconnect"
	ServerName string `json:"serverName"`
}

func (*McpReconnect) isControlRequestInner() {}
func (*McpReconnect) Subtype() string         { return "mcp_reconnect" }

type McpToggle struct {
	SubtypeF   string `json:"subtype"` // "mcp_toggle"
	ServerName string `json:"serverName"`
	Enabled    bool   `json:"enabled"`
}

func (*McpToggle) isControlRequestInner() {}
func (*McpToggle) Subtype() string         { return "mcp_toggle" }

// ── get_context_usage ───────────────────────────────────────

type GetContextUsage struct {
	SubtypeF string `json:"subtype"` // "get_context_usage"
}

func (*GetContextUsage) isControlRequestInner() {}
func (*GetContextUsage) Subtype() string         { return "get_context_usage" }

// ── hook_callback ───────────────────────────────────────────

type HookCallback struct {
	SubtypeF   string          `json:"subtype"` // "hook_callback"
	CallbackID string          `json:"callback_id"`
	Input      json.RawMessage `json:"input"`
	ToolUseID  string          `json:"tool_use_id,omitempty"`
}

func (*HookCallback) isControlRequestInner() {}
func (*HookCallback) Subtype() string         { return "hook_callback" }

// ── rewind_files ────────────────────────────────────────────

type RewindFiles struct {
	SubtypeF      string `json:"subtype"` // "rewind_files"
	UserMessageID string `json:"user_message_id"`
	DryRun        *bool  `json:"dry_run,omitempty"`
}

func (*RewindFiles) isControlRequestInner() {}
func (*RewindFiles) Subtype() string         { return "rewind_files" }

// ── cancel_async_message ────────────────────────────────────

type CancelAsyncMessage struct {
	SubtypeF    string `json:"subtype"` // "cancel_async_message"
	MessageUUID string `json:"message_uuid"`
}

func (*CancelAsyncMessage) isControlRequestInner() {}
func (*CancelAsyncMessage) Subtype() string         { return "cancel_async_message" }

// ── seed_read_state ─────────────────────────────────────────

type SeedReadState struct {
	SubtypeF string  `json:"subtype"` // "seed_read_state"
	Path     string  `json:"path"`
	Mtime    float64 `json:"mtime"`
}

func (*SeedReadState) isControlRequestInner() {}
func (*SeedReadState) Subtype() string         { return "seed_read_state" }

// ── reload_plugins ──────────────────────────────────────────

type ReloadPlugins struct {
	SubtypeF string `json:"subtype"` // "reload_plugins"
}

func (*ReloadPlugins) isControlRequestInner() {}
func (*ReloadPlugins) Subtype() string         { return "reload_plugins" }

// ── stop_task ───────────────────────────────────────────────

type StopTask struct {
	SubtypeF string `json:"subtype"` // "stop_task"
	TaskID   string `json:"task_id"`
}

func (*StopTask) isControlRequestInner() {}
func (*StopTask) Subtype() string         { return "stop_task" }

// ── apply_flag_settings / get_settings ──────────────────────

type ApplyFlagSettings struct {
	SubtypeF string                     `json:"subtype"` // "apply_flag_settings"
	Settings map[string]json.RawMessage `json:"settings"`
}

func (*ApplyFlagSettings) isControlRequestInner() {}
func (*ApplyFlagSettings) Subtype() string         { return "apply_flag_settings" }

type GetSettings struct {
	SubtypeF string `json:"subtype"` // "get_settings"
}

func (*GetSettings) isControlRequestInner() {}
func (*GetSettings) Subtype() string         { return "get_settings" }

// ── elicitation ─────────────────────────────────────────────

type Elicitation struct {
	SubtypeF        string          `json:"subtype"` // "elicitation"
	McpServerName   string          `json:"mcp_server_name"`
	Message         string          `json:"message"`
	Mode            string          `json:"mode,omitempty"` // form | url
	URL             string          `json:"url,omitempty"`
	ElicitationID   string          `json:"elicitation_id,omitempty"`
	RequestedSchema json.RawMessage `json:"requested_schema,omitempty"`
}

func (*Elicitation) isControlRequestInner() {}
func (*Elicitation) Subtype() string         { return "elicitation" }
