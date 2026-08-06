// S11-2 Worker tests —— httptest mock brain endpoints + 真 biumindkit
// （fake Anthropic SSE upstream）跑端到端单 work。

package agentplane

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/pkg/biumindkit"
	"github.com/google/uuid"
)

// fakeBrainWorker 是带 work poll / ack / publish 端点的 mock brain。
// 一次性把 workToServe 用 PollWork 投出去，记 ack / publish 计数。
type fakeBrainWorker struct {
	envID         string
	workToServe   *WorkItem
	workServed    atomic.Bool
	ackCount      atomic.Int32
	publishedRaw  atomic.Int32
	heartbeatHits atomic.Int32

	// Control plane (cancel reverse routing)。controlToServe 不为 nil 时,
	// 第一次 PollControl 返它然后切回 204。controlGate 让测试控制 control
	// 何时下发(避免 race:control loop 起得比 work loop 快,如果 control
	// 立刻下发,InterruptSession 时 agent 还没注册,打断会丢失)。
	controlToServe   *ControlItem
	controlServed    atomic.Bool
	controlAckCount  atomic.Int32
	controlPollCount atomic.Int32
	controlGate      <-chan struct{}
}

func (f *fakeBrainWorker) handler(t *testing.T) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/agent/environments", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"environment_id":"` + f.envID + `","worker_kind":"runtime","machine_name":"x","state":"online"}`))
	})
	mux.HandleFunc("POST /v1/agent/environments/{id}/heartbeat", func(w http.ResponseWriter, _ *http.Request) {
		f.heartbeatHits.Add(1)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	mux.HandleFunc("DELETE /v1/agent/environments/{id}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /v1/agent/work/{env_id}/poll", func(w http.ResponseWriter, _ *http.Request) {
		if f.workToServe != nil && f.workServed.CompareAndSwap(false, true) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(f.workToServe)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /v1/agent/work/{env_id}/ack/{token}", func(w http.ResponseWriter, _ *http.Request) {
		f.ackCount.Add(1)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	mux.HandleFunc("POST /v1/agent/work/{env_id}/nak/{token}", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	mux.HandleFunc("POST /v1/agent/sessions/{id}/publish", func(w http.ResponseWriter, _ *http.Request) {
		f.publishedRaw.Add(1)
		w.WriteHeader(http.StatusAccepted)
	})
	mux.HandleFunc("POST /v1/agent/control/{env_id}/poll", func(w http.ResponseWriter, _ *http.Request) {
		f.controlPollCount.Add(1)
		// 测试可注入 gate 强制 handler 等到 ready 才下发 control。没设
		// gate 时立刻下发首条(production-equivalent)。
		if f.controlGate != nil {
			select {
			case <-f.controlGate:
			default:
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		if f.controlToServe != nil && f.controlServed.CompareAndSwap(false, true) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(f.controlToServe)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /v1/agent/control/{env_id}/ack/{token}", func(w http.ResponseWriter, _ *http.Request) {
		f.controlAckCount.Add(1)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	return mux
}

// fakeAnthropicUpstream 跟 brain chat_runner_test 同款 fake Anthropic SSE
// upstream —— 一段 "ok" 文本 + end_turn 收尾。
func fakeAnthropicUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		_, _ = w.Write([]byte(`event: message_start
data: {"type":"message_start","message":{"id":"m_1","model":"test"}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"}}

event: message_stop
data: {"type":"message_stop"}

`))
	}))
}

func newDiscardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestWorker_HandleWorkPublishesAndAcks(t *testing.T) {
	envID := uuid.NewString()
	sessionID := uuid.New()
	work := &WorkItem{
		AckToken: "tok-1",
		Body: mustJSON(WorkPayload{
			SessionID: sessionID,
			UserID:    uuid.New(),
			Mode:      "task",
			Prompt:    "hi",
		}),
	}
	be := &fakeBrainWorker{envID: envID, workToServe: work}
	ts := httptest.NewServer(be.handler(t))
	defer ts.Close()

	upstream := fakeAnthropicUpstream(t)
	defer upstream.Close()

	reg, err := NewRegistrar(context.Background(), Config{
		BrainURL:        ts.URL,
		Token:           "tok",
		HeartbeatPeriod: 1 * time.Second, // 测试不 fire
	}, newDiscardLogger())
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Stop(context.Background())

	builder := func(_ context.Context, _ WorkPayload) (*biumindkit.Agent, error) {
		return biumindkit.New(biumindkit.Options{
			APIKey:              "sk-fake",
			AnthropicEndpoint:   upstream.URL,
			LoadProjectMemory:   biumindkit.NoMemory,
			LoadProjectSettings: biumindkit.NoSettings,
			BypassPermissions:   true,
		})
	}
	w := NewWorker(reg, builder, WorkerConfig{
		PollWait: 200 * time.Millisecond,
	}, newDiscardLogger())

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go func() {
		_ = w.Run(ctx)
	}()

	// 等到 work 被 ack
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if be.ackCount.Load() >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if be.ackCount.Load() < 1 {
		t.Fatalf("ack count=%d, want ≥1 (work didn't complete)", be.ackCount.Load())
	}
	// 至少 publish 1 次（streamlined_text + result_success 至少 2 帧）
	if be.publishedRaw.Load() < 1 {
		t.Errorf("publish count=%d, want ≥1", be.publishedRaw.Load())
	}
	cancel()
}

func TestWorker_NilRegistrarFails(t *testing.T) {
	w := NewWorker(nil, nil, WorkerConfig{}, newDiscardLogger())
	err := w.Run(context.Background())
	if err == nil {
		t.Fatal("expected nil-Registrar error")
	}
}

func TestWorker_NilBuilderFails(t *testing.T) {
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

	w := NewWorker(reg, nil, WorkerConfig{}, newDiscardLogger())
	err = w.Run(context.Background())
	if err == nil {
		t.Fatal("expected nil-AgentBuilder error")
	}
}

func TestNextBackoff(t *testing.T) {
	cases := []struct {
		cur, max, want time.Duration
	}{
		{1 * time.Second, 30 * time.Second, 2 * time.Second},
		{16 * time.Second, 30 * time.Second, 30 * time.Second},
		{30 * time.Second, 30 * time.Second, 30 * time.Second},
	}
	for _, c := range cases {
		if got := nextBackoff(c.cur, c.max); got != c.want {
			t.Errorf("nextBackoff(%v, %v) = %v, want %v",
				c.cur, c.max, got, c.want)
		}
	}
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
