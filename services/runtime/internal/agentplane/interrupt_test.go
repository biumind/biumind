// Cancel routing tests for runtime worker — 跟 biu daemon 同款契约,
// 验证 InterruptSession / observeCancelLatency / controlLoop end-to-end。

package agentplane

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/pkg/biumindkit"
	"github.com/google/uuid"
)

// holdingAgentBuilder 给 worker 一个 *real* biumindkit.Agent,但上游
// (Anthropic 假模拟)hang 在 message_start 之后等 ctx 取消。这样我们能
// 观察到「上游收到 ctx cancel」作为 Interrupt 真打到 engine 的证据。
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
	build := func(_ context.Context, _ WorkPayload) (*biumindkit.Agent, error) {
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

// TestWorker_InterruptSession_RegistryRoundtrip — runtime 内进程 API:
// 注册 → 命中 → cancelStartedAt 打了 timestamp → untrack 清干净。
func TestWorker_InterruptSession_RegistryRoundtrip(t *testing.T) {
	envID := uuid.NewString()
	be := &fakeBrainWorker{envID: envID}
	ts := httptest.NewServer(be.handler(t))
	defer ts.Close()

	reg, err := NewRegistrar(context.Background(), Config{
		BrainURL: ts.URL, Token: "tok",
	}, newDiscardLogger())
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Stop(context.Background())

	w := NewWorker(reg, func(_ context.Context, _ WorkPayload) (*biumindkit.Agent, error) {
		return nil, nil
	}, WorkerConfig{}, newDiscardLogger())

	// Case A: 没 agent 注册 → false / 不 panic
	if w.InterruptSession(uuid.New()) {
		t.Errorf("Interrupt on empty registry should return false")
	}

	// Case B: 注册一个真 agent,Interrupt 命中
	build, _, upstream := holdingAgentBuilder(t)
	defer upstream.Close()
	a, err := build(context.Background(), WorkPayload{})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	sid := uuid.New()
	w.trackAgent(sid, a)
	if !w.InterruptSession(sid) {
		t.Errorf("Interrupt on registered agent should return true")
	}
	w.agentsMu.Lock()
	_, hadTS := w.cancelStartedAt[sid]
	w.agentsMu.Unlock()
	if !hadTS {
		t.Error("InterruptSession should populate cancelStartedAt")
	}

	// Case C: untrack 清掉 cancelStartedAt 防止泄漏(被 cancel 但 Done 没 observe 到)
	w.untrackAgent(sid)
	w.agentsMu.Lock()
	_, stillThere := w.cancelStartedAt[sid]
	w.agentsMu.Unlock()
	if stillThere {
		t.Error("untrackAgent should clear cancelStartedAt")
	}

	// Case D: untrack 之后 Interrupt 又是 false
	if w.InterruptSession(sid) {
		t.Errorf("Interrupt after untrack should be false")
	}
}

// TestWorker_ObserveCancelLatency_OnlyAfterInterrupt — Done{interrupted}
// 事件触发 observe 时,只在 InterruptSession 命中过的 session 上记延迟。
// 自然结束(stop_reason=end_turn)走 observe 时是 noop。
func TestWorker_ObserveCancelLatency_OnlyAfterInterrupt(t *testing.T) {
	envID := uuid.NewString()
	be := &fakeBrainWorker{envID: envID}
	ts := httptest.NewServer(be.handler(t))
	defer ts.Close()

	reg, err := NewRegistrar(context.Background(), Config{
		BrainURL: ts.URL, Token: "tok",
	}, newDiscardLogger())
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Stop(context.Background())

	build, _, upstream := holdingAgentBuilder(t)
	defer upstream.Close()
	w := NewWorker(reg, build, WorkerConfig{}, newDiscardLogger())

	// Case A: 没注册 agent → no-op
	w.observeCancelLatency(uuid.New())

	// Case B: 注册但没 Interrupt → cancelStartedAt 不被填,observe noop
	sidB := uuid.New()
	a, err := build(context.Background(), WorkPayload{SessionID: sidB})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	w.trackAgent(sidB, a)
	w.observeCancelLatency(sidB) // no-op
	w.untrackAgent(sidB)

	// Case C: 注册 + Interrupt + observe → 时间戳被消费防重复 observe
	sidC := uuid.New()
	a2, err := build(context.Background(), WorkPayload{SessionID: sidC})
	if err != nil {
		t.Fatal(err)
	}
	defer a2.Close()
	w.trackAgent(sidC, a2)
	if !w.InterruptSession(sidC) {
		t.Fatal("Interrupt should hit")
	}
	w.observeCancelLatency(sidC)
	w.observeCancelLatency(sidC) // 二次 noop
	w.agentsMu.Lock()
	_, stillThere := w.cancelStartedAt[sidC]
	w.agentsMu.Unlock()
	if stillThere {
		t.Error("cancelStartedAt should be cleared after observe")
	}
	w.untrackAgent(sidC)
}

// TestWorker_ControlLoop_RoutesCancel — end-to-end:fakeBrain 端口投
// cancel_session,worker 的 controlLoop 拉到、解析、调 InterruptSession。
// 上游 holdingAgentBuilder 的 HTTP 请求观察到 ctx cancel,确认整条路通。
func TestWorker_ControlLoop_RoutesCancel(t *testing.T) {
	envID := uuid.NewString()
	sessionID := uuid.New()

	work := &WorkItem{
		AckToken: "tok-w",
		Body: mustJSON(WorkPayload{
			SessionID: sessionID,
			UserID:    uuid.New(),
			Mode:      "task",
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

	// gate 控制 control 何时下发 — 避免 race:control loop 比 work loop
	// 起得快,如果立刻下发,InterruptSession 时 agent 还没注册,打断丢失。
	gate := make(chan struct{})
	be := &fakeBrainWorker{
		envID:          envID,
		workToServe:    work,
		controlToServe: control,
		controlGate:    gate,
	}
	ts := httptest.NewServer(be.handler(t))
	defer ts.Close()

	reg, err := NewRegistrar(context.Background(), Config{
		BrainURL:        ts.URL,
		Token:           "tok",
		HeartbeatPeriod: 5 * time.Second,
	}, newDiscardLogger())
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Stop(context.Background())

	build, upstreamCanceled, upstream := holdingAgentBuilder(t)
	defer upstream.Close()

	w := NewWorker(reg, build, WorkerConfig{
		PollWait: 200 * time.Millisecond,
	}, newDiscardLogger())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	runDone := make(chan struct{})
	go func() {
		_ = w.Run(ctx)
		close(runDone)
	}()

	// Step 1: 等 agent 注册到 in-flight 表
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

	// Step 2: 放 gate,让 control 下发触发 InterruptSession
	close(gate)

	// Step 3: 等 control 被 ack(说明已经处理)
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

	// 上游应该观察到 ctx cancel — 真正的 「打断生效了」
	select {
	case <-upstreamCanceled:
	case <-time.After(2 * time.Second):
		t.Errorf("upstream did not observe cancel after control message")
	}

	cancel()
	<-runDone
}

// 防止 import 报 unused 的 dummy 引用。
var _ = io.Discard
