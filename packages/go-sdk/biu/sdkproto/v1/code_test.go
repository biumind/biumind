package sdkproto

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"
)

// codeRoundTrip 验证 code 帧经 UnmarshalFrame（通用入口，按 type 分发）选对类型，
// 且 re-marshal 不丢关键字段。走 UnmarshalFrame 而非 UnmarshalCodeFrame 直调，
// 是为了同时覆盖 service.go 里新加的分发分支。
func codeRoundTrip(t *testing.T, name, raw string, want Frame, requireSubstrings []string) {
	t.Helper()
	f, err := UnmarshalFrame([]byte(raw))
	if err != nil {
		t.Fatalf("%s: UnmarshalFrame: %v\nraw=%s", name, err, raw)
	}
	if got, wantT := fmt.Sprintf("%T", f), fmt.Sprintf("%T", want); got != wantT {
		t.Fatalf("%s: dispatched to %s, want %s", name, got, wantT)
	}
	if _, ok := f.(CodeFrame); !ok {
		t.Fatalf("%s: %T does not satisfy CodeFrame", name, f)
	}
	out, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("%s: re-marshal: %v", name, err)
	}
	for _, s := range requireSubstrings {
		if !bytes.Contains(out, []byte(s)) {
			t.Errorf("%s: marshaled output missing %q\ngot=%s", name, s, out)
		}
	}
}

func TestCodeFrame_Request(t *testing.T) {
	codeRoundTrip(t, "request", `{
		"type": "code_request",
		"request_id": "r1",
		"method": "git.status",
		"params": {"cwd": "/tmp/repo"}
	}`, &CodeRequest{}, []string{`"type":"code_request"`, `"request_id":"r1"`, `"method":"git.status"`, `"cwd":"/tmp/repo"`})
}

func TestCodeFrame_Response(t *testing.T) {
	codeRoundTrip(t, "response", `{
		"type": "code_response",
		"request_id": "r1",
		"ok": true,
		"result": {"branch": "main"}
	}`, &CodeResponse{}, []string{`"type":"code_response"`, `"ok":true`, `"branch":"main"`})
}

func TestCodeFrame_ResponseError(t *testing.T) {
	codeRoundTrip(t, "response_err", `{
		"type": "code_response",
		"request_id": "r2",
		"ok": false,
		"error": "no such repo"
	}`, &CodeResponse{}, []string{`"ok":false`, `"error":"no such repo"`})
}

// PTY 字节经 base64 往返不丢。"hi" → base64 "aGk=".
func TestCodeFrame_PtyChunk(t *testing.T) {
	raw := `{"type":"code_pty_chunk","pty_id":"p1","data":"aGk="}`
	f, err := UnmarshalFrame([]byte(raw))
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	chunk, ok := f.(*CodePtyChunk)
	if !ok {
		t.Fatalf("dispatched to %T, want *CodePtyChunk", f)
	}
	if chunk.PtyID != "p1" {
		t.Errorf("pty_id = %q, want p1", chunk.PtyID)
	}
	if string(chunk.Data) != "hi" {
		t.Errorf("data = %q, want hi", chunk.Data)
	}
	out, _ := json.Marshal(chunk)
	if !bytes.Contains(out, []byte(`"data":"aGk="`)) {
		t.Errorf("re-marshal lost base64 data: %s", out)
	}
}

func TestCodeFrame_PtyInput(t *testing.T) {
	codeRoundTrip(t, "pty_input", `{"type":"code_pty_input","pty_id":"p1","data":"bHM="}`,
		&CodePtyInput{}, []string{`"type":"code_pty_input"`, `"pty_id":"p1"`, `"data":"bHM="`})
}

func TestCodeFrame_PtyResize(t *testing.T) {
	codeRoundTrip(t, "pty_resize", `{"type":"code_pty_resize","pty_id":"p1","cols":120,"rows":40}`,
		&CodePtyResize{}, []string{`"cols":120`, `"rows":40`})
}

func TestCodeFrame_PtyExit(t *testing.T) {
	codeRoundTrip(t, "pty_exit", `{"type":"code_pty_exit","pty_id":"p1","exit_code":0}`,
		&CodePtyExit{}, []string{`"type":"code_pty_exit"`, `"exit_code":0`})
}

// code 帧在两个方向上都应被协议层接受（StdinMessage = 客户端→服务端，
// StdoutMessage = 服务端→客户端）。请求/输入/resize 是入站，chunk/response/exit
// 是出站；但 IsStdin/IsStdoutMessage 的当前规则只 deny ControlResponse /
// ControlCancelRequest / 部分 lifecycle —— code 帧不在 deny 列表，双向都应放行。
func TestCodeFrame_DirectionGuards(t *testing.T) {
	frames := []Frame{
		&CodeRequest{Type: TypeCodeRequest},
		&CodeResponse{Type: TypeCodeResponse},
		&CodePtyChunk{Type: TypeCodePtyChunk},
		&CodePtyInput{Type: TypeCodePtyInput},
		&CodePtyResize{Type: TypeCodePtyResize},
		&CodePtyExit{Type: TypeCodePtyExit},
	}
	for _, f := range frames {
		if !IsStdinMessage(f) {
			t.Errorf("%T: IsStdinMessage = false, want true", f)
		}
		if !IsStdoutMessage(f) {
			t.Errorf("%T: IsStdoutMessage = false, want true", f)
		}
	}
}
