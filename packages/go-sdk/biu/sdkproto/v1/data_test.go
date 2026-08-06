package sdkproto

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// roundTrip: 给一段 JSON、期望 dispatch 后类型、期望 marshal 后包含的若干字段子串。
// 验证两点：UnmarshalSDKMessage 选对类型；marshal 不丢字段。
func roundTrip(t *testing.T, name, raw string, wantType SDKMessage, requireSubstrings []string) {
	t.Helper()
	msg, err := UnmarshalSDKMessage([]byte(raw))
	if err != nil {
		t.Fatalf("%s: unmarshal: %v\nraw=%s", name, err, raw)
	}
	gotT := fmt.Sprintf("%T", msg)
	wantT := fmt.Sprintf("%T", wantType)
	if gotT != wantT {
		t.Fatalf("%s: dispatched to %s, want %s", name, gotT, wantT)
	}
	out, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("%s: re-marshal: %v", name, err)
	}
	for _, s := range requireSubstrings {
		if !strings.Contains(string(out), s) {
			t.Errorf("%s: marshaled output missing %q\ngot=%s", name, s, out)
		}
	}
}

// 28 个 variant 各一条 minimal fixture，保证 dispatch 选对 + 关键字段不丢。
// fixture JSON 的字段尽量贴真实样本；可选字段不必填。

func TestSDKMessage_User(t *testing.T) {
	roundTrip(t, "user", `{
		"type": "user",
		"message": { "role": "user", "content": "hi" },
		"uuid": "u1",
		"session_id": "s1"
	}`, &SDKUserMessage{}, []string{`"type":"user"`, `"role":"user"`, `"session_id":"s1"`})
}

func TestSDKMessage_UserReplay(t *testing.T) {
	// SDKUserMessageReplay 跟 User 用同一个 Go struct（带 IsReplay 字段）。
	roundTrip(t, "user_replay", `{
		"type": "user",
		"message": { "role": "user", "content": "hi" },
		"isReplay": true,
		"uuid": "u1",
		"session_id": "s1"
	}`, &SDKUserMessage{}, []string{`"isReplay":true`})
}

func TestSDKMessage_Assistant(t *testing.T) {
	roundTrip(t, "assistant", `{
		"type": "assistant",
		"message": { "role": "assistant", "content": "ok" },
		"uuid": "a1",
		"session_id": "s1"
	}`, &SDKAssistantMessage{}, []string{`"type":"assistant"`, `"uuid":"a1"`})
}

func TestSDKMessage_PartialAssistant(t *testing.T) {
	roundTrip(t, "stream_event", `{
		"type": "stream_event",
		"event": { "type": "content_block_delta" },
		"uuid": "p1",
		"session_id": "s1"
	}`, &SDKPartialAssistantMessage{}, []string{`"type":"stream_event"`})
}

func TestSDKMessage_ResultSuccess(t *testing.T) {
	roundTrip(t, "result_success", `{
		"type": "result",
		"subtype": "success",
		"duration_ms": 1000,
		"duration_api_ms": 800,
		"is_error": false,
		"num_turns": 3,
		"result": "done",
		"stop_reason": "end_turn",
		"total_cost_usd": 0.01,
		"usage": {},
		"modelUsage": {
			"claude-3-7": {
				"inputTokens": 10, "outputTokens": 20,
				"cacheReadInputTokens": 0, "cacheCreationInputTokens": 0,
				"webSearchRequests": 0, "costUSD": 0.01,
				"contextWindow": 200000, "maxOutputTokens": 4096
			}
		},
		"permission_denials": [],
		"uuid": "r1",
		"session_id": "s1"
	}`, &SDKResultSuccess{}, []string{`"subtype":"success"`, `"is_error":false`, `"costUSD":0.01`})
}

func TestSDKMessage_ResultError(t *testing.T) {
	roundTrip(t, "result_error", `{
		"type": "result",
		"subtype": "error_max_turns",
		"duration_ms": 5000,
		"duration_api_ms": 4000,
		"is_error": true,
		"num_turns": 10,
		"total_cost_usd": 0.05,
		"usage": {},
		"modelUsage": {},
		"permission_denials": [],
		"errors": [{ "message": "max turns reached" }],
		"uuid": "r2",
		"session_id": "s1"
	}`, &SDKResultError{}, []string{`"subtype":"error_max_turns"`, `"is_error":true`})
}

func TestSDKMessage_SystemInit(t *testing.T) {
	roundTrip(t, "system_init", `{
		"type": "system",
		"subtype": "init",
		"agents": [],
		"apiKeySource": "env",
		"betas": [],
		"claude_code_version": "2.0.0",
		"cwd": "/tmp",
		"tools": ["Bash"],
		"mcp_servers": [],
		"model": "claude-3-7",
		"permissionMode": "acceptEdits",
		"slash_commands": [],
		"output_style": "default",
		"uuid": "i1",
		"session_id": "s1"
	}`, &SDKSystemInit{}, []string{`"subtype":"init"`, `"apiKeySource":"env"`, `"claude_code_version":"2.0.0"`})
}

func TestSDKMessage_SystemStatus(t *testing.T) {
	roundTrip(t, "system_status", `{
		"type": "system",
		"subtype": "status",
		"status": "compacting",
		"uuid": "st1",
		"session_id": "s1"
	}`, &SDKSystemStatus{}, []string{`"subtype":"status"`, `"status":"compacting"`})
}

func TestSDKMessage_CompactBoundary(t *testing.T) {
	roundTrip(t, "compact_boundary", `{
		"type": "system",
		"subtype": "compact_boundary",
		"compact_metadata": { "trigger": "auto", "pre_tokens": 50000 },
		"uuid": "c1",
		"session_id": "s1"
	}`, &SDKCompactBoundary{}, []string{`"subtype":"compact_boundary"`, `"trigger":"auto"`, `"pre_tokens":50000`})
}

func TestSDKMessage_APIRetry(t *testing.T) {
	roundTrip(t, "api_retry", `{
		"type": "system",
		"subtype": "api_retry",
		"attempt": 2,
		"max_retries": 5,
		"retry_delay_ms": 1000,
		"error": "529 Overloaded",
		"uuid": "ar1",
		"session_id": "s1"
	}`, &SDKAPIRetry{}, []string{`"subtype":"api_retry"`, `"attempt":2`})
}

func TestSDKMessage_LocalCommandOutput(t *testing.T) {
	roundTrip(t, "local_command_output", `{
		"type": "system",
		"subtype": "local_command_output",
		"content": "hello",
		"uuid": "l1",
		"session_id": "s1"
	}`, &SDKLocalCommandOutput{}, []string{`"subtype":"local_command_output"`, `"content":"hello"`})
}

func TestSDKMessage_HookStarted(t *testing.T) {
	roundTrip(t, "hook_started", `{
		"type": "system",
		"subtype": "hook_started",
		"hook_id": "h1",
		"hook_name": "PreToolUse",
		"hook_event": "pre_tool_use",
		"uuid": "hs1",
		"session_id": "s1"
	}`, &SDKHookStarted{}, []string{`"subtype":"hook_started"`, `"hook_id":"h1"`})
}

func TestSDKMessage_HookProgress(t *testing.T) {
	roundTrip(t, "hook_progress", `{
		"type": "system",
		"subtype": "hook_progress",
		"hook_id": "h1",
		"stdout": "running",
		"uuid": "hp1",
		"session_id": "s1"
	}`, &SDKHookProgress{}, []string{`"subtype":"hook_progress"`, `"stdout":"running"`})
}

func TestSDKMessage_HookResponse(t *testing.T) {
	roundTrip(t, "hook_response", `{
		"type": "system",
		"subtype": "hook_response",
		"hook_id": "h1",
		"outcome": "success",
		"exit_code": 0,
		"uuid": "hr1",
		"session_id": "s1"
	}`, &SDKHookResponse{}, []string{`"subtype":"hook_response"`, `"outcome":"success"`})
}

func TestSDKMessage_AuthStatus(t *testing.T) {
	roundTrip(t, "auth_status", `{
		"type": "auth_status",
		"isAuthenticating": true,
		"output": ["check email"],
		"uuid": "au1",
		"session_id": "s1"
	}`, &SDKAuthStatus{}, []string{`"type":"auth_status"`, `"isAuthenticating":true`})
}

func TestSDKMessage_FilesPersisted(t *testing.T) {
	roundTrip(t, "files_persisted", `{
		"type": "system",
		"subtype": "files_persisted",
		"files": ["/a.txt"],
		"failed": [],
		"processed_at": 1700000000000,
		"uuid": "fp1",
		"session_id": "s1"
	}`, &SDKFilesPersisted{}, []string{`"subtype":"files_persisted"`, `"files":["/a.txt"]`})
}

func TestSDKMessage_TaskNotification(t *testing.T) {
	roundTrip(t, "task_notification", `{
		"type": "system",
		"subtype": "task_notification",
		"task_id": "t1",
		"status": "completed",
		"output_file": "/out.md",
		"summary": "ok",
		"uuid": "tn1",
		"session_id": "s1"
	}`, &SDKTaskNotification{}, []string{`"subtype":"task_notification"`, `"task_id":"t1"`})
}

func TestSDKMessage_TaskStarted(t *testing.T) {
	roundTrip(t, "task_started", `{
		"type": "system",
		"subtype": "task_started",
		"task_id": "t1",
		"description": "build feature",
		"uuid": "ts1",
		"session_id": "s1"
	}`, &SDKTaskStarted{}, []string{`"subtype":"task_started"`, `"description":"build feature"`})
}

func TestSDKMessage_TaskProgress(t *testing.T) {
	roundTrip(t, "task_progress", `{
		"type": "system",
		"subtype": "task_progress",
		"task_id": "t1",
		"last_tool_name": "Bash",
		"summary": "step 3/5",
		"uuid": "tp1",
		"session_id": "s1"
	}`, &SDKTaskProgress{}, []string{`"subtype":"task_progress"`, `"last_tool_name":"Bash"`})
}

func TestSDKMessage_SessionStateChanged(t *testing.T) {
	roundTrip(t, "session_state_changed", `{
		"type": "system",
		"subtype": "session_state_changed",
		"state": "running",
		"uuid": "ssc1",
		"session_id": "s1"
	}`, &SDKSessionStateChanged{}, []string{`"subtype":"session_state_changed"`, `"state":"running"`})
}

func TestSDKMessage_RateLimitEvent(t *testing.T) {
	roundTrip(t, "rate_limit_event", `{
		"type": "rate_limit_event",
		"rate_limit_info": { "remaining": 0, "reset": 60 },
		"uuid": "rl1",
		"session_id": "s1"
	}`, &SDKRateLimitEvent{}, []string{`"type":"rate_limit_event"`, `"remaining":0`})
}

func TestSDKMessage_ElicitationComplete(t *testing.T) {
	roundTrip(t, "elicitation_complete", `{
		"type": "system",
		"subtype": "elicitation_complete",
		"mcp_server_name": "supabase",
		"elicitation_id": "e1",
		"uuid": "ec1",
		"session_id": "s1"
	}`, &SDKElicitationComplete{}, []string{`"subtype":"elicitation_complete"`, `"mcp_server_name":"supabase"`})
}

func TestSDKMessage_PromptSuggestion(t *testing.T) {
	roundTrip(t, "prompt_suggestion", `{
		"type": "prompt_suggestion",
		"suggestion": "try /help",
		"uuid": "ps1",
		"session_id": "s1"
	}`, &SDKPromptSuggestion{}, []string{`"type":"prompt_suggestion"`, `"suggestion":"try /help"`})
}

func TestSDKMessage_ToolProgress(t *testing.T) {
	roundTrip(t, "tool_progress", `{
		"type": "tool_progress",
		"tool_use_id": "tu1",
		"tool_name": "Bash",
		"elapsed_time_seconds": 1.5,
		"uuid": "toolp1",
		"session_id": "s1"
	}`, &SDKToolProgress{}, []string{`"type":"tool_progress"`, `"tool_name":"Bash"`})
}

func TestSDKMessage_ToolUseSummary(t *testing.T) {
	roundTrip(t, "tool_use_summary", `{
		"type": "tool_use_summary",
		"summary": "Edited 3 files",
		"preceding_tool_use_ids": ["tu1", "tu2", "tu3"],
		"uuid": "tus1",
		"session_id": "s1"
	}`, &SDKToolUseSummary{}, []string{`"type":"tool_use_summary"`, `"tu1"`})
}

func TestSDKMessage_StreamlinedText(t *testing.T) {
	roundTrip(t, "streamlined_text", `{
		"type": "streamlined_text",
		"text": "I'll help with that.",
		"uuid": "stt1",
		"session_id": "s1"
	}`, &SDKStreamlinedText{}, []string{`"type":"streamlined_text"`})
}

func TestSDKMessage_StreamlinedToolUseSummary(t *testing.T) {
	roundTrip(t, "streamlined_tool_use_summary", `{
		"type": "streamlined_tool_use_summary",
		"tool_summary": "Edited 3 files",
		"uuid": "sts1",
		"session_id": "s1"
	}`, &SDKStreamlinedToolUseSummary{}, []string{`"type":"streamlined_tool_use_summary"`})
}

func TestSDKMessage_PostTurnSummary(t *testing.T) {
	roundTrip(t, "post_turn_summary", `{
		"type": "system",
		"subtype": "post_turn_summary",
		"summarizes_uuid": "u100",
		"status_category": "success",
		"status_detail": "done",
		"is_noteworthy": false,
		"title": "Edit",
		"description": "Edited file",
		"recent_action": "edit",
		"needs_action": false,
		"artifact_urls": [],
		"uuid": "pts1",
		"session_id": "s1"
	}`, &SDKPostTurnSummary{}, []string{`"subtype":"post_turn_summary"`, `"summarizes_uuid":"u100"`})
}

func TestSDKMessage_UnknownType(t *testing.T) {
	_, err := UnmarshalSDKMessage([]byte(`{"type": "totally_made_up"}`))
	if err == nil {
		t.Fatal("expected error for unknown type, got nil")
	}
}

func TestSDKMessage_UnknownSystemSubtype(t *testing.T) {
	_, err := UnmarshalSDKMessage([]byte(`{"type": "system", "subtype": "future_thing"}`))
	if err == nil {
		t.Fatal("expected error for unknown subtype, got nil")
	}
}
