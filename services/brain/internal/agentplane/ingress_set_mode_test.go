// set_permission_mode 路由单测 —— 不起 WS 也不起 NATS,直接调
// maybeRouteSetPermissionMode 验证帧识别与分支:非目标帧不触发 /
// 进程内 chat 会话(envID==nil)no-op / 有 envID 但 Queue 未接线不
// panic。envID + Queue.EnqueueControl 命中路径需真 broker,与 cancel
// 一样归 ingress_real_test.go 那套集成套件。

package agentplane

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"
)

func newModeTestIngress(t *testing.T) *Ingress {
	t.Helper()
	return &Ingress{
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

// 普通 user 帧 / 其它 control 帧不触发路由。
func TestMaybeRouteSetPermissionMode_IgnoresOtherFrames(t *testing.T) {
	i := newModeTestIngress(t)
	envID := uuid.New()
	for _, body := range []string{
		`{"type":"user","message":{"role":"user","content":"hi"}}`,
		`{"type":"control_cancel_request","request_id":"r1"}`,
		`{"type":"control_request","request_id":"r2","request":{"subtype":"set_model","model":"x"}}`,
		`{"type":"control_request","request_id":"r3"}`, // 缺 request 体
	} {
		if i.maybeRouteSetPermissionMode(context.Background(), uuid.New(), &envID, []byte(body)) {
			t.Errorf("should not route %s", body)
		}
	}
}

// 损坏 JSON 不 panic,返 false。
func TestMaybeRouteSetPermissionMode_MalformedJSON(t *testing.T) {
	i := newModeTestIngress(t)
	if i.maybeRouteSetPermissionMode(context.Background(), uuid.New(), nil, []byte("not-json")) {
		t.Error("malformed json should return false")
	}
}

// 进程内 chat 会话(envID==nil)没有权限内核可应用 —— 识别后 no-op,
// 不 panic。
func TestMaybeRouteSetPermissionMode_InProcessChatNoop(t *testing.T) {
	i := newModeTestIngress(t)
	body := []byte(`{"type":"control_request","request_id":"r9","request":{"subtype":"set_permission_mode","mode":"auto"}}`)
	if !i.maybeRouteSetPermissionMode(context.Background(), uuid.New(), nil, body) {
		t.Error("set_permission_mode frame should be recognised")
	}
}

// 有 envID 但 Queue 未接线(dev 无 NATS):识别但不投递、不 panic。
func TestMaybeRouteSetPermissionMode_NilQueueWithEnvID(t *testing.T) {
	i := newModeTestIngress(t)
	envID := uuid.New()
	body := []byte(`{"type":"control_request","request_id":"r9","request":{"subtype":"set_permission_mode","mode":"plan"}}`)
	if !i.maybeRouteSetPermissionMode(context.Background(), uuid.New(), &envID, body) {
		t.Error("set_permission_mode frame should be recognised even without queue")
	}
}

// 完整投递路径:fakeJS 捕获 EnqueueControl 的 subject + payload —
// type/session_id/request_id/mode 四元组是 daemon worker
// handleControl 的消费契约。
func TestMaybeRouteSetPermissionMode_EnqueuesToDaemonControlQueue(t *testing.T) {
	js := &fakeJS{}
	ingress := NewIngress(nil, nil, nil, discardLogger())
	ingress.SetQueue(NewQueue(js))

	sessionID := uuid.New()
	envID := uuid.New()
	body := []byte(`{"type":"control_request","request_id":"req-mode-1","request":{"subtype":"set_permission_mode","mode":"auto"}}`)
	if !ingress.maybeRouteSetPermissionMode(context.Background(), sessionID, &envID, body) {
		t.Fatal("set_permission_mode should be claimed")
	}

	js.mu.Lock()
	if len(js.publishes) != 1 {
		js.mu.Unlock()
		t.Fatalf("publishes = %d, want 1", len(js.publishes))
	}
	if subj := js.publishes[0].Subject; subj != ControlSubject(envID.String()) {
		js.mu.Unlock()
		t.Fatalf("subject = %q, want %q", subj, ControlSubject(envID.String()))
	}
	js.mu.Unlock()

	got := controlQueuePayload(t, js, 0)
	if got["type"] != "set_permission_mode" {
		t.Errorf("payload type = %v, want set_permission_mode", got["type"])
	}
	if got["session_id"] != sessionID.String() || got["request_id"] != "req-mode-1" {
		t.Errorf("payload addressing wrong: %v", got)
	}
	if got["mode"] != "auto" {
		t.Errorf("payload mode = %v, want auto", got["mode"])
	}
}
