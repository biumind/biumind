package sdkproto

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// hookRoundTrip：给一段 hook input JSON，验证 dispatch 选对类型 + 关键字段不丢。
func hookRoundTrip(t *testing.T, name, raw string, wantType HookInput, requireSubstrings []string) {
	t.Helper()
	v, err := UnmarshalHookInput([]byte(raw))
	if err != nil {
		t.Fatalf("%s: unmarshal: %v\nraw=%s", name, err, raw)
	}
	gotT := fmt.Sprintf("%T", v)
	wantT := fmt.Sprintf("%T", wantType)
	if gotT != wantT {
		t.Fatalf("%s: dispatched to %s, want %s", name, gotT, wantT)
	}
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("%s: re-marshal: %v", name, err)
	}
	for _, s := range requireSubstrings {
		if !strings.Contains(string(out), s) {
			t.Errorf("%s: marshaled output missing %q\ngot=%s", name, s, out)
		}
	}
}

// 给所有 27 个 hook variant 一个 base JSON 模板，测试 dispatch + round-trip。
func TestHook_PreToolUse(t *testing.T) {
	hookRoundTrip(t, "PreToolUse", `{
		"session_id": "s1", "transcript_path": "/t", "cwd": "/cwd",
		"hook_event_name": "PreToolUse",
		"tool_name": "Bash",
		"tool_input": { "command": "ls" },
		"tool_use_id": "tu1"
	}`, &PreToolUse{}, []string{`"hook_event_name":"PreToolUse"`, `"tool_name":"Bash"`})
}

func TestHook_PostToolUse(t *testing.T) {
	hookRoundTrip(t, "PostToolUse", `{
		"session_id": "s1", "transcript_path": "/t", "cwd": "/cwd",
		"hook_event_name": "PostToolUse",
		"tool_name": "Bash",
		"tool_input": { "command": "ls" },
		"tool_response": { "exit_code": 0 },
		"tool_use_id": "tu1"
	}`, &PostToolUse{}, []string{`"hook_event_name":"PostToolUse"`, `"exit_code":0`})
}

func TestHook_PostToolUseFailure(t *testing.T) {
	hookRoundTrip(t, "PostToolUseFailure", `{
		"session_id": "s1", "transcript_path": "/t", "cwd": "/cwd",
		"hook_event_name": "PostToolUseFailure",
		"tool_name": "Bash", "tool_input": {}, "tool_use_id": "tu1",
		"error": "killed", "is_interrupt": true
	}`, &PostToolUseFailure{}, []string{`"PostToolUseFailure"`, `"is_interrupt":true`})
}

func TestHook_PermissionDenied(t *testing.T) {
	hookRoundTrip(t, "PermissionDenied", `{
		"session_id": "s1", "transcript_path": "/t", "cwd": "/cwd",
		"hook_event_name": "PermissionDenied",
		"tool_name": "Bash", "tool_input": {}, "tool_use_id": "tu1",
		"reason": "user denied"
	}`, &PermissionDenied{}, []string{`"PermissionDenied"`, `"reason":"user denied"`})
}

func TestHook_PermissionRequest(t *testing.T) {
	hookRoundTrip(t, "PermissionRequest", `{
		"session_id": "s1", "transcript_path": "/t", "cwd": "/cwd",
		"hook_event_name": "PermissionRequest",
		"tool_name": "Bash", "tool_input": {}
	}`, &PermissionRequestHook{}, []string{`"PermissionRequest"`, `"tool_name":"Bash"`})
}

func TestHook_Notification(t *testing.T) {
	hookRoundTrip(t, "Notification", `{
		"session_id": "s1", "transcript_path": "/t", "cwd": "/cwd",
		"hook_event_name": "Notification",
		"message": "hi", "notification_type": "info"
	}`, &Notification{}, []string{`"Notification"`, `"message":"hi"`})
}

func TestHook_UserPromptSubmit(t *testing.T) {
	hookRoundTrip(t, "UserPromptSubmit", `{
		"session_id": "s1", "transcript_path": "/t", "cwd": "/cwd",
		"hook_event_name": "UserPromptSubmit",
		"prompt": "what time is it"
	}`, &UserPromptSubmit{}, []string{`"UserPromptSubmit"`, `"prompt":"what time is it"`})
}

func TestHook_SessionStart(t *testing.T) {
	hookRoundTrip(t, "SessionStart", `{
		"session_id": "s1", "transcript_path": "/t", "cwd": "/cwd",
		"hook_event_name": "SessionStart", "source": "cli"
	}`, &SessionStart{}, []string{`"SessionStart"`, `"source":"cli"`})
}

func TestHook_SessionEnd(t *testing.T) {
	hookRoundTrip(t, "SessionEnd", `{
		"session_id": "s1", "transcript_path": "/t", "cwd": "/cwd",
		"hook_event_name": "SessionEnd", "reason": "ctrl-c"
	}`, &SessionEnd{}, []string{`"SessionEnd"`, `"reason":"ctrl-c"`})
}

func TestHook_Stop(t *testing.T) {
	hookRoundTrip(t, "Stop", `{
		"session_id": "s1", "transcript_path": "/t", "cwd": "/cwd",
		"hook_event_name": "Stop", "stop_hook_active": true
	}`, &Stop{}, []string{`"Stop"`, `"stop_hook_active":true`})
}

func TestHook_StopFailure(t *testing.T) {
	hookRoundTrip(t, "StopFailure", `{
		"session_id": "s1", "transcript_path": "/t", "cwd": "/cwd",
		"hook_event_name": "StopFailure", "error": "panic"
	}`, &StopFailure{}, []string{`"StopFailure"`, `"error":"panic"`})
}

func TestHook_Setup(t *testing.T) {
	hookRoundTrip(t, "Setup", `{
		"session_id": "s1", "transcript_path": "/t", "cwd": "/cwd",
		"hook_event_name": "Setup", "trigger": "init"
	}`, &Setup{}, []string{`"Setup"`, `"trigger":"init"`})
}

func TestHook_SubagentStart(t *testing.T) {
	hookRoundTrip(t, "SubagentStart", `{
		"session_id": "s1", "transcript_path": "/t", "cwd": "/cwd",
		"hook_event_name": "SubagentStart",
		"agent_id": "sa1", "agent_type": "general-purpose"
	}`, &SubagentStart{}, []string{`"SubagentStart"`, `"sa1"`, `"general-purpose"`})
}

func TestHook_SubagentStop(t *testing.T) {
	hookRoundTrip(t, "SubagentStop", `{
		"session_id": "s1", "transcript_path": "/t", "cwd": "/cwd",
		"hook_event_name": "SubagentStop",
		"stop_hook_active": false,
		"agent_id": "sa1",
		"agent_transcript_path": "/sa1.jsonl",
		"agent_type": "general-purpose"
	}`, &SubagentStop{}, []string{`"SubagentStop"`, `"agent_transcript_path":"/sa1.jsonl"`})
}

func TestHook_TeammateIdle(t *testing.T) {
	hookRoundTrip(t, "TeammateIdle", `{
		"session_id": "s1", "transcript_path": "/t", "cwd": "/cwd",
		"hook_event_name": "TeammateIdle",
		"teammate_name": "alice", "team_name": "core"
	}`, &TeammateIdle{}, []string{`"TeammateIdle"`, `"alice"`})
}

func TestHook_PreCompact(t *testing.T) {
	hookRoundTrip(t, "PreCompact", `{
		"session_id": "s1", "transcript_path": "/t", "cwd": "/cwd",
		"hook_event_name": "PreCompact",
		"trigger": "auto", "custom_instructions": ""
	}`, &PreCompact{}, []string{`"PreCompact"`, `"trigger":"auto"`})
}

func TestHook_PostCompact(t *testing.T) {
	hookRoundTrip(t, "PostCompact", `{
		"session_id": "s1", "transcript_path": "/t", "cwd": "/cwd",
		"hook_event_name": "PostCompact",
		"trigger": "auto", "compact_summary": "did stuff"
	}`, &PostCompact{}, []string{`"PostCompact"`, `"compact_summary":"did stuff"`})
}

func TestHook_TaskCreated(t *testing.T) {
	hookRoundTrip(t, "TaskCreated", `{
		"session_id": "s1", "transcript_path": "/t", "cwd": "/cwd",
		"hook_event_name": "TaskCreated",
		"task_id": "t1", "task_subject": "build"
	}`, &TaskCreated{}, []string{`"TaskCreated"`, `"task_id":"t1"`})
}

func TestHook_TaskCompleted(t *testing.T) {
	hookRoundTrip(t, "TaskCompleted", `{
		"session_id": "s1", "transcript_path": "/t", "cwd": "/cwd",
		"hook_event_name": "TaskCompleted",
		"task_id": "t1", "task_subject": "build"
	}`, &TaskCompleted{}, []string{`"TaskCompleted"`, `"task_id":"t1"`})
}

func TestHook_Elicitation(t *testing.T) {
	hookRoundTrip(t, "Elicitation", `{
		"session_id": "s1", "transcript_path": "/t", "cwd": "/cwd",
		"hook_event_name": "Elicitation",
		"mcp_server_name": "supabase", "message": "fill form"
	}`, &ElicitationHook{}, []string{`"Elicitation"`, `"mcp_server_name":"supabase"`})
}

func TestHook_ElicitationResult(t *testing.T) {
	hookRoundTrip(t, "ElicitationResult", `{
		"session_id": "s1", "transcript_path": "/t", "cwd": "/cwd",
		"hook_event_name": "ElicitationResult",
		"mcp_server_name": "supabase", "action": "accept"
	}`, &ElicitationResult{}, []string{`"ElicitationResult"`, `"action":"accept"`})
}

func TestHook_ConfigChange(t *testing.T) {
	hookRoundTrip(t, "ConfigChange", `{
		"session_id": "s1", "transcript_path": "/t", "cwd": "/cwd",
		"hook_event_name": "ConfigChange",
		"source": "user", "file_path": "~/.claude/settings.json"
	}`, &ConfigChange{}, []string{`"ConfigChange"`, `"source":"user"`})
}

func TestHook_InstructionsLoaded(t *testing.T) {
	hookRoundTrip(t, "InstructionsLoaded", `{
		"session_id": "s1", "transcript_path": "/t", "cwd": "/cwd",
		"hook_event_name": "InstructionsLoaded",
		"file_path": "/CLAUDE.md", "memory_type": "project", "load_reason": "session_start"
	}`, &InstructionsLoaded{}, []string{`"InstructionsLoaded"`, `"/CLAUDE.md"`})
}

func TestHook_WorktreeCreate(t *testing.T) {
	hookRoundTrip(t, "WorktreeCreate", `{
		"session_id": "s1", "transcript_path": "/t", "cwd": "/cwd",
		"hook_event_name": "WorktreeCreate",
		"name": "feature-x"
	}`, &WorktreeCreate{}, []string{`"WorktreeCreate"`, `"feature-x"`})
}

func TestHook_WorktreeRemove(t *testing.T) {
	hookRoundTrip(t, "WorktreeRemove", `{
		"session_id": "s1", "transcript_path": "/t", "cwd": "/cwd",
		"hook_event_name": "WorktreeRemove",
		"worktree_path": "/path/to/wt"
	}`, &WorktreeRemove{}, []string{`"WorktreeRemove"`, `"/path/to/wt"`})
}

func TestHook_CwdChanged(t *testing.T) {
	hookRoundTrip(t, "CwdChanged", `{
		"session_id": "s1", "transcript_path": "/t", "cwd": "/new",
		"hook_event_name": "CwdChanged",
		"old_cwd": "/old", "new_cwd": "/new"
	}`, &CwdChanged{}, []string{`"CwdChanged"`, `"old_cwd":"/old"`})
}

func TestHook_FileChanged(t *testing.T) {
	hookRoundTrip(t, "FileChanged", `{
		"session_id": "s1", "transcript_path": "/t", "cwd": "/cwd",
		"hook_event_name": "FileChanged",
		"file_path": "/a.txt", "event": "modified"
	}`, &FileChanged{}, []string{`"FileChanged"`, `"event":"modified"`})
}

func TestHook_UnknownEvent(t *testing.T) {
	_, err := UnmarshalHookInput([]byte(`{"session_id":"s","transcript_path":"/t","cwd":"/c","hook_event_name":"FuturisticEvent"}`))
	if err == nil {
		t.Fatal("expected unknown hook event error, got nil")
	}
}

// ── permission update 6 union ───────────────────────────────

func permRoundTrip(t *testing.T, name, raw string, wantType PermissionUpdate) {
	t.Helper()
	v, err := UnmarshalPermissionUpdate([]byte(raw))
	if err != nil {
		t.Fatalf("%s: unmarshal: %v", name, err)
	}
	gotT := fmt.Sprintf("%T", v)
	wantT := fmt.Sprintf("%T", wantType)
	if gotT != wantT {
		t.Fatalf("%s: dispatched to %s, want %s", name, gotT, wantT)
	}
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("%s: marshal: %v", name, err)
	}
	if !strings.Contains(string(out), v.PermissionUpdateType()) {
		t.Errorf("%s: marshal lost type field: %s", name, out)
	}
}

func TestPermissionUpdate_AddRules(t *testing.T) {
	permRoundTrip(t, "addRules", `{
		"type": "addRules",
		"rules": [{ "toolName": "Bash" }],
		"behavior": "allow",
		"destination": "userSettings"
	}`, &AddRules{})
}

func TestPermissionUpdate_ReplaceRules(t *testing.T) {
	permRoundTrip(t, "replaceRules", `{
		"type": "replaceRules",
		"rules": [],
		"behavior": "ask",
		"destination": "session"
	}`, &ReplaceRules{})
}

func TestPermissionUpdate_RemoveRules(t *testing.T) {
	permRoundTrip(t, "removeRules", `{
		"type": "removeRules",
		"rules": [{ "toolName": "Bash" }],
		"behavior": "allow",
		"destination": "userSettings"
	}`, &RemoveRules{})
}

func TestPermissionUpdate_SetMode(t *testing.T) {
	permRoundTrip(t, "setMode", `{
		"type": "setMode",
		"mode": "acceptEdits",
		"destination": "session"
	}`, &SetModeUpdate{})
}

func TestPermissionUpdate_AddDirectories(t *testing.T) {
	permRoundTrip(t, "addDirectories", `{
		"type": "addDirectories",
		"directories": ["/a", "/b"],
		"destination": "userSettings"
	}`, &AddDirectories{})
}

func TestPermissionUpdate_RemoveDirectories(t *testing.T) {
	permRoundTrip(t, "removeDirectories", `{
		"type": "removeDirectories",
		"directories": ["/a"],
		"destination": "session"
	}`, &RemoveDirectories{})
}

func TestPermissionUpdate_Unknown(t *testing.T) {
	_, err := UnmarshalPermissionUpdate([]byte(`{"type":"someFutureType"}`))
	if err == nil {
		t.Fatal("expected unknown permission update type error, got nil")
	}
}

// ── mcp 5 union ─────────────────────────────────────────────

func mcpRoundTrip(t *testing.T, name, raw string, wantType McpServerConfig) {
	t.Helper()
	v, err := UnmarshalMcpServerConfig([]byte(raw))
	if err != nil {
		t.Fatalf("%s: unmarshal: %v", name, err)
	}
	gotT := fmt.Sprintf("%T", v)
	wantT := fmt.Sprintf("%T", wantType)
	if gotT != wantT {
		t.Fatalf("%s: dispatched to %s, want %s", name, gotT, wantT)
	}
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("%s: marshal: %v", name, err)
	}
	if !strings.Contains(string(out), v.McpServerType()) && v.McpServerType() != "stdio" {
		t.Errorf("%s: marshal missing type: %s", name, out)
	}
}

func TestMcpServer_Stdio(t *testing.T) {
	mcpRoundTrip(t, "stdio", `{
		"type": "stdio",
		"command": "supabase-mcp",
		"args": ["--port", "8000"]
	}`, &McpStdioServerConfig{})
}

func TestMcpServer_StdioImplicit(t *testing.T) {
	// 没有 type 字段时按 stdio 处理（协议默认）
	v, err := UnmarshalMcpServerConfig([]byte(`{"command":"x"}`))
	if err != nil {
		t.Fatalf("implicit stdio: %v", err)
	}
	if _, ok := v.(*McpStdioServerConfig); !ok {
		t.Errorf("implicit type, want stdio, got %T", v)
	}
}

func TestMcpServer_SSE(t *testing.T) {
	mcpRoundTrip(t, "sse", `{
		"type": "sse",
		"url": "https://example.com/sse"
	}`, &McpSSEServerConfig{})
}

func TestMcpServer_HTTP(t *testing.T) {
	mcpRoundTrip(t, "http", `{
		"type": "http",
		"url": "https://example.com/mcp"
	}`, &McpHttpServerConfig{})
}

func TestMcpServer_SDK(t *testing.T) {
	mcpRoundTrip(t, "sdk", `{
		"type": "sdk",
		"name": "in-process"
	}`, &McpSdkServerConfig{})
}

func TestMcpServer_ClaudeProxy(t *testing.T) {
	mcpRoundTrip(t, "claudeai-proxy", `{
		"type": "claudeai-proxy",
		"url": "https://api.anthropic.com/mcp"
	}`, &McpClaudeAIProxyServerConfig{})
}

// ── agents / settings round-trip ────────────────────────────

func TestAgents_Roundtrip(t *testing.T) {
	raw := `{"name":"task","description":"…","model":"claude-3-7","tools":["Bash"]}`
	var a AgentInfo
	if err := json.Unmarshal([]byte(raw), &a); err != nil {
		t.Fatal(err)
	}
	if a.Name != "task" || a.Model != "claude-3-7" {
		t.Errorf("got %+v", a)
	}
	out, _ := json.Marshal(&a)
	if !strings.Contains(string(out), `"name":"task"`) {
		t.Errorf("marshal lost name: %s", out)
	}
}

func TestSettings_Roundtrip(t *testing.T) {
	raw := `{
		"effective": { "permissionMode": "default" },
		"sources": { "userSettings": { "permissionMode": "default" } },
		"applied": { "permissionMode": "userSettings" }
	}`
	var s GetSettingsResponse
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatal(err)
	}
	if got := s.Applied["permissionMode"]; got != "userSettings" {
		t.Errorf("applied[permissionMode]=%q want userSettings", got)
	}
}
