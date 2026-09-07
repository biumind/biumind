package agentplane

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/pkg/biumindkit"
	agentpkg "github.com/biumind/biumind/packages/go-sdk/biu/agent"
	"github.com/google/uuid"
)

// stubRunner 记录 Run 是否被调用，用于断言"越界 work 不应触达外部 backend"。
type stubRunner struct{ ran atomic.Bool }

func (s *stubRunner) Name() string { return "stub" }
func (s *stubRunner) Run(context.Context, agentpkg.Request) (<-chan agentpkg.Event, error) {
	s.ran.Store(true)
	ch := make(chan agentpkg.Event)
	close(ch)
	return ch, nil
}

// R6.3 路径地板：外部 CLI backend 路径必须和 biumindkit 路径一样拒越界
// Workdir。否则手机/web 端可投 backend=claude-cli + workdir=~/.ssh 让 daemon
// 在越界目录起 CLI，架空 --allowed-roots。回归锁。
func TestWorker_ExternalBackend_RejectsOutOfBoundsWorkdir(t *testing.T) {
	envID := uuid.NewString()
	work := &WorkItem{
		AckToken: "tok-x",
		Body: mustJSON(WorkPayload{
			SessionID: uuid.New(), UserID: uuid.New(),
			Mode: "agent", Backend: "claude-cli",
			Workdir: "/etc/forbidden", Prompt: "hi",
		}),
	}
	be := &fakeBackend{envID: envID, workToServe: work}
	ts := httptest.NewServer(be.handler(t))
	defer ts.Close()
	c := NewClient(ts.URL, "tok", nil)

	runner := &stubRunner{}
	build, upstream := newStubAgentBuilder(t)
	defer upstream.Close()

	w := NewWorker(c, WorkerConfig{
		EnvironmentName: "test",
		HeartbeatPeriod: time.Second,
		PollWait:        100 * time.Millisecond,
		RunnerBuilder:   func(WorkPayload) agentpkg.Runner { return runner },
		ResolveWorkdir: func(wd string) (string, error) {
			if wd == "" || wd == "/tmp/allowed" {
				return "/tmp/allowed", nil
			}
			return "", errors.New("work.Workdir outside allowed roots")
		},
	}, build, slog.New(slog.NewTextHandler(io.Discard, nil)))

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go func() { _ = w.Run(ctx) }()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if be.nakCount.Load() >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if be.nakCount.Load() < 1 {
		t.Fatalf("nak count=%d, want ≥1 (越界 workdir 必须被拒)", be.nakCount.Load())
	}
	if runner.ran.Load() {
		t.Error("runner.Run 被调用了——外部 backend 路径地板被绕过")
	}
	if be.ackCount.Load() != 0 {
		t.Errorf("ack count=%d, want 0 (被拒的 work 不应 ack)", be.ackCount.Load())
	}
}

// fakeBackend 是个简化的 brain stand-in。Register 给固定 env_id；PollWork
// 第一次返一条 work 然后永远 204；Ack 计数；PublishFrame 收集帧。
type fakeBackend struct {
	envID         string
	workToServe   *WorkItem // 第一次 PollWork 返这个；之后返 nil
	workServed    atomic.Bool
	ackCount      atomic.Int32
	nakCount      atomic.Int32
	publishedRaw  atomic.Int32
	heartbeatHit  atomic.Int32
	deregisterHit atomic.Int32

	// Control plane (cancel / 反向打断)。controlToServe 不为 nil 时,
	// 第一次 PollControl 返它然后切回 204。controlAck/controlPoll 计数让
	// 测试看到生命周期。controlGate 不 nil 时,handler 在 close 之前都返
	// 204 (让测试控制 control 何时被消费,避免 race)。
	controlToServe   *ControlItem
	controlServed    atomic.Bool
	controlAckCount  atomic.Int32
	controlPollCount atomic.Int32
	controlGate      <-chan struct{}
}

func (f *fakeBackend) handler(t *testing.T) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/agent/environments", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"environment_id":"` + f.envID + `","worker_kind":"biu_daemon","machine_name":"x","state":"online"}`))
	})
	mux.HandleFunc("POST /v1/agent/environments/{id}/heartbeat", func(w http.ResponseWriter, _ *http.Request) {
		f.heartbeatHit.Add(1)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	mux.HandleFunc("DELETE /v1/agent/environments/{id}", func(w http.ResponseWriter, _ *http.Request) {
		f.deregisterHit.Add(1)
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
		f.nakCount.Add(1)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	mux.HandleFunc("POST /v1/agent/sessions/{id}/publish", func(w http.ResponseWriter, _ *http.Request) {
		f.publishedRaw.Add(1)
		w.WriteHeader(http.StatusAccepted)
	})
	mux.HandleFunc("POST /v1/agent/control/{env_id}/poll", func(w http.ResponseWriter, _ *http.Request) {
		f.controlPollCount.Add(1)
		// 测试可注入 gate 强制 handler 等到测试 ready 才下发 control。
		// 没设 gate(production-equivalent)就立刻下发首条。
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

// stubAgent 实现 biumindkit.Agent 不可能（unexported field）—— 改用构造一个
// **真** biumindkit.Agent 但给假 Anthropic upstream，让 Submit 返回一条
// AssistantText + Done。原有 bridge / sdkbridge 测试已经走过这个模式。
func newStubAgentBuilder(t *testing.T) (AgentBuilder, *httptest.Server) {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		_, _ = w.Write([]byte(`event: message_start
data: {"type":"message_start","message":{"id":"m_1","model":"test"}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"}}

event: message_stop
data: {"type":"message_stop"}

`))
	}))
	build := func(_ context.Context, _ WorkPayload, _ biumindkit.PermissionPolicyFn, _ biumindkit.AskUserFn) (*biumindkit.Agent, error) {
		return biumindkit.New(biumindkit.Options{
			APIKey:              "sk-fake",
			AnthropicEndpoint:   upstream.URL,
			LoadProjectMemory:   biumindkit.NoMemory,
			LoadProjectSettings: biumindkit.NoSettings,
			BypassPermissions:   true,
		})
	}
	return build, upstream
}

func TestWorker_Register(t *testing.T) {
	envID := uuid.NewString()
	be := &fakeBackend{envID: envID}
	ts := httptest.NewServer(be.handler(t))
	defer ts.Close()
	c := NewClient(ts.URL, "tok", nil)

	build, upstream := newStubAgentBuilder(t)
	defer upstream.Close()

	w := NewWorker(c, WorkerConfig{
		EnvironmentName: "test-machine",
		HeartbeatPeriod: 100 * time.Millisecond,
		PollWait:        200 * time.Millisecond,
	}, build, slog.New(slog.NewTextHandler(io.Discard, nil)))

	ctx, cancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
	defer cancel()
	if err := w.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// 注册成功 → envID 设上
	w.mu.Lock()
	gotEnvID := w.envID.String()
	registered := w.registered
	w.mu.Unlock()
	if !registered || gotEnvID != envID {
		t.Errorf("envID=%q registered=%v", gotEnvID, registered)
	}
	// 至少跑过几次 heartbeat
	if be.heartbeatHit.Load() < 1 {
		t.Errorf("heartbeat count=%d, want ≥1", be.heartbeatHit.Load())
	}
	// 退出时 deregister
	if be.deregisterHit.Load() != 1 {
		t.Errorf("deregister count=%d, want 1", be.deregisterHit.Load())
	}
}

func TestWorker_HandleWork_AcksAndPublishes(t *testing.T) {
	envID := uuid.NewString()
	sessionID := uuid.New()

	work := &WorkItem{
		AckToken: "tok-1",
		Body: mustJSON(WorkPayload{
			SessionID: sessionID,
			UserID:    uuid.New(),
			Mode:      "agent",
			Prompt:    "hi",
		}),
	}
	be := &fakeBackend{envID: envID, workToServe: work}
	ts := httptest.NewServer(be.handler(t))
	defer ts.Close()
	c := NewClient(ts.URL, "tok", nil)

	build, upstream := newStubAgentBuilder(t)
	defer upstream.Close()

	w := NewWorker(c, WorkerConfig{
		EnvironmentName: "test",
		HeartbeatPeriod: 1 * time.Second, // 测试期间不 fire
		PollWait:        100 * time.Millisecond,
	}, build, slog.New(slog.NewTextHandler(io.Discard, nil)))

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go func() {
		_ = w.Run(ctx)
	}()

	// 等到 work 被 ack（说明跑完了）。最多等 2s
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
	// publish 应该至少 1 次（AssistantText → SDKStreamlinedText + Done →
	// SDKResultSuccess，至少 2 帧）
	if be.publishedRaw.Load() < 1 {
		t.Errorf("publish count=%d, want ≥1", be.publishedRaw.Load())
	}
	cancel()
}

func TestWorker_RegisterFailure_ExitsRun(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/agent/environments", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"invalid_token","message":"bad pat"}}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	build, upstream := newStubAgentBuilder(t)
	defer upstream.Close()

	c := NewClient(ts.URL, "wrong", nil)
	w := NewWorker(c, WorkerConfig{EnvironmentName: "x"}, build,
		slog.New(slog.NewTextHandler(io.Discard, nil)))

	err := w.Run(context.Background())
	if err == nil {
		t.Fatal("expected register error to surface")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusUnauthorized {
		t.Errorf("expected wrapped 401 APIError, got %v", err)
	}
}

func TestNextBackoff(t *testing.T) {
	cases := []struct {
		cur, max, want time.Duration
	}{
		{1 * time.Second, 30 * time.Second, 2 * time.Second},
		{16 * time.Second, 30 * time.Second, 30 * time.Second}, // cap 触发
		{30 * time.Second, 30 * time.Second, 30 * time.Second}, // 已到 max
	}
	for _, c := range cases {
		if got := nextBackoff(c.cur, c.max); got != c.want {
			t.Errorf("nextBackoff(%v, %v) = %v, want %v", c.cur, c.max, got, c.want)
		}
	}
}

// 基础设施 404（frp/nginx 在后端抖动时返的 HTML 错误页）绝不能当
// "environment 被删" —— 不 re-register、不退出，按 transient 退避继续 poll。
// 回归: frp 抖一次曾被误判成 env 删除 → re-register 再撞 404 → daemon 自杀。
func TestWorker_Infra404_NoReregisterNoExit(t *testing.T) {
	envID := uuid.NewString()
	var registerCount, pollCount atomic.Int32

	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/agent/environments", func(w http.ResponseWriter, _ *http.Request) {
		registerCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"environment_id":"` + envID + `","worker_kind":"biu_daemon","state":"online"}`))
	})
	mux.HandleFunc("POST /v1/agent/environments/{id}/heartbeat", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	mux.HandleFunc("DELETE /v1/agent/environments/{id}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	// frp 隧道错误页 —— 404 + HTML,无 brain JSON code。
	mux.HandleFunc("POST /v1/agent/work/{id}/poll", func(w http.ResponseWriter, _ *http.Request) {
		pollCount.Add(1)
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`<!DOCTYPE html><html><body><h1>The page you requested was not found.</h1><p>The server is powered by frp.</p></body></html>`))
	})
	mux.HandleFunc("POST /v1/agent/control/{id}/poll", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	build, upstream := newStubAgentBuilder(t)
	defer upstream.Close()

	c := NewClient(ts.URL, "tok", nil)
	w := NewWorker(c, WorkerConfig{
		EnvironmentName: "test",
		HeartbeatPeriod: time.Second,
		PollWait:        50 * time.Millisecond,
		BackoffInitial:  10 * time.Millisecond,
		BackoffMax:      50 * time.Millisecond,
	}, build, slog.New(slog.NewTextHandler(io.Discard, nil)))

	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
	defer cancel()
	if err := w.Run(ctx); err != nil {
		t.Fatalf("Run 应随 ctx 结束干净返回, got %v", err)
	}
	if got := registerCount.Load(); got != 1 {
		t.Errorf("register count=%d, want 1 (基础设施 404 不该触发 re-register)", got)
	}
	if got := pollCount.Load(); got < 2 {
		t.Errorf("poll count=%d, want ≥2 (应持续退避重试而非退出)", got)
	}
}

// brain 业务 404（env 真被删）触发 re-register；re-register 失败不再退出,
// 退避后持续重试直到 brain 恢复。回归: 此前 re-register 一次失败即 exit,
// 一次瞬时故障就把 daemon 打死到 client 重启。
func TestWorker_Brain404_ReregisterRetriedUntilRecovery(t *testing.T) {
	envID := uuid.NewString()
	var registerCount atomic.Int32

	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/agent/environments", func(w http.ResponseWriter, _ *http.Request) {
		n := registerCount.Add(1)
		// 初次注册之后的头两次 re-register 失败(brain 还在抖),第三次起恢复。
		if n > 1 && n < 4 {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`<html><body>502 Bad Gateway</body></html>`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"environment_id":"` + envID + `","worker_kind":"biu_daemon","state":"online"}`))
	})
	mux.HandleFunc("POST /v1/agent/environments/{id}/heartbeat", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	mux.HandleFunc("DELETE /v1/agent/environments/{id}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	// brain 业务 404 —— JSON 错误体带 code=not_found。
	mux.HandleFunc("POST /v1/agent/work/{id}/poll", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"agentplane: environment not found"}}`))
	})
	mux.HandleFunc("POST /v1/agent/control/{id}/poll", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	build, upstream := newStubAgentBuilder(t)
	defer upstream.Close()

	c := NewClient(ts.URL, "tok", nil)
	w := NewWorker(c, WorkerConfig{
		EnvironmentName: "test",
		HeartbeatPeriod: time.Second,
		PollWait:        50 * time.Millisecond,
		BackoffInitial:  10 * time.Millisecond,
		BackoffMax:      50 * time.Millisecond,
	}, build, slog.New(slog.NewTextHandler(io.Discard, nil)))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runDone := make(chan error, 1)
	go func() { runDone <- w.Run(ctx) }()

	// 等到第 4 次 register（1 初次 + 2 失败 + 1 恢复）—— 旧行为在第 2 次
	// 失败后就直接退出了,永远到不了 4。
	deadline := time.Now().Add(3 * time.Second)
	for registerCount.Load() < 4 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("Run 应干净返回, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancel 后 Run 未退出")
	}
	if got := registerCount.Load(); got < 4 {
		t.Fatalf("register count=%d, want ≥4 (re-register 失败应退避重试直到恢复)", got)
	}
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
