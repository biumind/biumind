package sdkproto

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// controlRoundTrip：给一段完整 SDKControlRequest 帧 JSON，验证 dispatch 选对内层类型，
// 重新 marshal 后保留所需字段。
func controlRoundTrip(t *testing.T, name, raw string, wantInner ControlRequestInner, requireSubstrings []string) {
	t.Helper()
	var req SDKControlRequest
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		t.Fatalf("%s: unmarshal: %v\nraw=%s", name, err, raw)
	}
	gotT := fmt.Sprintf("%T", req.Request)
	wantT := fmt.Sprintf("%T", wantInner)
	if gotT != wantT {
		t.Fatalf("%s: dispatched to %s, want %s", name, gotT, wantT)
	}
	out, err := json.Marshal(&req)
	if err != nil {
		t.Fatalf("%s: re-marshal: %v", name, err)
	}
	for _, s := range requireSubstrings {
		if !strings.Contains(string(out), s) {
			t.Errorf("%s: marshaled output missing %q\ngot=%s", name, s, out)
		}
	}
}

func TestControl_Initialize(t *testing.T) {
	controlRoundTrip(t, "initialize", `{
		"type": "control_request",
		"request_id": "r1",
		"request": {
			"subtype": "initialize",
			"systemPrompt": "you are helpful",
			"promptSuggestions": true
		}
	}`, &Initialize{}, []string{`"type":"control_request"`, `"subtype":"initialize"`, `"systemPrompt":"you are helpful"`})
}

func TestControl_Interrupt(t *testing.T) {
	controlRoundTrip(t, "interrupt", `{
		"type": "control_request",
		"request_id": "r2",
		"request": { "subtype": "interrupt" }
	}`, &Interrupt{}, []string{`"subtype":"interrupt"`})
}

func TestControl_Permission(t *testing.T) {
	controlRoundTrip(t, "can_use_tool", `{
		"type": "control_request",
		"request_id": "r3",
		"request": {
			"subtype": "can_use_tool",
			"tool_name": "Bash",
			"input": { "command": "rm -rf /" },
			"tool_use_id": "tu1"
		}
	}`, &PermissionRequest{}, []string{`"subtype":"can_use_tool"`, `"tool_name":"Bash"`, `"tool_use_id":"tu1"`})
}

func TestControl_SetModel(t *testing.T) {
	controlRoundTrip(t, "set_model", `{
		"type": "control_request",
		"request_id": "r4",
		"request": { "subtype": "set_model", "model": "claude-3-7" }
	}`, &SetModel{}, []string{`"subtype":"set_model"`, `"model":"claude-3-7"`})
}

func TestControl_SetPermissionMode(t *testing.T) {
	controlRoundTrip(t, "set_permission_mode", `{
		"type": "control_request",
		"request_id": "r5",
		"request": { "subtype": "set_permission_mode", "mode": "acceptEdits" }
	}`, &SetPermissionMode{}, []string{`"subtype":"set_permission_mode"`, `"mode":"acceptEdits"`})
}

func TestControl_SetMaxThinkingTokens(t *testing.T) {
	controlRoundTrip(t, "set_max_thinking_tokens", `{
		"type": "control_request",
		"request_id": "r6",
		"request": { "subtype": "set_max_thinking_tokens", "max_thinking_tokens": 8000 }
	}`, &SetMaxThinkingTokens{}, []string{`"subtype":"set_max_thinking_tokens"`, `"max_thinking_tokens":8000`})
}

func TestControl_McpStatus(t *testing.T) {
	controlRoundTrip(t, "mcp_status", `{
		"type": "control_request", "request_id": "r7",
		"request": { "subtype": "mcp_status" }
	}`, &McpStatus{}, []string{`"subtype":"mcp_status"`})
}

func TestControl_McpMessage(t *testing.T) {
	controlRoundTrip(t, "mcp_message", `{
		"type": "control_request", "request_id": "r8",
		"request": {
			"subtype": "mcp_message",
			"server_name": "supabase",
			"message": { "jsonrpc": "2.0", "method": "initialize", "id": 1 }
		}
	}`, &McpMessage{}, []string{`"subtype":"mcp_message"`, `"server_name":"supabase"`})
}

func TestControl_McpSetServers(t *testing.T) {
	controlRoundTrip(t, "mcp_set_servers", `{
		"type": "control_request", "request_id": "r9",
		"request": {
			"subtype": "mcp_set_servers",
			"servers": { "supabase": { "type": "stdio", "command": "supabase-mcp" } }
		}
	}`, &McpSetServers{}, []string{`"subtype":"mcp_set_servers"`, `"supabase"`})
}

func TestControl_McpReconnect(t *testing.T) {
	controlRoundTrip(t, "mcp_reconnect", `{
		"type": "control_request", "request_id": "r10",
		"request": { "subtype": "mcp_reconnect", "serverName": "supabase" }
	}`, &McpReconnect{}, []string{`"subtype":"mcp_reconnect"`, `"serverName":"supabase"`})
}

func TestControl_McpToggle(t *testing.T) {
	controlRoundTrip(t, "mcp_toggle", `{
		"type": "control_request", "request_id": "r11",
		"request": { "subtype": "mcp_toggle", "serverName": "supabase", "enabled": false }
	}`, &McpToggle{}, []string{`"subtype":"mcp_toggle"`, `"enabled":false`})
}

func TestControl_GetContextUsage(t *testing.T) {
	controlRoundTrip(t, "get_context_usage", `{
		"type": "control_request", "request_id": "r12",
		"request": { "subtype": "get_context_usage" }
	}`, &GetContextUsage{}, []string{`"subtype":"get_context_usage"`})
}

func TestControl_HookCallback(t *testing.T) {
	controlRoundTrip(t, "hook_callback", `{
		"type": "control_request", "request_id": "r13",
		"request": {
			"subtype": "hook_callback",
			"callback_id": "cb1",
			"input": { "hook_event_name": "PreToolUse", "tool_name": "Bash" }
		}
	}`, &HookCallback{}, []string{`"subtype":"hook_callback"`, `"callback_id":"cb1"`})
}

func TestControl_RewindFiles(t *testing.T) {
	controlRoundTrip(t, "rewind_files", `{
		"type": "control_request", "request_id": "r14",
		"request": { "subtype": "rewind_files", "user_message_id": "u1", "dry_run": true }
	}`, &RewindFiles{}, []string{`"subtype":"rewind_files"`, `"user_message_id":"u1"`, `"dry_run":true`})
}

func TestControl_CancelAsyncMessage(t *testing.T) {
	controlRoundTrip(t, "cancel_async_message", `{
		"type": "control_request", "request_id": "r15",
		"request": { "subtype": "cancel_async_message", "message_uuid": "m1" }
	}`, &CancelAsyncMessage{}, []string{`"subtype":"cancel_async_message"`, `"message_uuid":"m1"`})
}

func TestControl_SeedReadState(t *testing.T) {
	controlRoundTrip(t, "seed_read_state", `{
		"type": "control_request", "request_id": "r16",
		"request": { "subtype": "seed_read_state", "path": "/a.txt", "mtime": 1700000000 }
	}`, &SeedReadState{}, []string{`"subtype":"seed_read_state"`, `"path":"/a.txt"`})
}

func TestControl_ReloadPlugins(t *testing.T) {
	controlRoundTrip(t, "reload_plugins", `{
		"type": "control_request", "request_id": "r17",
		"request": { "subtype": "reload_plugins" }
	}`, &ReloadPlugins{}, []string{`"subtype":"reload_plugins"`})
}

func TestControl_StopTask(t *testing.T) {
	controlRoundTrip(t, "stop_task", `{
		"type": "control_request", "request_id": "r18",
		"request": { "subtype": "stop_task", "task_id": "t1" }
	}`, &StopTask{}, []string{`"subtype":"stop_task"`, `"task_id":"t1"`})
}

func TestControl_ApplyFlagSettings(t *testing.T) {
	controlRoundTrip(t, "apply_flag_settings", `{
		"type": "control_request", "request_id": "r19",
		"request": { "subtype": "apply_flag_settings", "settings": { "x": 1 } }
	}`, &ApplyFlagSettings{}, []string{`"subtype":"apply_flag_settings"`})
}

func TestControl_GetSettings(t *testing.T) {
	controlRoundTrip(t, "get_settings", `{
		"type": "control_request", "request_id": "r20",
		"request": { "subtype": "get_settings" }
	}`, &GetSettings{}, []string{`"subtype":"get_settings"`})
}

func TestControl_Elicitation(t *testing.T) {
	controlRoundTrip(t, "elicitation", `{
		"type": "control_request", "request_id": "r21",
		"request": {
			"subtype": "elicitation",
			"mcp_server_name": "supabase",
			"message": "Please provide token",
			"mode": "form",
			"requested_schema": { "type": "object" }
		}
	}`, &Elicitation{}, []string{`"subtype":"elicitation"`, `"mcp_server_name":"supabase"`, `"mode":"form"`})
}

// ── wrappers: success / error / cancel ──────────────────────

func TestControlResponse_Success(t *testing.T) {
	raw := `{
		"type": "control_response",
		"response": {
			"subtype": "success",
			"request_id": "r1",
			"response": { "totalTokens": 1234 }
		}
	}`
	var resp SDKControlResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Type != "control_response" {
		t.Errorf("type=%q want control_response", resp.Type)
	}
	if resp.Response == nil || resp.Response.Subtype != "success" {
		t.Errorf("subtype=%v want success", resp.Response)
	}
	if resp.Response.RequestID != "r1" {
		t.Errorf("request_id=%q want r1", resp.Response.RequestID)
	}
}

func TestControlResponse_Error(t *testing.T) {
	raw := `{
		"type": "control_response",
		"response": {
			"subtype": "error",
			"request_id": "r1",
			"error": "tool denied"
		}
	}`
	var resp SDKControlResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Response.Subtype != "error" || resp.Response.Error != "tool denied" {
		t.Errorf("got %+v", resp.Response)
	}
}

func TestControlCancelRequest(t *testing.T) {
	raw := `{ "type": "control_cancel_request", "request_id": "r1" }`
	var c SDKControlCancelRequest
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if c.Type != "control_cancel_request" || c.RequestID != "r1" {
		t.Errorf("got %+v", c)
	}
	out, err := json.Marshal(&c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(out), `"type":"control_cancel_request"`) {
		t.Errorf("marshal lost type tag: %s", out)
	}
}

func TestControl_UnknownSubtype(t *testing.T) {
	raw := `{
		"type": "control_request", "request_id": "rX",
		"request": { "subtype": "future_subtype" }
	}`
	var req SDKControlRequest
	if err := json.Unmarshal([]byte(raw), &req); err == nil {
		t.Fatal("expected unknown subtype error, got nil")
	}
}

func TestControlRequest_WrongType(t *testing.T) {
	raw := `{
		"type": "control_response", "request_id": "rX",
		"request": { "subtype": "interrupt" }
	}`
	var req SDKControlRequest
	if err := json.Unmarshal([]byte(raw), &req); err == nil {
		t.Fatal("expected wrong-type error, got nil")
	}
}

// Subtype() 接口方法返回正确字面量（用于运行时类型分支）
func TestControlRequestInner_SubtypeMethod(t *testing.T) {
	cases := []struct {
		v    ControlRequestInner
		want string
	}{
		{&Initialize{}, SubtypeInitialize},
		{&Interrupt{}, SubtypeInterrupt},
		{&PermissionRequest{}, SubtypeCanUseTool},
		{&Elicitation{}, SubtypeElicitation},
		{&StopTask{}, SubtypeStopTask},
	}
	for _, c := range cases {
		if got := c.v.Subtype(); got != c.want {
			t.Errorf("%T.Subtype() = %q, want %q", c.v, got, c.want)
		}
	}
}
