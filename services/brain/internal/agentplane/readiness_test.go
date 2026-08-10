// Readiness reconciler 单测 —— fake bus 模拟 disconnected → connected →
// 建流成功 / 失败重试 / 断线回退 的状态转换；readyz handler 各状态码 +
// payload。fakeJS 复用 queue_test.go 的内存实现（同包）。

package agentplane

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/biumind/biumind/packages/go-sdk/biu/bus"
	"github.com/google/uuid"
)

// fakeBus 是 bus.Bus 的可控内存实现：connected 可翻转，JetStream() 返回
// 预设句柄 / 错误。不依赖真 broker。
type fakeBus struct {
	mu        sync.Mutex
	connected bool
	js        bus.JetStream
	jsErr     error
}

func (f *fakeBus) Publish(context.Context, string, any, ...bus.Header) error { return nil }
func (f *fakeBus) Subscribe(string, bus.Handler) (bus.Subscription, error)   { return nil, nil }
func (f *fakeBus) Close() error                                              { return nil }

func (f *fakeBus) Connected() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.connected
}

func (f *fakeBus) JetStream() (bus.JetStream, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.js, f.jsErr
}

func (f *fakeBus) setConnected(b bool) {
	f.mu.Lock()
	f.connected = b
	f.mu.Unlock()
}

// flakyJS 包一层 fakeJS：前 failRemaining 次 EnsureStream 失败，模拟
// broker 已连但 JetStream 尚未就绪的窗口。
type flakyJS struct {
	fakeJS
	mu            sync.Mutex
	failRemaining int
	ensureCalls   int
}

func (f *flakyJS) EnsureStream(ctx context.Context, spec bus.StreamSpec) error {
	f.mu.Lock()
	f.ensureCalls++
	fail := f.failRemaining > 0
	if fail {
		f.failRemaining--
	}
	f.mu.Unlock()
	if fail {
		return errors.New("flakyJS: jetstream not ready yet")
	}
	return f.fakeJS.EnsureStream(ctx, spec)
}

func (f *flakyJS) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ensureCalls
}

var readinessDiscardLogger = slog.New(slog.NewTextHandler(io.Discard, nil))

// runReadiness 以测试节奏（5ms tick）后台跑 reconciler，t.Cleanup 停。
func runReadiness(t *testing.T, r *Readiness) {
	t.Helper()
	r.Tick = 5 * time.Millisecond
	r.MaxBackoff = 20 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	go r.Run(ctx)
	t.Cleanup(cancel)
}

func waitState(t *testing.T, r *Readiness, want ReadinessState) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if r.State() == want {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("state=%q, want %q (timeout)", r.State(), want)
}

// disconnected → connected → streams_ready；断线回退；重连后再就绪 —— 全
// 程不重启 reconciler（对应「不重启进程自愈」）。
func TestReadiness_ReconcileFlow(t *testing.T) {
	fj := &fakeJS{}
	fb := &fakeBus{js: fj} // connected=false 起步
	q := NewQueue(nil)
	r := NewReadiness(fb, true, q, readinessDiscardLogger)

	if r.State() != ReadinessDisconnected {
		t.Fatalf("initial state=%q want disconnected", r.State())
	}
	if r.Ready() {
		t.Fatal("Ready()=true before connect")
	}
	if r.JetStream() != nil || r.Queue() != nil {
		t.Fatal("handles should be nil before ready")
	}
	runReadiness(t, r)

	// 未连接时保持 disconnected
	time.Sleep(30 * time.Millisecond)
	if r.State() != ReadinessDisconnected {
		t.Fatalf("state=%q want disconnected while broker down", r.State())
	}

	// 连上 → 最终 streams_ready
	fb.setConnected(true)
	waitState(t, r, ReadinessStreamsReady)
	if !r.Ready() {
		t.Fatal("Ready()=false after streams_ready")
	}
	if r.JetStream() == nil || r.Queue() != q {
		t.Fatal("handles should be live after streams_ready")
	}
	// 三条流都 ensured（work / control / session）
	if len(fj.streams) != 3 {
		t.Fatalf("ensured streams=%d want 3", len(fj.streams))
	}
	// queue 已灌入 JS —— EnqueueWork 真走 fakeJS
	if err := q.EnqueueWork(context.Background(), uuid.New(), "w1", map[string]any{"x": 1}); err != nil {
		t.Fatalf("enqueue after ready: %v", err)
	}

	// 断线 → 回退 disconnected，句柄收回
	fb.setConnected(false)
	waitState(t, r, ReadinessDisconnected)
	if r.JetStream() != nil || r.Queue() != nil {
		t.Fatal("handles should be nil after disconnect")
	}
	if r.Ready() {
		t.Fatal("Ready()=true after disconnect")
	}

	// 重连 → 重新 Ensure（幂等）→ 再次就绪，不重启 reconciler
	fb.setConnected(true)
	waitState(t, r, ReadinessStreamsReady)
	if !r.Ready() || r.Queue() == nil {
		t.Fatal("should self-heal after reconnect")
	}
}

// EnsureStream 失败 → 保持重试（带退避）直到成功。
func TestReadiness_EnsureFailureRetries(t *testing.T) {
	fj := &flakyJS{failRemaining: 2}
	fb := &fakeBus{connected: true, js: fj}
	q := NewQueue(nil)
	r := NewReadiness(fb, true, q, readinessDiscardLogger)
	runReadiness(t, r)

	waitState(t, r, ReadinessStreamsReady)
	// work 流 ensure 失败 2 次 + 成功 1 次，control / session 各 1 次
	if got := fj.calls(); got != 5 {
		t.Fatalf("ensure calls=%d want 5 (2 fails + 3 ok)", got)
	}
	if fj.failRemaining != 0 {
		t.Fatalf("failRemaining=%d want 0 (retries consumed)", fj.failRemaining)
	}
}

// bus.JetStream() 本身失败也走重试路径。
func TestReadiness_JetStreamInitFailureRetries(t *testing.T) {
	fj := &fakeJS{}
	fb := &fakeBus{connected: true, jsErr: errors.New("no js")}
	q := NewQueue(nil)
	r := NewReadiness(fb, true, q, readinessDiscardLogger)
	runReadiness(t, r)

	time.Sleep(30 * time.Millisecond)
	if r.State() == ReadinessStreamsReady {
		t.Fatal("should not be ready while JetStream() errors")
	}
	// 修复 → 就绪
	fb.mu.Lock()
	fb.jsErr = nil
	fb.js = fj
	fb.mu.Unlock()
	waitState(t, r, ReadinessStreamsReady)
}

// NATS_URL 为空 → disabled 终态：Ready 恒 true（/readyz 不算失败），句柄
// 恒 nil，Run 立即返回。
func TestReadiness_Disabled(t *testing.T) {
	q := NewQueue(nil)
	r := NewReadiness(bus.NewNoopBus(), false, q, readinessDiscardLogger)

	if r.State() != ReadinessDisabled {
		t.Fatalf("state=%q want disabled", r.State())
	}
	if !r.Ready() {
		t.Fatal("disabled should count as ready")
	}
	if r.JetStream() != nil || r.Queue() != nil {
		t.Fatal("handles should be nil when disabled")
	}
	snap := r.Snapshot()
	if snap.NATS != "disabled" || snap.Streams != "disabled" || snap.Queue || snap.Ingress {
		t.Fatalf("disabled snapshot=%+v", snap)
	}

	// Run 立即返回（不阻塞 shutdown）
	done := make(chan struct{})
	go func() { r.Run(context.Background()); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run should return immediately when disabled")
	}
}

// /readyz：各状态下状态码与 payload。
func TestReadiness_ReadyzHandler(t *testing.T) {
	q := NewQueue(nil)
	mk := func(state ReadinessState) *Readiness {
		r := NewReadiness(&fakeBus{}, true, q, readinessDiscardLogger)
		r.state = state // 同包直设,免去跑 reconciler
		return r
	}
	cases := []struct {
		name        string
		readiness   *Readiness
		wantStatus  int
		wantNATS    string
		wantStreams string
		wantQueue   bool
		wantIngress bool
	}{
		{"disabled", NewReadiness(bus.NewNoopBus(), false, q, readinessDiscardLogger),
			http.StatusOK, "disabled", "disabled", false, false},
		{"disconnected", mk(ReadinessDisconnected),
			http.StatusServiceUnavailable, "disconnected", "pending", false, false},
		{"connected_pending", mk(ReadinessConnected),
			http.StatusServiceUnavailable, "connected", "pending", false, false},
		{"streams_ready", mk(ReadinessStreamsReady),
			http.StatusOK, "connected", "ready", true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
			c.readiness.HandleReadyz(rec, req)
			if rec.Code != c.wantStatus {
				t.Fatalf("status=%d want %d", rec.Code, c.wantStatus)
			}
			var snap ReadinessSnapshot
			if err := json.Unmarshal(rec.Body.Bytes(), &snap); err != nil {
				t.Fatalf("payload not JSON: %v", err)
			}
			if snap.NATS != c.wantNATS || snap.Streams != c.wantStreams ||
				snap.Queue != c.wantQueue || snap.Ingress != c.wantIngress {
				t.Fatalf("snapshot=%+v want nats=%q streams=%q queue=%v ingress=%v",
					snap, c.wantNATS, c.wantStreams, c.wantQueue, c.wantIngress)
			}
		})
	}
}
