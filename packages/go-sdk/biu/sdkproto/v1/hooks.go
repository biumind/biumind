package sdkproto

import (
	"encoding/json"
	"fmt"
)

// 27 个 HookEvent 字面量 — 跟 schema/sdk/v1/hooks/events.json HookEvent enum 对齐。
const (
	HookPreToolUse         = "PreToolUse"
	HookPostToolUse        = "PostToolUse"
	HookPostToolUseFailure = "PostToolUseFailure"
	HookNotification       = "Notification"
	HookUserPromptSubmit   = "UserPromptSubmit"
	HookSessionStart       = "SessionStart"
	HookSessionEnd         = "SessionEnd"
	HookStop               = "Stop"
	HookStopFailure        = "StopFailure"
	HookSubagentStart      = "SubagentStart"
	HookSubagentStop       = "SubagentStop"
	HookPreCompact         = "PreCompact"
	HookPostCompact        = "PostCompact"
	HookPermissionRequest  = "PermissionRequest"
	HookPermissionDenied   = "PermissionDenied"
	HookSetup              = "Setup"
	HookTeammateIdle       = "TeammateIdle"
	HookTaskCreated        = "TaskCreated"
	HookTaskCompleted      = "TaskCompleted"
	HookElicitation        = "Elicitation"
	HookElicitationResult  = "ElicitationResult"
	HookConfigChange       = "ConfigChange"
	HookWorktreeCreate     = "WorktreeCreate"
	HookWorktreeRemove     = "WorktreeRemove"
	HookInstructionsLoaded = "InstructionsLoaded"
	HookCwdChanged         = "CwdChanged"
	HookFileChanged        = "FileChanged"
)

// BaseHookInput 是 27 个 hook variant 共用的字段。Go 用嵌入而不是 allOf。
type BaseHookInput struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	Cwd            string `json:"cwd"`
	HookEventName  string `json:"hook_event_name"`
	PermissionMode string `json:"permission_mode,omitempty"`
	AgentID        string `json:"agent_id,omitempty"`
	AgentType      string `json:"agent_type,omitempty"`
}

// HookInput 是 27 个 variant 的标记接口。
type HookInput interface {
	isHookInput()
	HookEvent() string
}

// ── tool_use ────────────────────────────────────────────────

type PreToolUse struct {
	BaseHookInput
	ToolName   string          `json:"tool_name"`
	ToolInput  json.RawMessage `json:"tool_input"`
	ToolUseID  string          `json:"tool_use_id"`
}

func (*PreToolUse) isHookInput()       {}
func (*PreToolUse) HookEvent() string  { return HookPreToolUse }

type PostToolUse struct {
	BaseHookInput
	ToolName     string          `json:"tool_name"`
	ToolInput    json.RawMessage `json:"tool_input"`
	ToolResponse json.RawMessage `json:"tool_response"`
	ToolUseID    string          `json:"tool_use_id"`
}

func (*PostToolUse) isHookInput()      {}
func (*PostToolUse) HookEvent() string { return HookPostToolUse }

type PostToolUseFailure struct {
	BaseHookInput
	ToolName    string          `json:"tool_name"`
	ToolInput   json.RawMessage `json:"tool_input"`
	ToolUseID   string          `json:"tool_use_id"`
	Error       string          `json:"error"`
	IsInterrupt *bool           `json:"is_interrupt,omitempty"`
}

func (*PostToolUseFailure) isHookInput()      {}
func (*PostToolUseFailure) HookEvent() string { return HookPostToolUseFailure }

type PermissionDenied struct {
	BaseHookInput
	ToolName  string          `json:"tool_name"`
	ToolInput json.RawMessage `json:"tool_input"`
	ToolUseID string          `json:"tool_use_id"`
	Reason    string          `json:"reason"`
}

func (*PermissionDenied) isHookInput()      {}
func (*PermissionDenied) HookEvent() string { return HookPermissionDenied }

// ── permission ──────────────────────────────────────────────

// PermissionRequestHook: hook 通道里的 PermissionRequest。
// 注意跟 control/permission.go 的 PermissionRequest（顶层 control request）不同载体。
type PermissionRequestHook struct {
	BaseHookInput
	ToolName              string          `json:"tool_name"`
	ToolInput             json.RawMessage `json:"tool_input"`
	PermissionSuggestions json.RawMessage `json:"permission_suggestions,omitempty"`
}

func (*PermissionRequestHook) isHookInput()      {}
func (*PermissionRequestHook) HookEvent() string { return HookPermissionRequest }

// ── notification ────────────────────────────────────────────

type Notification struct {
	BaseHookInput
	Message          string `json:"message"`
	Title            string `json:"title,omitempty"`
	NotificationType string `json:"notification_type"`
}

func (*Notification) isHookInput()      {}
func (*Notification) HookEvent() string { return HookNotification }

type UserPromptSubmit struct {
	BaseHookInput
	Prompt string `json:"prompt"`
}

func (*UserPromptSubmit) isHookInput()      {}
func (*UserPromptSubmit) HookEvent() string { return HookUserPromptSubmit }

// ── session ─────────────────────────────────────────────────

type SessionStart struct {
	BaseHookInput
	Source        string `json:"source"`
	HookAgentType string `json:"agent_type,omitempty"` // 字段在 BaseHookInput 也有；session_start 时复制一份
	Model         string `json:"model,omitempty"`
}

func (*SessionStart) isHookInput()      {}
func (*SessionStart) HookEvent() string { return HookSessionStart }

type SessionEnd struct {
	BaseHookInput
	Reason string `json:"reason"`
}

func (*SessionEnd) isHookInput()      {}
func (*SessionEnd) HookEvent() string { return HookSessionEnd }

type Stop struct {
	BaseHookInput
	StopHookActive       bool   `json:"stop_hook_active"`
	LastAssistantMessage string `json:"last_assistant_message,omitempty"`
}

func (*Stop) isHookInput()      {}
func (*Stop) HookEvent() string { return HookStop }

type StopFailure struct {
	BaseHookInput
	Error                string          `json:"error"`
	ErrorDetails         json.RawMessage `json:"error_details,omitempty"`
	LastAssistantMessage string          `json:"last_assistant_message,omitempty"`
}

func (*StopFailure) isHookInput()      {}
func (*StopFailure) HookEvent() string { return HookStopFailure }

type Setup struct {
	BaseHookInput
	Trigger string `json:"trigger"` // init | maintenance
}

func (*Setup) isHookInput()      {}
func (*Setup) HookEvent() string { return HookSetup }

// ── subagent ────────────────────────────────────────────────

type SubagentStart struct {
	BaseHookInput
	HookAgentID   string `json:"agent_id"`
	HookAgentType string `json:"agent_type"`
}

func (*SubagentStart) isHookInput()      {}
func (*SubagentStart) HookEvent() string { return HookSubagentStart }

type SubagentStop struct {
	BaseHookInput
	StopHookActive       bool   `json:"stop_hook_active"`
	HookAgentID          string `json:"agent_id"`
	AgentTranscriptPath  string `json:"agent_transcript_path"`
	HookAgentType        string `json:"agent_type"`
	LastAssistantMessage string `json:"last_assistant_message,omitempty"`
}

func (*SubagentStop) isHookInput()      {}
func (*SubagentStop) HookEvent() string { return HookSubagentStop }

type TeammateIdle struct {
	BaseHookInput
	TeammateName string `json:"teammate_name"`
	TeamName     string `json:"team_name"`
}

func (*TeammateIdle) isHookInput()      {}
func (*TeammateIdle) HookEvent() string { return HookTeammateIdle }

// ── compact ─────────────────────────────────────────────────

type PreCompact struct {
	BaseHookInput
	Trigger            string `json:"trigger"`
	CustomInstructions string `json:"custom_instructions"`
}

func (*PreCompact) isHookInput()      {}
func (*PreCompact) HookEvent() string { return HookPreCompact }

type PostCompact struct {
	BaseHookInput
	Trigger        string `json:"trigger"`
	CompactSummary string `json:"compact_summary"`
}

func (*PostCompact) isHookInput()      {}
func (*PostCompact) HookEvent() string { return HookPostCompact }

// ── task ────────────────────────────────────────────────────

type TaskCreated struct {
	BaseHookInput
	TaskID          string `json:"task_id"`
	TaskSubject     string `json:"task_subject"`
	TaskDescription string `json:"task_description,omitempty"`
	TeammateName    string `json:"teammate_name,omitempty"`
	TeamName        string `json:"team_name,omitempty"`
}

func (*TaskCreated) isHookInput()      {}
func (*TaskCreated) HookEvent() string { return HookTaskCreated }

type TaskCompleted struct {
	BaseHookInput
	TaskID          string `json:"task_id"`
	TaskSubject     string `json:"task_subject"`
	TaskDescription string `json:"task_description,omitempty"`
	TeammateName    string `json:"teammate_name,omitempty"`
	TeamName        string `json:"team_name,omitempty"`
}

func (*TaskCompleted) isHookInput()      {}
func (*TaskCompleted) HookEvent() string { return HookTaskCompleted }

// ── elicitation ─────────────────────────────────────────────

type ElicitationHook struct {
	BaseHookInput
	McpServerName   string          `json:"mcp_server_name"`
	Message         string          `json:"message"`
	Mode            string          `json:"mode,omitempty"`
	URL             string          `json:"url,omitempty"`
	ElicitationID   string          `json:"elicitation_id,omitempty"`
	RequestedSchema json.RawMessage `json:"requested_schema,omitempty"`
}

func (*ElicitationHook) isHookInput()      {}
func (*ElicitationHook) HookEvent() string { return HookElicitation }

type ElicitationResult struct {
	BaseHookInput
	McpServerName string          `json:"mcp_server_name"`
	ElicitationID string          `json:"elicitation_id,omitempty"`
	Mode          string          `json:"mode,omitempty"`
	Action        string          `json:"action"` // accept | decline | cancel
	Content       json.RawMessage `json:"content,omitempty"`
}

func (*ElicitationResult) isHookInput()      {}
func (*ElicitationResult) HookEvent() string { return HookElicitationResult }

// ── config / instructions / worktree / filesystem ───────────

type ConfigChange struct {
	BaseHookInput
	Source   string `json:"source"`
	FilePath string `json:"file_path,omitempty"`
}

func (*ConfigChange) isHookInput()      {}
func (*ConfigChange) HookEvent() string { return HookConfigChange }

type InstructionsLoaded struct {
	BaseHookInput
	FilePath        string   `json:"file_path"`
	MemoryType      string   `json:"memory_type"`
	LoadReason      string   `json:"load_reason"`
	Globs           []string `json:"globs,omitempty"`
	TriggerFilePath string   `json:"trigger_file_path,omitempty"`
	ParentFilePath  string   `json:"parent_file_path,omitempty"`
}

func (*InstructionsLoaded) isHookInput()      {}
func (*InstructionsLoaded) HookEvent() string { return HookInstructionsLoaded }

type WorktreeCreate struct {
	BaseHookInput
	Name string `json:"name"`
}

func (*WorktreeCreate) isHookInput()      {}
func (*WorktreeCreate) HookEvent() string { return HookWorktreeCreate }

type WorktreeRemove struct {
	BaseHookInput
	WorktreePath string `json:"worktree_path"`
}

func (*WorktreeRemove) isHookInput()      {}
func (*WorktreeRemove) HookEvent() string { return HookWorktreeRemove }

type CwdChanged struct {
	BaseHookInput
	OldCwd string `json:"old_cwd"`
	NewCwd string `json:"new_cwd"`
}

func (*CwdChanged) isHookInput()      {}
func (*CwdChanged) HookEvent() string { return HookCwdChanged }

type FileChanged struct {
	BaseHookInput
	FilePath string `json:"file_path"`
	Event    string `json:"event"`
}

func (*FileChanged) isHookInput()      {}
func (*FileChanged) HookEvent() string { return HookFileChanged }

// ── dispatcher ──────────────────────────────────────────────

// UnmarshalHookInput peek hook_event_name 然后 dispatch 到具体类型。
func UnmarshalHookInput(data []byte) (HookInput, error) {
	var head struct {
		HookEventName string `json:"hook_event_name"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return nil, fmt.Errorf("hook: peek hook_event_name: %w", err)
	}
	var v HookInput
	switch head.HookEventName {
	case HookPreToolUse:
		v = &PreToolUse{}
	case HookPostToolUse:
		v = &PostToolUse{}
	case HookPostToolUseFailure:
		v = &PostToolUseFailure{}
	case HookPermissionDenied:
		v = &PermissionDenied{}
	case HookPermissionRequest:
		v = &PermissionRequestHook{}
	case HookNotification:
		v = &Notification{}
	case HookUserPromptSubmit:
		v = &UserPromptSubmit{}
	case HookSessionStart:
		v = &SessionStart{}
	case HookSessionEnd:
		v = &SessionEnd{}
	case HookStop:
		v = &Stop{}
	case HookStopFailure:
		v = &StopFailure{}
	case HookSetup:
		v = &Setup{}
	case HookSubagentStart:
		v = &SubagentStart{}
	case HookSubagentStop:
		v = &SubagentStop{}
	case HookTeammateIdle:
		v = &TeammateIdle{}
	case HookPreCompact:
		v = &PreCompact{}
	case HookPostCompact:
		v = &PostCompact{}
	case HookTaskCreated:
		v = &TaskCreated{}
	case HookTaskCompleted:
		v = &TaskCompleted{}
	case HookElicitation:
		v = &ElicitationHook{}
	case HookElicitationResult:
		v = &ElicitationResult{}
	case HookConfigChange:
		v = &ConfigChange{}
	case HookInstructionsLoaded:
		v = &InstructionsLoaded{}
	case HookWorktreeCreate:
		v = &WorktreeCreate{}
	case HookWorktreeRemove:
		v = &WorktreeRemove{}
	case HookCwdChanged:
		v = &CwdChanged{}
	case HookFileChanged:
		v = &FileChanged{}
	default:
		return nil, fmt.Errorf("hook: unknown hook_event_name %q", head.HookEventName)
	}
	if err := json.Unmarshal(data, v); err != nil {
		return nil, fmt.Errorf("hook: unmarshal %s: %w", head.HookEventName, err)
	}
	return v, nil
}
