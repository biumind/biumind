// ChatRunner.InterruptSession 单测 — 验证 inflight registry 维护正确,
// 命中时 cancel cause 被设为 biumindkit.ErrInterrupted。完整跑 RunSession
// 走 LLM 的端到端验证不在这里(那边需要 NATS / Anthropic upstream)。

package agentplane

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/pkg/biumindkit"
	"github.com/google/uuid"
)

func newTestChatRunner() *ChatRunner {
	return &ChatRunner{
		Logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
		inflight:        map[uuid.UUID]context.CancelCauseFunc{},
		cancelStartedAt: map[uuid.UUID]time.Time{},
	}
}

// TestChatRunner_InterruptSession_Miss — session 没注册时返 false 不 panic。
func TestChatRunner_InterruptSession_Miss(t *testing.T) {
	cr := newTestChatRunner()
	if cr.InterruptSession(uuid.New()) {
		t.Errorf("Interrupt on empty registry should return false")
	}
}

// TestChatRunner_InterruptSession_Hit — 注册后调用,cancel 应被触发,cause 为
// biumindkit.ErrInterrupted。这是 chat 模式 cancel 的核心契约。
func TestChatRunner_InterruptSession_Hit(t *testing.T) {
	cr := newTestChatRunner()
	sid := uuid.New()

	// 模拟 runSessionImpl 的 ctx + cancel 注册路径。
	ctx, cancel := context.WithCancelCause(context.Background())
	cr.trackInflight(sid, cancel)
	defer cr.untrackInflight(sid)

	if !cr.InterruptSession(sid) {
		t.Errorf("InterruptSession returned false on registered session")
	}
	if ctx.Err() == nil {
		t.Fatal("ctx not canceled after Interrupt")
	}
	if !errors.Is(context.Cause(ctx), biumindkit.ErrInterrupted) {
		t.Errorf("ctx cause = %v, want biumindkit.ErrInterrupted",
			context.Cause(ctx))
	}
}

// TestChatRunner_InterruptSession_Idempotent — 已 untrack / 多次调用都不 panic。
func TestChatRunner_InterruptSession_Idempotent(t *testing.T) {
	cr := newTestChatRunner()
	sid := uuid.New()
	_, cancel := context.WithCancelCause(context.Background())
	cr.trackInflight(sid, cancel)

	if !cr.InterruptSession(sid) {
		t.Fatal("first Interrupt should hit")
	}
	// untrack后再调
	cr.untrackInflight(sid)
	if cr.InterruptSession(sid) {
		t.Errorf("InterruptSession after untrack should be miss")
	}
	// 重复 untrack 不 panic
	cr.untrackInflight(sid)
}

// TestChatRunner_UntrackInflight_LatencyHook — InterruptSession 触发后
// untrackInflight 返 true(被 cancel 过 → metrics 已 observe);没触发
// cancel 时返 false。这把 ChatRunner 跟 metrics 的耦合契约锁死,后续
// 改 cancel 路径时编译期就能发现 metrics 漏埋点。
func TestChatRunner_UntrackInflight_LatencyHook(t *testing.T) {
	cr := newTestChatRunner()

	// Case A: 注册 + cancel + untrack → 返 true (observe 了 latency)
	sidA := uuid.New()
	_, cancelA := context.WithCancelCause(context.Background())
	cr.trackInflight(sidA, cancelA)
	cr.InterruptSession(sidA)
	// 让 wall time 至少跨过 1ms,避免延迟为 0 让 prometheus 累计 0
	time.Sleep(1 * time.Millisecond)
	if !cr.untrackInflight(sidA) {
		t.Errorf("untrackInflight after Interrupt should return true")
	}

	// Case B: 只注册没 cancel → 返 false (没 observe 没意义)
	sidB := uuid.New()
	_, cancelB := context.WithCancelCause(context.Background())
	cr.trackInflight(sidB, cancelB)
	if cr.untrackInflight(sidB) {
		t.Errorf("untrackInflight without Interrupt should return false")
	}

	// Case C: 重复 cancel 同 sessionID — 时间戳只取第一次,untrack 仍返 true
	sidC := uuid.New()
	_, cancelC := context.WithCancelCause(context.Background())
	cr.trackInflight(sidC, cancelC)
	cr.InterruptSession(sidC)
	first := cr.cancelStartedAt[sidC]
	time.Sleep(1 * time.Millisecond)
	cr.InterruptSession(sidC) // 第二次,不应覆盖时间戳
	second := cr.cancelStartedAt[sidC]
	if !first.Equal(second) {
		t.Errorf("repeated InterruptSession should NOT reset timestamp; first=%v second=%v",
			first, second)
	}
	if !cr.untrackInflight(sidC) {
		t.Errorf("untrackInflight Case C should return true")
	}
}
