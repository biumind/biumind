// F5 worker integration tests — verify Worker.InterruptSession() can
// reach the in-flight Agent and trigger a clean stop.

package agentplane

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/pkg/biumindkit"
	"github.com/google/uuid"
)

// holdingAgentBuilder spins up a *real* biumindkit.Agent pointed at a
// hanging upstream (writes message_start then blocks on its req ctx).
// The handler returns a started signal so callers can assert "the
// upstream actually saw cancel."
func holdingAgentBuilder(t *testing.T) (AgentBuilder, <-chan struct{}, *httptest.Server) {
	t.Helper()
	canceled := make(chan struct{})
	var once sync.Once
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`event: message_start
data: {"type":"message_start","message":{"id":"m_1","model":"test"}}

`))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
		once.Do(func() { close(canceled) })
	}))
	build := func(_ context.Context, _ WorkPayload, _ biumindkit.PermissionPolicyFn) (*biumindkit.Agent, error) {
		return biumindkit.New(biumindkit.Options{
			APIKey:              "sk-fake",
			AnthropicEndpoint:   upstream.URL,
			LoadProjectMemory:   biumindkit.NoMemory,
			LoadProjectSettings: biumindkit.NoSettings,
			BypassPermissions:   true,
		})
	}
	return build, canceled, upstream
}

// TestWorker_InterruptSession_FoundAndUnknown verifies the lookup path:
// in-flight session resolves to its agent, missing session is a clean
// false return (no panic). End-to-end check: after firing
// InterruptSession the upstream HTTP request observes ctx cancel,
// proving the cancel propagated SDK → engine → adapter → http transport.
func TestWorker_InterruptSession_FoundAndUnknown(t *testing.T) {
	envID := uuid.NewString()
	sessionID := uuid.New()

	// We need the worker to (1) successfully register, (2) get back a
	// work item with our sessionID, and (3) park inside Submit so we
	// can call InterruptSession from the test goroutine.
	work := &WorkItem{
		AckToken: "tok-x",
		Body: mustJSON(WorkPayload{
			SessionID: sessionID,
			UserID:    uuid.New(),
			Mode:      "agent",
			Prompt:    "long",
		}),
	}
	be := &fakeBackend{envID: envID, workToServe: work}
	ts := httptest.NewServer(be.handler(t))
	defer ts.Close()

	c := NewClient(ts.URL, "tok", nil)
	build, upstreamCanceled, upstream := holdingAgentBuilder(t)
	defer upstream.Close()

	w := NewWorker(c, WorkerConfig{
		EnvironmentName: "test",
		HeartbeatPeriod: 5 * time.Second,
		PollWait:        100 * time.Millisecond,
	}, build, slog.New(slog.NewTextHandler(io.Discard, nil)))

	// Unknown session before Run starts: must be false, no panic.
	if w.InterruptSession(uuid.New()) {
		t.Errorf("InterruptSession on empty registry should return false")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	runDone := make(chan struct{})
	go func() {
		_ = w.Run(ctx)
		close(runDone)
	}()

	// Wait for the agent to land in the registry — ~50ms cycle since
	// poll, register, build can race.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		w.agentsMu.Lock()
		_, present := w.agents[sessionID]
		w.agentsMu.Unlock()
		if present {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	w.agentsMu.Lock()
	_, present := w.agents[sessionID]
	w.agentsMu.Unlock()
	if !present {
		t.Fatal("agent never registered for sessionID")
	}

	// Unknown session AFTER one is registered: still false.
	if w.InterruptSession(uuid.New()) {
		t.Errorf("InterruptSession on bogus id should return false")
	}

	// Real interrupt: must return true and propagate to upstream req.
	if !w.InterruptSession(sessionID) {
		t.Errorf("InterruptSession for live session should return true")
	}
	select {
	case <-upstreamCanceled:
	case <-time.After(2 * time.Second):
		t.Errorf("upstream did not observe cancel after InterruptSession")
	}

	// Worker should ack the work and continue polling. We don't need to
	// shut it down explicitly — ctx timeout will. But ensure handleWork
	// returned and the agent was untracked.
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		w.agentsMu.Lock()
		_, stillTracked := w.agents[sessionID]
		w.agentsMu.Unlock()
		if !stillTracked {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	w.agentsMu.Lock()
	_, stillTracked := w.agents[sessionID]
	w.agentsMu.Unlock()
	if stillTracked {
		t.Errorf("agent still tracked after handleWork returned")
	}

	// Re-Interrupt after untrack: clean false, not panic.
	if w.InterruptSession(sessionID) {
		t.Errorf("post-untrack InterruptSession should be false")
	}

	cancel()
	<-runDone
}

// TestWorker_ObserveCancelLatency_OnlyAfterInterrupt — observeCancelLatency
// 是 Done{interrupted} 事件的 hook,只在用户主动 cancel 过的 session
// 上 observe。session 自然结束(stop_reason=end_turn)走过 observe 时
// 是 noop(没 timestamp 可算)。这把 cancelStartedAt 表跟 metrics 路径
// 解耦成「事件触发 observe」而不是「Done 事件无条件 observe」。
func TestWorker_ObserveCancelLatency_OnlyAfterInterrupt(t *testing.T) {
	build, _, upstream := holdingAgentBuilder(t)
	defer upstream.Close()

	w := NewWorker(nil, WorkerConfig{}, build,
		slog.New(slog.NewTextHandler(io.Discard, nil)))

	// Case A: 没注册 agent → observeCancelLatency 是 no-op,不 panic
	w.observeCancelLatency(uuid.New())

	// Case B: 注册 + 没 InterruptSession → cancelStartedAt 是空,
	// observeCancelLatency 也 no-op
	sidB := uuid.New()
	a, err := build(context.Background(), WorkPayload{SessionID: sidB}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	w.trackAgent(sidB, a)
	w.observeCancelLatency(sidB) // no-op
	w.agentsMu.Lock()
	if _, present := w.cancelStartedAt[sidB]; present {
		t.Errorf("cancelStartedAt should not be populated without InterruptSession")
	}
	w.agentsMu.Unlock()
	w.untrackAgent(sidB)

	// Case C: 注册 + InterruptSession + observeCancelLatency → 时间戳
	// 被消费(防止重复 observe),Interrupt 真触发了 agent ctx
	sidC := uuid.New()
	a2, err := build(context.Background(), WorkPayload{SessionID: sidC}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer a2.Close()
	w.trackAgent(sidC, a2)
	if !w.InterruptSession(sidC) {
		t.Fatal("InterruptSession should hit registered agent")
	}
	w.agentsMu.Lock()
	_, hadTS := w.cancelStartedAt[sidC]
	w.agentsMu.Unlock()
	if !hadTS {
		t.Fatal("InterruptSession should populate cancelStartedAt")
	}
	w.observeCancelLatency(sidC)
	// 二次 observe 是 no-op (timestamp 已被消费)
	w.observeCancelLatency(sidC)
	w.agentsMu.Lock()
	_, stillThere := w.cancelStartedAt[sidC]
	w.agentsMu.Unlock()
	if stillThere {
		t.Errorf("cancelStartedAt should be cleared after observe")
	}
	w.untrackAgent(sidC)
}

// TestWorker_ControlLoop_RoutesCancel — end-to-end at the worker layer:
// when the brain control endpoint serves a `cancel_session` payload,
// the daemon's control loop should
//   - poll the endpoint
//   - parse the body
//   - call InterruptSession on the in-flight agent
//   - ack the message (so broker doesn't redeliver)
//
// The agent itself uses the holding upstream so we can observe
// "upstream got ctx cancel" as proof the interrupt landed.
func TestWorker_ControlLoop_RoutesCancel(t *testing.T) {
	envID := uuid.NewString()
	sessionID := uuid.New()

	work := &WorkItem{
		AckToken: "tok-w",
		Body: mustJSON(WorkPayload{
			SessionID: sessionID,
			UserID:    uuid.New(),
			Mode:      "agent",
			Prompt:    "long",
		}),
	}
	cancelPayload, _ := json.Marshal(map[string]any{
		"type":       "cancel_session",
		"session_id": sessionID.String(),
		"request_id": "req-test-1",
	})
	control := &ControlItem{
		AckToken: "tok-c",
		Body:     cancelPayload,
	}

	// gate 控制 control message 何时下发 —— 避免 race:control loop 起得
	// 比 work loop 快,如果 control 立刻就下发,InterruptSession 时 agent
	// 还没注册,打断会丢失。close(gate) 之前 control endpoint 都返 204。
	gate := make(chan struct{})
	be := &fakeBackend{
		envID:          envID,
		workToServe:    work,
		controlToServe: control,
		controlGate:    gate,
	}
	ts := httptest.NewServer(be.handler(t))
	defer ts.Close()

	c := NewClient(ts.URL, "tok", nil)
	build, upstreamCanceled, upstream := holdingAgentBuilder(t)
	defer upstream.Close()

	w := NewWorker(c, WorkerConfig{
		EnvironmentName: "test",
		HeartbeatPeriod: 5 * time.Second,
		PollWait:        200 * time.Millisecond, // 短一点让 control loop 多转
	}, build, slog.New(slog.NewTextHandler(io.Discard, nil)))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	runDone := make(chan struct{})
	go func() {
		_ = w.Run(ctx)
		close(runDone)
	}()

	// Step 1: 等 agent 注册到 in-flight 表。
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		w.agentsMu.Lock()
		_, present := w.agents[sessionID]
		w.agentsMu.Unlock()
		if present {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	w.agentsMu.Lock()
	_, present := w.agents[sessionID]
	w.agentsMu.Unlock()
	if !present {
		t.Fatal("agent never registered")
	}

	// Step 2: 放 gate,让 control 下发,触发 InterruptSession。
	close(gate)

	// Step 3: 等 control 被 ack(说明已经处理)。
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if be.controlAckCount.Load() >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if be.controlAckCount.Load() < 1 {
		t.Errorf("control message never acked; pollCount=%d",
			be.controlPollCount.Load())
	}
	// 上游应当观察到 ctx cancel —— 真正意义上的「打断生效了」。
	select {
	case <-upstreamCanceled:
	case <-time.After(2 * time.Second):
		t.Errorf("upstream did not observe cancel after control message")
	}

	cancel()
	<-runDone
}
