// 老路径（forwardToRuntime + bus publish + memorybridge → runtime forward）
// S11-4 删了；这里只剩 dedup / Recent ring buffer 等不依赖下游的纯路由
// 行为测试。AgentPlane integration 路径覆盖在 agentplane_test.go。

package router

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/biumind/biumind/services/channels/internal/envelope"
)

func newTestRouter(t *testing.T) *Router {
	t.Helper()
	return New(slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestRouter_DedupesByMessageID(t *testing.T) {
	r := newTestRouter(t)
	ctx := context.Background()

	envs := []envelope.Envelope{
		{Channel: "telegram", MessageID: "1", Text: "hi"},
		{Channel: "telegram", MessageID: "1", Text: "hi"}, // dup
		{Channel: "telegram", MessageID: "2", Text: "bye"},
		{Channel: "slack", MessageID: "1", Text: "from-slack"}, // diff channel, same id → distinct
	}
	r.Inbound(ctx, envs)
	got := r.Recent(10)
	if len(got) != 3 {
		t.Errorf("want 3 unique recorded, got %d: %+v", len(got), got)
	}
}

func TestRouter_DedupExpires(t *testing.T) {
	r := newTestRouter(t)
	r.DedupTTL = 10 * time.Millisecond
	ctx := context.Background()
	r.Inbound(ctx, []envelope.Envelope{{Channel: "x", MessageID: "1"}})
	time.Sleep(20 * time.Millisecond)
	r.Inbound(ctx, []envelope.Envelope{{Channel: "x", MessageID: "1"}})
	if len(r.Recent(10)) != 2 {
		t.Errorf("post-expiry the same msg_id should be re-recorded")
	}
}

func TestRouter_NoDedupWhenMessageIDEmpty(t *testing.T) {
	r := newTestRouter(t)
	ctx := context.Background()
	r.Inbound(ctx, []envelope.Envelope{
		{Channel: "x", Text: "first"},
		{Channel: "x", Text: "second"},
	})
	if len(r.Recent(10)) != 2 {
		t.Errorf("envelopes without message_id must not be deduped")
	}
}
