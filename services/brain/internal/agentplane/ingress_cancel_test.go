// Cancel 路由单测 —— 不起 WS 也不起 NATS, 直接调 maybeRouteCancel
// 验证三条路径:chatInterrupt 命中 / 没 chat 也没 envID 静默 drop /
// 非 cancel 帧不触发任何事。完整的 envID + Queue.EnqueueControl 端到
// 端验证(需真 broker)放在 ingress_real_test.go。

package agentplane

import (
	"context"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
)

func newCancelTestIngress(t *testing.T) *Ingress {
	t.Helper()
	return &Ingress{
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

// TestMaybeRouteCancel_IgnoresNonCancelFrame — 普通 user message 不走
// cancel 路由, 返 false, callback 不该触发。
func TestMaybeRouteCancel_IgnoresNonCancelFrame(t *testing.T) {
	i := newCancelTestIngress(t)
	called := atomic.Bool{}
	i.SetChatInterrupt(func(_ uuid.UUID) bool {
		called.Store(true)
		return true
	})

	body := []byte(`{"type":"user","message":{"role":"user","content":"hi"}}`)
	got := i.maybeRouteCancel(context.Background(), uuid.New(), nil, body)
	if got {
		t.Errorf("maybeRouteCancel = true on non-cancel frame")
	}
	if called.Load() {
		t.Errorf("chatInterrupt called for non-cancel frame")
	}
}

// TestMaybeRouteCancel_MalformedJSON — 损坏 JSON 不该 panic, 返 false。
func TestMaybeRouteCancel_MalformedJSON(t *testing.T) {
	i := newCancelTestIngress(t)
	got := i.maybeRouteCancel(context.Background(), uuid.New(), nil, []byte("not-json"))
	if got {
		t.Errorf("maybeRouteCancel = true on malformed json")
	}
}

// TestMaybeRouteCancel_ChatInterruptHits — chat 模式有注入回调:命中即结束,
// 不再尝试 environment 路径。callback 拿到正确 sessionID。
func TestMaybeRouteCancel_ChatInterruptHits(t *testing.T) {
	i := newCancelTestIngress(t)
	wantSID := uuid.New()
	var seenSID uuid.UUID
	i.SetChatInterrupt(func(sid uuid.UUID) bool {
		seenSID = sid
		return true // 命中
	})

	body := []byte(`{"type":"control_cancel_request","request_id":"r1"}`)
	got := i.maybeRouteCancel(context.Background(), wantSID, nil, body)
	if !got {
		t.Errorf("maybeRouteCancel = false on cancel frame")
	}
	if seenSID != wantSID {
		t.Errorf("chatInterrupt got sid=%v, want %v", seenSID, wantSID)
	}
}

// TestMaybeRouteCancel_ChatMissesAndNoEnv — chat 不命中且没 environment_id:
// 静默 drop。不该 panic, 不该误触发 queue。返 true 表示 cancel 帧已被识别。
func TestMaybeRouteCancel_ChatMissesAndNoEnv(t *testing.T) {
	i := newCancelTestIngress(t)
	i.SetChatInterrupt(func(_ uuid.UUID) bool { return false }) // miss
	body := []byte(`{"type":"control_cancel_request","request_id":"r1"}`)
	got := i.maybeRouteCancel(context.Background(), uuid.New(), nil, body)
	if !got {
		t.Errorf("maybeRouteCancel should still return true (cancel was identified)")
	}
}

// TestMaybeRouteCancel_NilQueueWithEnvID — 有 environment_id 但 queue 没接
// (dev 配置无 NATS): 不该 panic, 走 "Queue not wired" 日志。
func TestMaybeRouteCancel_NilQueueWithEnvID(t *testing.T) {
	i := newCancelTestIngress(t)
	envID := uuid.New()
	body := []byte(`{"type":"control_cancel_request","request_id":"r1"}`)
	got := i.maybeRouteCancel(context.Background(), uuid.New(), &envID, body)
	if !got {
		t.Errorf("maybeRouteCancel should return true (cancel identified)")
	}
}

// TestSetChatInterrupt_Idempotent — 重复设置 callback 应该正确 override,
// 不留下旧的回调。运行时切换 chat handler 不该泄漏。
func TestSetChatInterrupt_Idempotent(t *testing.T) {
	i := newCancelTestIngress(t)
	c1 := atomic.Int32{}
	c2 := atomic.Int32{}
	i.SetChatInterrupt(func(_ uuid.UUID) bool { c1.Add(1); return true })
	i.SetChatInterrupt(func(_ uuid.UUID) bool { c2.Add(1); return true })

	body := []byte(`{"type":"control_cancel_request"}`)
	i.maybeRouteCancel(context.Background(), uuid.New(), nil, body)

	if c1.Load() != 0 {
		t.Errorf("old chatInterrupt called %d times after override", c1.Load())
	}
	if c2.Load() != 1 {
		t.Errorf("new chatInterrupt called %d times, want 1", c2.Load())
	}
}
