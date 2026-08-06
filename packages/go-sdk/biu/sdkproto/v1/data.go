package sdkproto

import "encoding/json"

// 数据平面 28 个 SDKMessage variant — 跟 schema/sdk/v1/data/*.json 一一对应。
// 字段顺序、命名、可选性均严格跟上游 schema 对齐。

// ── user ────────────────────────────────────────────────────

type SDKUserMessage struct {
	Type            string           `json:"type"` // "user"
	Message         AnthropicMessage `json:"message"`
	ParentToolUseID *string          `json:"parent_tool_use_id,omitempty"`
	IsSynthetic     *bool            `json:"isSynthetic,omitempty"`
	ToolUseResult   json.RawMessage  `json:"tool_use_result,omitempty"`
	Priority        string           `json:"priority,omitempty"`
	Timestamp       *int64           `json:"timestamp,omitempty"`
	UUID            string           `json:"uuid,omitempty"`
	SessionID       string           `json:"session_id,omitempty"`
	IsReplay        *bool            `json:"isReplay,omitempty"` // SDKUserMessageReplay 时为 true
}

// ── assistant ───────────────────────────────────────────────

type SDKAssistantMessage struct {
	Type            string           `json:"type"` // "assistant"
	Message         AnthropicMessage `json:"message"`
	ParentToolUseID *string          `json:"parent_tool_use_id,omitempty"`
	Error           *string          `json:"error,omitempty"`
	UUID            string           `json:"uuid"`
	SessionID       string           `json:"session_id"`
}

type SDKPartialAssistantMessage struct {
	Type            string          `json:"type"` // "stream_event"
	Event           json.RawMessage `json:"event"`
	ParentToolUseID *string         `json:"parent_tool_use_id,omitempty"`
	UUID            string          `json:"uuid"`
	SessionID       string          `json:"session_id"`
}

// ── tool ────────────────────────────────────────────────────

type SDKToolProgress struct {
	Type               string  `json:"type"` // "tool_progress"
	ToolUseID          string  `json:"tool_use_id"`
	ToolName           string  `json:"tool_name"`
	ParentToolUseID    *string `json:"parent_tool_use_id,omitempty"`
	ElapsedTimeSeconds float64 `json:"elapsed_time_seconds"`
	TaskID             string  `json:"task_id,omitempty"`
	UUID               string  `json:"uuid"`
	SessionID          string  `json:"session_id"`
}

type SDKToolUseSummary struct {
	Type                string   `json:"type"` // "tool_use_summary"
	Summary             string   `json:"summary"`
	PrecedingToolUseIDs []string `json:"preceding_tool_use_ids"`
	UUID                string   `json:"uuid"`
	SessionID           string   `json:"session_id"`
}

// ── result ──────────────────────────────────────────────────

// SDKResultSuccess: type=result, subtype=success, is_error=false.
type SDKResultSuccess struct {
	Type              string                `json:"type"`    // "result"
	Subtype           string                `json:"subtype"` // "success"
	DurationMs        int                   `json:"duration_ms"`
	DurationAPIMs     int                   `json:"duration_api_ms"`
	IsError           bool                  `json:"is_error"` // false
	NumTurns          int                   `json:"num_turns"`
	Result            string                `json:"result"`
	StopReason        *string               `json:"stop_reason"`
	TotalCostUSD      float64               `json:"total_cost_usd"`
	Usage             NonNullableUsage      `json:"usage"`
	ModelUsage        map[string]ModelUsage `json:"modelUsage"`
	PermissionDenials []json.RawMessage     `json:"permission_denials"`
	StructuredOutput  json.RawMessage       `json:"structured_output,omitempty"`
	FastModeState     string                `json:"fast_mode_state,omitempty"`
	UUID              string                `json:"uuid"`
	SessionID         string                `json:"session_id"`
}

// SDKResultError: type=result, subtype=error_*, is_error=true.
type SDKResultError struct {
	Type              string                `json:"type"`    // "result"
	Subtype           string                `json:"subtype"` // "error_*"
	DurationMs        int                   `json:"duration_ms"`
	DurationAPIMs     int                   `json:"duration_api_ms"`
	IsError           bool                  `json:"is_error"` // true
	NumTurns          int                   `json:"num_turns"`
	TotalCostUSD      float64               `json:"total_cost_usd"`
	Usage             NonNullableUsage      `json:"usage"`
	ModelUsage        map[string]ModelUsage `json:"modelUsage"`
	PermissionDenials []json.RawMessage     `json:"permission_denials"`
	Errors            []json.RawMessage     `json:"errors"`
	StopReason        *string               `json:"stop_reason,omitempty"`
	FastModeState     string                `json:"fast_mode_state,omitempty"`
	UUID              string                `json:"uuid"`
	SessionID         string                `json:"session_id"`
}

// ── system: init ────────────────────────────────────────────

type SDKSystemInit struct {
	Type              string            `json:"type"`    // "system"
	Subtype           string            `json:"subtype"` // "init"
	Agents            []json.RawMessage `json:"agents"`
	APIKeySource      string            `json:"apiKeySource"`
	Betas             []string          `json:"betas"`
	ClaudeCodeVersion string            `json:"claude_code_version"`
	Cwd               string            `json:"cwd"`
	Tools             []string          `json:"tools"`
	McpServers        []json.RawMessage `json:"mcp_servers"`
	Model             string            `json:"model"`
	PermissionMode    string            `json:"permissionMode"`
	SlashCommands     []json.RawMessage `json:"slash_commands"`
	OutputStyle       string            `json:"output_style"`
	Skills            []json.RawMessage `json:"skills,omitempty"`
	Plugins           []json.RawMessage `json:"plugins,omitempty"`
	FastModeState     string            `json:"fast_mode_state,omitempty"`
	UUID              string            `json:"uuid"`
	SessionID         string            `json:"session_id"`
}

// ── system: status ──────────────────────────────────────────

type SDKSystemStatus struct {
	Type           string  `json:"type"`    // "system"
	Subtype        string  `json:"subtype"` // "status"
	Status         *string `json:"status,omitempty"`
	PermissionMode string  `json:"permissionMode,omitempty"`
	UUID           string  `json:"uuid"`
	SessionID      string  `json:"session_id"`
}

// ── system: compact_boundary ────────────────────────────────

type CompactMetadata struct {
	Trigger          string          `json:"trigger"`
	PreTokens        int             `json:"pre_tokens"`
	PreservedSegment json.RawMessage `json:"preserved_segment,omitempty"`
}

type SDKCompactBoundary struct {
	Type            string          `json:"type"`    // "system"
	Subtype         string          `json:"subtype"` // "compact_boundary"
	CompactMetadata CompactMetadata `json:"compact_metadata"`
	UUID            string          `json:"uuid"`
	SessionID       string          `json:"session_id"`
}

// ── system: api_retry ───────────────────────────────────────

type SDKAPIRetry struct {
	Type          string          `json:"type"`    // "system"
	Subtype       string          `json:"subtype"` // "api_retry"
	Attempt       int             `json:"attempt"`
	MaxRetries    int             `json:"max_retries"`
	RetryDelayMs  int             `json:"retry_delay_ms"`
	ErrorStatus   json.RawMessage `json:"error_status,omitempty"`
	Error         string          `json:"error"`
	UUID          string          `json:"uuid"`
	SessionID     string          `json:"session_id"`
}

// ── system: local_command_output ────────────────────────────

type SDKLocalCommandOutput struct {
	Type      string `json:"type"`    // "system"
	Subtype   string `json:"subtype"` // "local_command_output"
	Content   string `json:"content"`
	UUID      string `json:"uuid"`
	SessionID string `json:"session_id"`
}

// ── system: hook_started / hook_progress / hook_response ────

type SDKHookStarted struct {
	Type      string `json:"type"`    // "system"
	Subtype   string `json:"subtype"` // "hook_started"
	HookID    string `json:"hook_id"`
	HookName  string `json:"hook_name"`
	HookEvent string `json:"hook_event"`
	UUID      string `json:"uuid"`
	SessionID string `json:"session_id"`
}

type SDKHookProgress struct {
	Type      string `json:"type"`    // "system"
	Subtype   string `json:"subtype"` // "hook_progress"
	HookID    string `json:"hook_id"`
	HookName  string `json:"hook_name,omitempty"`
	HookEvent string `json:"hook_event,omitempty"`
	Stdout    string `json:"stdout,omitempty"`
	Stderr    string `json:"stderr,omitempty"`
	Output    string `json:"output,omitempty"`
	UUID      string `json:"uuid"`
	SessionID string `json:"session_id"`
}

type SDKHookResponse struct {
	Type      string `json:"type"`    // "system"
	Subtype   string `json:"subtype"` // "hook_response"
	HookID    string `json:"hook_id"`
	HookName  string `json:"hook_name,omitempty"`
	HookEvent string `json:"hook_event,omitempty"`
	ExitCode  *int   `json:"exit_code,omitempty"`
	Outcome   string `json:"outcome"` // success | error | cancelled
	Stdout    string `json:"stdout,omitempty"`
	Stderr    string `json:"stderr,omitempty"`
	Output    string `json:"output,omitempty"`
	UUID      string `json:"uuid"`
	SessionID string `json:"session_id"`
}

// ── auth_status (not subtype'd under system) ────────────────

type SDKAuthStatus struct {
	Type             string   `json:"type"` // "auth_status"
	IsAuthenticating bool     `json:"isAuthenticating"`
	Output           []string `json:"output"`
	Error            *string  `json:"error,omitempty"`
	UUID             string   `json:"uuid"`
	SessionID        string   `json:"session_id"`
}

// ── system: files_persisted ─────────────────────────────────

type SDKFilesPersisted struct {
	Type        string            `json:"type"`    // "system"
	Subtype     string            `json:"subtype"` // "files_persisted"
	Files       []string          `json:"files"`
	Failed      []json.RawMessage `json:"failed"`
	ProcessedAt int64             `json:"processed_at"`
	UUID        string            `json:"uuid"`
	SessionID   string            `json:"session_id"`
}

// ── system: task_notification / task_started / task_progress ─

type SDKTaskNotification struct {
	Type       string          `json:"type"`    // "system"
	Subtype    string          `json:"subtype"` // "task_notification"
	TaskID     string          `json:"task_id"`
	ToolUseID  string          `json:"tool_use_id,omitempty"`
	Status     string          `json:"status"`
	OutputFile string          `json:"output_file"`
	Summary    string          `json:"summary"`
	Usage      json.RawMessage `json:"usage,omitempty"`
	UUID       string          `json:"uuid"`
	SessionID  string          `json:"session_id"`
}

type SDKTaskStarted struct {
	Type         string `json:"type"`    // "system"
	Subtype      string `json:"subtype"` // "task_started"
	TaskID       string `json:"task_id"`
	ToolUseID    string `json:"tool_use_id,omitempty"`
	Description  string `json:"description"`
	TaskType     string `json:"task_type,omitempty"`
	WorkflowName string `json:"workflow_name,omitempty"`
	Prompt       string `json:"prompt,omitempty"`
	UUID         string `json:"uuid"`
	SessionID    string `json:"session_id"`
}

type SDKTaskProgress struct {
	Type         string `json:"type"`    // "system"
	Subtype      string `json:"subtype"` // "task_progress"
	TaskID       string `json:"task_id"`
	LastToolName string `json:"last_tool_name,omitempty"`
	Summary      string `json:"summary,omitempty"`
	UUID         string `json:"uuid"`
	SessionID    string `json:"session_id"`
}

// ── system: session_state_changed ───────────────────────────

type SDKSessionStateChanged struct {
	Type      string `json:"type"`    // "system"
	Subtype   string `json:"subtype"` // "session_state_changed"
	State     string `json:"state"`   // idle | running | requires_action
	UUID      string `json:"uuid"`
	SessionID string `json:"session_id"`
}

// ── rate_limit_event ────────────────────────────────────────

type SDKRateLimitEvent struct {
	Type          string         `json:"type"` // "rate_limit_event"
	RateLimitInfo RateLimitInfo  `json:"rate_limit_info"`
	UUID          string         `json:"uuid"`
	SessionID     string         `json:"session_id"`
}

// ── system: elicitation_complete ────────────────────────────

type SDKElicitationComplete struct {
	Type          string `json:"type"`    // "system"
	Subtype       string `json:"subtype"` // "elicitation_complete"
	McpServerName string `json:"mcp_server_name"`
	ElicitationID string `json:"elicitation_id"`
	UUID          string `json:"uuid"`
	SessionID     string `json:"session_id"`
}

// ── prompt_suggestion ───────────────────────────────────────

type SDKPromptSuggestion struct {
	Type       string `json:"type"` // "prompt_suggestion"
	Suggestion string `json:"suggestion"`
	UUID       string `json:"uuid"`
	SessionID  string `json:"session_id"`
}

// ── streamlined ─────────────────────────────────────────────

type SDKStreamlinedText struct {
	Type      string `json:"type"` // "streamlined_text"
	Text      string `json:"text"`
	UUID      string `json:"uuid"`
	SessionID string `json:"session_id"`
}

type SDKStreamlinedToolUseSummary struct {
	Type        string `json:"type"` // "streamlined_tool_use_summary"
	ToolSummary string `json:"tool_summary"`
	UUID        string `json:"uuid"`
	SessionID   string `json:"session_id"`
}

// ── post_turn_summary (subtype'd under system) ──────────────

type SDKPostTurnSummary struct {
	Type           string   `json:"type"`    // "system"
	Subtype        string   `json:"subtype"` // "post_turn_summary"
	SummarizesUUID string   `json:"summarizes_uuid"`
	StatusCategory string   `json:"status_category"`
	StatusDetail   string   `json:"status_detail"`
	IsNoteworthy   bool     `json:"is_noteworthy"`
	Title          string   `json:"title"`
	Description    string   `json:"description"`
	RecentAction   string   `json:"recent_action"`
	NeedsAction    bool     `json:"needs_action"`
	ArtifactURLs   []string `json:"artifact_urls"`
	UUID           string   `json:"uuid"`
	SessionID      string   `json:"session_id"`
}
