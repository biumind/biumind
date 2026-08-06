// S11-1 Registrar tests —— httptest mock brain endpoints；不需要真 NATS / DB。

package agentplane

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

type fakeBrain struct {
	envID         string
	registerCalls atomic.Int32
	heartbeatHits atomic.Int32
	deregisterHit atomic.Int32

	// 控制开关：让 heartbeat 第 N 次返 404 测试 re-register 路径
	heartbeat404OnNthCall int32
}

func (f *fakeBrain) handler(t *testing.T) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/agent/environments", func(w http.ResponseWriter, r *http.Request) {
		f.registerCalls.Add(1)
		if got := r.Header.Get("Authorization"); !strings.HasPrefix(got, "Bearer ") {
			t.Errorf("missing/wrong Bearer header: %q", got)
		}
		var body registerReq
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.WorkerKind != "runtime" {
			t.Errorf("worker_kind=%q want runtime", body.WorkerKind)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"environment_id":"` + f.envID + `","worker_kind":"runtime","machine_name":"x","state":"online"}`))
	})
	mux.HandleFunc("POST /v1/agent/environments/{id}/heartbeat", func(w http.ResponseWriter, _ *http.Request) {
		hit := f.heartbeatHits.Add(1)
		if f.heartbeat404OnNthCall > 0 && int32(hit) == f.heartbeat404OnNthCall {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	mux.HandleFunc("DELETE /v1/agent/environments/{id}", func(w http.ResponseWriter, _ *http.Request) {
		f.deregisterHit.Add(1)
		w.WriteHeader(http.StatusNoContent)
	})
	return mux
}

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestRegistrar_RegisterAndDeregister(t *testing.T) {
	envID := uuid.NewString()
	be := &fakeBrain{envID: envID}
	ts := httptest.NewServer(be.handler(t))
	defer ts.Close()

	reg, err := NewRegistrar(context.Background(), Config{
		BrainURL:        ts.URL,
		Token:           "tok-system",
		MachineName:     "runtime-test",
		PoolTag:         "runtime-prod",
		HeartbeatPeriod: 200 * time.Millisecond,
	}, newTestLogger())
	if err != nil {
		t.Fatalf("NewRegistrar: %v", err)
	}
	if reg.EnvironmentID().String() != envID {
		t.Errorf("envID=%s want %s", reg.EnvironmentID(), envID)
	}
	if be.registerCalls.Load() != 1 {
		t.Errorf("register calls=%d want 1", be.registerCalls.Load())
	}

	// 至少跑一次心跳
	time.Sleep(350 * time.Millisecond)
	if be.heartbeatHits.Load() < 1 {
		t.Errorf("heartbeat hits=%d want ≥1", be.heartbeatHits.Load())
	}

	reg.Stop(context.Background())
	if be.deregisterHit.Load() != 1 {
		t.Errorf("deregister hits=%d want 1", be.deregisterHit.Load())
	}
}

func TestRegistrar_RegisterFails(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/agent/environments", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"bad token"}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	_, err := NewRegistrar(context.Background(), Config{
		BrainURL: ts.URL,
		Token:    "wrong",
	}, newTestLogger())
	if err == nil {
		t.Fatal("expected register error")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("err=%v want 401 in message", err)
	}
}

func TestRegistrar_HeartbeatReregisterOn404(t *testing.T) {
	envID := uuid.NewString()
	be := &fakeBrain{envID: envID, heartbeat404OnNthCall: 1}
	ts := httptest.NewServer(be.handler(t))
	defer ts.Close()

	reg, err := NewRegistrar(context.Background(), Config{
		BrainURL:        ts.URL,
		Token:           "tok",
		HeartbeatPeriod: 200 * time.Millisecond,
	}, newTestLogger())
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Stop(context.Background())

	// 初始注册 1 次 + 心跳 404 后再注册 1 次 = 至少 2 次 register
	time.Sleep(500 * time.Millisecond)
	if got := be.registerCalls.Load(); got < 2 {
		t.Errorf("register calls=%d want ≥2 (heartbeat 404 should trigger re-register)", got)
	}
}

func TestRegistrar_RequiresBrainURL(t *testing.T) {
	_, err := NewRegistrar(context.Background(), Config{Token: "x"}, newTestLogger())
	if err == nil {
		t.Fatal("expected BrainURL-required error")
	}
}

func TestRegistrar_RequiresToken(t *testing.T) {
	_, err := NewRegistrar(context.Background(), Config{BrainURL: "http://x"}, newTestLogger())
	if err == nil {
		t.Fatal("expected Token-required error")
	}
}
