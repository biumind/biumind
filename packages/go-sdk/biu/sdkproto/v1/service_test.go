package sdkproto

import (
	"encoding/json"
	"fmt"
	"testing"
)

// 端到端 session 序列测试 —— 模拟一个完整的 Agent 模式 turn：
//   1. SystemInit       (server → client)
//   2. UserMessage      (client → server)
//   3. AssistantPartial (server → client, stream_event)
//   4. ToolProgress     (server → client)
//   5. ControlRequest:can_use_tool  (server → client)
//   6. ControlResponse:success      (client → server, allow)
//   7. AssistantMessage (server → client)
//   8. ResultSuccess    (server → client)
//   9. KeepAlive        (server → client, lifecycle)
//
// 验证每帧能 marshal/unmarshal，dispatch 类型正确，且整套帧都通过 IsStdinMessage/IsStdoutMessage
// 在正确方向。
func TestServiceFrame_FullSessionSequence(t *testing.T) {
	frames := []struct {
		name    string
		raw     string
		wantTyp string
		stdin   bool // 是否合法 stdin
		stdout  bool // 是否合法 stdout
	}{
		{
			name: "1.system_init",
			raw: `{
				"type": "system", "subtype": "init",
				"agents": [], "apiKeySource": "env", "betas": [],
				"claude_code_version": "2.0.0", "cwd": "/repo",
				"tools": ["Bash"], "mcp_servers": [], "model": "claude-3-7",
				"permissionMode": "default", "slash_commands": [], "output_style": "default",
				"uuid": "init1", "session_id": "s1"
			}`,
			wantTyp: "*sdkproto.SDKSystemInit",
			stdin:   true, // 也允许 echo back 但通常 server → client
			stdout:  true,
		},
		{
			name: "2.user_message",
			raw: `{
				"type": "user",
				"message": { "role": "user", "content": "list files in /tmp" },
				"uuid": "u1", "session_id": "s1"
			}`,
			wantTyp: "*sdkproto.SDKUserMessage",
			stdin:   true,
			stdout:  true,
		},
		{
			name: "3.assistant_partial",
			raw: `{
				"type": "stream_event",
				"event": { "type": "content_block_delta", "delta": "I'll" },
				"uuid": "p1", "session_id": "s1"
			}`,
			wantTyp: "*sdkproto.SDKPartialAssistantMessage",
			stdin:   true,
			stdout:  true,
		},
		{
			name: "4.tool_progress",
			raw: `{
				"type": "tool_progress", "tool_use_id": "tu1", "tool_name": "Bash",
				"elapsed_time_seconds": 0.5, "uuid": "tp1", "session_id": "s1"
			}`,
			wantTyp: "*sdkproto.SDKToolProgress",
			stdin:   true,
			stdout:  true,
		},
		{
			name: "5.control_request_permission",
			raw: `{
				"type": "control_request", "request_id": "perm1",
				"request": {
					"subtype": "can_use_tool",
					"tool_name": "Bash",
					"input": { "command": "ls /tmp" },
					"tool_use_id": "tu1"
				}
			}`,
			wantTyp: "*sdkproto.SDKControlRequest",
			stdin:   true,
			stdout:  true,
		},
		{
			name: "6.control_response_success",
			raw: `{
				"type": "control_response",
				"response": {
					"subtype": "success",
					"request_id": "perm1",
					"response": { "behavior": "allow" }
				}
			}`,
			wantTyp: "*sdkproto.SDKControlResponse",
			stdin:   false, // ControlResponse 是 client → server 方向但 IsStdinMessage 返回 false
			stdout:  true,
		},
		{
			name: "7.assistant_message",
			raw: `{
				"type": "assistant",
				"message": { "role": "assistant", "content": "Done." },
				"uuid": "a1", "session_id": "s1"
			}`,
			wantTyp: "*sdkproto.SDKAssistantMessage",
			stdin:   true,
			stdout:  true,
		},
		{
			name: "8.result_success",
			raw: `{
				"type": "result", "subtype": "success",
				"duration_ms": 2000, "duration_api_ms": 1500,
				"is_error": false, "num_turns": 1,
				"result": "Listed files",
				"stop_reason": "end_turn",
				"total_cost_usd": 0.001,
				"usage": {}, "modelUsage": {},
				"permission_denials": [],
				"uuid": "r1", "session_id": "s1"
			}`,
			wantTyp: "*sdkproto.SDKResultSuccess",
			stdin:   true,
			stdout:  true,
		},
		{
			name: "9.keep_alive_lifecycle",
			raw: `{ "type": "keep_alive", "ts": 1700000000000 }`,
			wantTyp: "*sdkproto.KeepAlive",
			stdin:   false, // KeepAlive 仅 server → client
			stdout:  true,
		},
	}

	for _, tc := range frames {
		t.Run(tc.name, func(t *testing.T) {
			// 1. dispatcher 选对类型
			inner, err := UnmarshalFrame([]byte(tc.raw))
			if err != nil {
				t.Fatalf("UnmarshalFrame: %v\nraw=%s", err, tc.raw)
			}
			gotT := fmt.Sprintf("%T", inner)
			if gotT != tc.wantTyp {
				t.Fatalf("dispatched to %s, want %s", gotT, tc.wantTyp)
			}

			// 2. 重新 marshal 后能重新解析
			out, err := json.Marshal(inner)
			if err != nil {
				t.Fatalf("re-marshal: %v", err)
			}
			inner2, err := UnmarshalFrame(out)
			if err != nil {
				t.Fatalf("re-unmarshal: %v\nout=%s", err, out)
			}
			if fmt.Sprintf("%T", inner2) != tc.wantTyp {
				t.Errorf("re-dispatch lost type")
			}

			// 3. 方向校验
			if got := IsStdinMessage(inner); got != tc.stdin {
				t.Errorf("IsStdinMessage = %v, want %v", got, tc.stdin)
			}
			if got := IsStdoutMessage(inner); got != tc.stdout {
				t.Errorf("IsStdoutMessage = %v, want %v", got, tc.stdout)
			}
		})
	}
}

// 验证 control_cancel_request 仅在 stdin 方向（client → server）。
func TestServiceFrame_ControlCancelDirection(t *testing.T) {
	raw := `{ "type": "control_cancel_request", "request_id": "perm1" }`
	inner, err := UnmarshalFrame([]byte(raw))
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !IsStdinMessage(inner) {
		t.Error("ControlCancelRequest 应该是 stdin 合法")
	}
	if IsStdoutMessage(inner) {
		t.Error("ControlCancelRequest 不应该出现在 stdout 方向")
	}
}

// UpdateEnvironmentVariables 仅在 stdin 方向（client → server）。
func TestServiceFrame_UpdateEnvVarsDirection(t *testing.T) {
	raw := `{
		"type": "update_environment_variables",
		"variables": { "FOO": "bar" }
	}`
	inner, err := UnmarshalFrame([]byte(raw))
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !IsStdinMessage(inner) {
		t.Error("UpdateEnvironmentVariables 应该是 stdin 合法")
	}
	if IsStdoutMessage(inner) {
		t.Error("UpdateEnvironmentVariables 不应该出现在 stdout 方向")
	}
}

func TestServiceFrame_LifecycleAll(t *testing.T) {
	cases := []struct {
		raw     string
		wantTyp string
	}{
		{`{"type": "keep_alive"}`, "*sdkproto.KeepAlive"},
		{`{"type": "biumind.session_desynced", "session_id": "s1"}`, "*sdkproto.SessionDesynced"},
		{`{"type": "biumind.session_paused", "session_id": "s1", "reason": "rate_limited"}`, "*sdkproto.SessionPaused"},
		{`{"type": "biumind.session_resumed", "session_id": "s1"}`, "*sdkproto.SessionResumed"},
		{`{"type": "biumind.session_primary_promoted", "session_id": "s1", "primary_replica": "runtime-x9p2k"}`, "*sdkproto.SessionPrimaryPromoted"},
	}
	for _, c := range cases {
		inner, err := UnmarshalFrame([]byte(c.raw))
		if err != nil {
			t.Fatalf("%s: %v", c.raw, err)
		}
		if got := fmt.Sprintf("%T", inner); got != c.wantTyp {
			t.Errorf("%s → %s, want %s", c.raw, got, c.wantTyp)
		}
	}
}

func TestServiceFrame_UnknownType(t *testing.T) {
	_, err := UnmarshalFrame([]byte(`{"type": "totally_unknown"}`))
	if err == nil {
		t.Error("expected error for unknown frame type")
	}
}
