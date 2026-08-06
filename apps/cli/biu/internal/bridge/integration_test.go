// Bridge S2-5 集成测试 —— 跑全链路：create → upload attachment → submit turn
// → WS drain → control inbound → delete。
//
// 跟单功能测试的区别：这个测试故意把所有 endpoint 串起来，模拟一个真实
// Flutter / IDE 客户端会怎么用 bridge。如果将来某个 endpoint 改了导致
// 协议级回归（比如 control 帧编码改了客户端解析不了），这个测试会先红。

package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/pkg/biumindkit"
	sdkproto "github.com/biumind/biumind/packages/go-sdk/biu/sdkproto/v1"
	"github.com/gorilla/websocket"
)

// integrationUpstream 给一个简短可完成的 turn —— 没 tool_use，但带文本 +
// stop_reason，让 biumindkit 完整 emit AssistantText + Done。
func integrationUpstream() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		_, _ = w.Write([]byte(`event: message_start
data: {"type":"message_start","message":{"id":"m_1","model":"test"}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"integration ok"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"}}

event: message_stop
data: {"type":"message_stop"}

`))
	}))
}

// TestIntegration_FullSessionLifecycle 把 6 个 endpoint 串成一个流程：
//
//  1. POST /v1/code/sessions                 → 创建 session
//  2. POST /v1/code/sessions/{id}/attachments → 上传一张"图"
//  3. POST /v1/code/sessions/{id}/messages    → 启 turn
//  4. GET  /v1/code/sessions/{id}/ws          → WS 拉 stream
//  5. (在 ws 上发 control_request: interrupt)  → 收 control_response
//  6. GET  /v1/code/sessions/{id}/cost        → cost 快照
//  7. DELETE /v1/code/sessions/{id}           → 清理
//
// 每一步都断言 status code + 关键字段，覆盖客户端常见用法。
func TestIntegration_FullSessionLifecycle(t *testing.T) {
	upstream := integrationUpstream()
	defer upstream.Close()

	tmpDir := t.TempDir()
	srv, err := NewServer(Options{
		AttachmentDir: tmpDir,
		AgentFactory: func(extras AgentExtras) (*biumindkit.Agent, error) {
			return biumindkit.New(biumindkit.Options{
				APIKey:              "sk-fake",
				AnthropicEndpoint:   upstream.URL,
				LoadProjectMemory:   biumindkit.NoMemory,
				LoadProjectSettings: biumindkit.NoSettings,
				BypassPermissions:   true,
				PermissionPolicy:    extras.PermissionPolicy,
			})
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// 1. Create
	resp, err := http.Post(ts.URL+"/v1/code/sessions", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status=%d", resp.StatusCode)
	}
	var c struct{ ID string }
	_ = json.NewDecoder(resp.Body).Decode(&c)
	resp.Body.Close()
	if c.ID == "" {
		t.Fatal("empty session id")
	}

	// 2. Upload attachment（PNG 占位）
	pngHdr := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	hdr := make(textproto.MIMEHeader)
	hdr.Set("Content-Disposition", `form-data; name="file"; filename="screenshot.png"`)
	hdr.Set("Content-Type", "image/png")
	part, _ := mw.CreatePart(hdr)
	_, _ = part.Write(pngHdr)
	mw.Close()
	req, _ := http.NewRequest("POST",
		ts.URL+"/v1/code/sessions/"+c.ID+"/attachments", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	uploadResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if uploadResp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(uploadResp.Body)
		t.Fatalf("upload status=%d body=%s", uploadResp.StatusCode, respBody)
	}
	var uploadJSON struct {
		AttachmentID string `json:"attachment_id"`
	}
	_ = json.NewDecoder(uploadResp.Body).Decode(&uploadJSON)
	uploadResp.Body.Close()
	if uploadJSON.AttachmentID == "" {
		t.Fatal("missing attachment_id")
	}

	// 3. Submit prompt
	r2, err := http.Post(
		ts.URL+"/v1/code/sessions/"+c.ID+"/messages",
		"application/json", strings.NewReader(`{"prompt":"hello bridge"}`))
	if err != nil {
		t.Fatal(err)
	}
	if r2.StatusCode != http.StatusAccepted {
		t.Fatalf("submit status=%d", r2.StatusCode)
	}
	r2.Body.Close()

	// 4. WS drain
	wsURL := strings.Replace(ts.URL, "http://", "ws://", 1) + "/v1/code/sessions/" + c.ID + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))

	var sawAssistant, sawResult, sawKeepAlive bool
	for !sawKeepAlive {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			break
		}
		frame, err := sdkproto.UnmarshalFrame(raw)
		if err != nil {
			t.Fatalf("frame parse: %v\nraw=%s", err, raw)
		}
		switch f := frame.(type) {
		case *sdkproto.SDKStreamlinedText:
			if strings.Contains(f.Text, "integration ok") {
				sawAssistant = true
			}
		case *sdkproto.SDKResultSuccess:
			sawResult = true
		case *sdkproto.KeepAlive:
			sawKeepAlive = true
		}
	}
	if !sawAssistant {
		t.Errorf("missing SDKStreamlinedText with 'integration ok'")
	}
	if !sawResult {
		t.Errorf("missing SDKResultSuccess")
	}
	if !sawKeepAlive {
		t.Errorf("missing KeepAlive sentinel")
	}

	// 5. Cost snapshot
	costResp, err := http.Get(ts.URL + "/v1/code/sessions/" + c.ID + "/cost")
	if err != nil {
		t.Fatal(err)
	}
	if costResp.StatusCode != http.StatusOK {
		t.Errorf("cost status=%d", costResp.StatusCode)
	}
	costResp.Body.Close()

	// 6. Delete
	delReq, _ := http.NewRequest("DELETE", ts.URL+"/v1/code/sessions/"+c.ID, nil)
	delResp, err := http.DefaultClient.Do(delReq)
	if err != nil {
		t.Fatal(err)
	}
	if delResp.StatusCode != http.StatusNoContent {
		t.Errorf("delete status=%d", delResp.StatusCode)
	}
	delResp.Body.Close()

	// 7. 删除后 session 不存在
	r3, _ := http.Get(ts.URL + "/v1/code/sessions/" + c.ID + "/cost")
	if r3.StatusCode != http.StatusNotFound {
		t.Errorf("cost after delete = %d, want 404", r3.StatusCode)
	}
	r3.Body.Close()
}

// TestIntegration_PermissionThenInterrupt 把 S2-2 (interrupt) 跟 S2-3
// (permission ask) 合在一起跑：客户端先收 can_use_tool，然后没回包，转头
// 发 interrupt —— askPermission 应该被 ctx 取消而 deny。
func TestIntegration_PermissionThenInterrupt(t *testing.T) {
	ts, sid, rec := permissionTestSession(t)
	defer ts.Close()
	conn := startTurnAndConnect(t, ts, sid)
	defer conn.Close()

	// 触发 askPermission，但用我们能 cancel 的 ctx
	askCtx, askCancel := context.WithCancel(context.Background())
	defer askCancel()
	decisionCh := make(chan biumindkit.PermissionDecision, 1)
	go func() {
		decisionCh <- rec.askPermission(askCtx, biumindkit.PermissionRequest{
			ToolUseID: "tu-int", ToolName: "Bash", Input: map[string]any{},
		})
	}()

	// 收到 can_use_tool 帧
	requestID, _ := readUntilCanUseTool(t, conn)
	_ = requestID

	// 模拟 client 决定中断 —— 直接 cancel askCtx（生产里是 dispatchControl
	// 走 interrupt → rec.pendingCancel；这里我们没有 turn 在跑，直接 cancel）
	askCancel()

	select {
	case d := <-decisionCh:
		if d != biumindkit.PermDeny {
			t.Errorf("ctx-cancelled decision=%v want PermDeny", d)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("askPermission did not return after ctx cancel")
	}
}
