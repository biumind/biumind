// S12-1 router 路径分流测试 —— AgentPlane integration 注入后 Inbound
// 走新路径；no_runtime / 其它错误时降级到老 JS / HTTP。

package router

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/biumind/biumind/services/channels/internal/envelope"
)

// fakeAgentPlane 记录调用 + 配置返回值，让测试控分支。
type fakeAgentPlane struct {
	created     atomic.Int32
	subscribed  atomic.Int32
	createErr   error
	sessionID   string
	subscribeErr error
}

func (f *fakeAgentPlane) integration() *AgentPlaneIntegration {
	return &AgentPlaneIntegration{
		CreateTaskSession: func(_ context.Context, _ AgentPlaneCreateReq) (string, error) {
			f.created.Add(1)
			if f.createErr != nil {
				return "", f.createErr
			}
			return f.sessionID, nil
		},
		SubscribeAndReply: func(_ context.Context, _ string, _ AgentPlaneReply) error {
			f.subscribed.Add(1)
			return f.subscribeErr
		},
	}
}

// noRuntimeErr 模拟 brain 503 no_runtime_available。
type noRuntimeErrType struct{}

func (noRuntimeErrType) Error() string { return "agentplane: HTTP 503: no_runtime_available" }

// router 内部用 errors.As 不到具体 type；AgentPlaneIntegration 的 createErr
// 直接 type-check 通过 IsNoRuntime() 接口判断。这里简化：用一个实现了
// IsNoRuntime() bool 的 sentinel struct。
type fakeNoRuntimeErr struct{}

func (fakeNoRuntimeErr) Error() string    { return "no_runtime_available" }
func (fakeNoRuntimeErr) IsNoRuntime() bool { return true }

func TestRouter_AgentPlane_HappyPath(t *testing.T) {
	r := newTestRouter(t)
	fap := &fakeAgentPlane{sessionID: "550e8400-e29b-41d4-a716-446655440000"}
	r.AgentPlane = fap.integration()

	r.Inbound(context.Background(), []envelope.Envelope{{
		Channel:   "telegram",
		MessageID: "msg-1",
		Text:      "hello",
		Sender:    envelope.Sender{PlatformID: "u-1", DisplayName: "Alice"},
	}})

	if got := fap.created.Load(); got != 1 {
		t.Errorf("CreateTaskSession calls=%d want 1", got)
	}
	if got := fap.subscribed.Load(); got != 1 {
		t.Errorf("SubscribeAndReply calls=%d want 1", got)
	}
}

func TestRouter_AgentPlane_FallsBackOnError(t *testing.T) {
	// 无 NATS / RuntimeURL 时 fallback 路径自然空运行 —— 验"创建失败时
	// AgentPlane.SubscribeAndReply 不应该被调用"
	r := newTestRouter(t)
	fap := &fakeAgentPlane{createErr: errors.New("boom")}
	r.AgentPlane = fap.integration()

	r.Inbound(context.Background(), []envelope.Envelope{{
		Channel: "telegram", MessageID: "x", Text: "hi",
	}})

	if got := fap.created.Load(); got != 1 {
		t.Errorf("CreateTaskSession calls=%d want 1 (attempted)", got)
	}
	if got := fap.subscribed.Load(); got != 0 {
		t.Errorf("SubscribeAndReply calls=%d want 0 (create failed → no listener)", got)
	}
	// envelope 仍记到 recent —— 老路径 fallthrough 写日志后继续
	if len(r.Recent(10)) != 1 {
		t.Errorf("envelope should still be recorded for replay")
	}
}

func TestRouter_AgentPlane_NoIntegration(t *testing.T) {
	// AgentPlane 没注入时 Inbound 直接走老路径，不试图 CreateTaskSession
	r := newTestRouter(t)

	r.Inbound(context.Background(), []envelope.Envelope{{
		Channel: "telegram", MessageID: "x", Text: "hi",
	}})

	if len(r.Recent(10)) != 1 {
		t.Errorf("envelope should be recorded")
	}
}

func TestRouter_AgentPlane_SubscribeFailureNotFatal(t *testing.T) {
	r := newTestRouter(t)
	fap := &fakeAgentPlane{
		sessionID:    "550e8400-e29b-41d4-a716-446655440000",
		subscribeErr: errors.New("nats unreachable"),
	}
	r.AgentPlane = fap.integration()

	r.Inbound(context.Background(), []envelope.Envelope{{
		Channel: "telegram", MessageID: "x", Text: "hi",
	}})

	// session 创建成功 → 即使 listener 起不来也算 "handled"，不降级
	if got := fap.created.Load(); got != 1 {
		t.Errorf("CreateTaskSession calls=%d", got)
	}
	if got := fap.subscribed.Load(); got != 1 {
		t.Errorf("SubscribeAndReply attempted=%d (must still try)", got)
	}
}
