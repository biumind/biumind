// set_permission_mode 下行链路的 daemon 侧单测（P1 遗留 1）：
// handleControl 收到 control 队列消息后应用到在飞 agent 的权限
// Context 并回 control_response ack。
//
// 不打真 broker：httptest 起 fake brain 收 publish/ack，直接调
// handleControl（与 elicitation_test.go 同一风格）。

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

	"github.com/google/uuid"
)

// modeFakeBrain 只实现本测试族需要的两个端点：session publish（捕获
// 帧体）与 control ack。
type modeFakeBrain struct {
	mu        sync.Mutex
	published []map[string]any
}

func (f *modeFakeBrain) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/agent/sessions/{id}/publish", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.published = append(f.published, body)
		f.mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
	})
	mux.HandleFunc("POST /v1/agent/control/{env_id}/ack/{token}", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})
	return mux
}

func (f *modeFakeBrain) lastPublished(t *testing.T) map[string]any {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.published) == 0 {
		t.Fatal("no frame published")
	}
	return f.published[len(f.published)-1]
}

func newModeWorker(t *testing.T, be *modeFakeBrain) *Worker {
	t.Helper()
	ts := httptest.NewServer(be.handler())
	t.Cleanup(ts.Close)
	return NewWorker(NewClient(ts.URL, "tok", nil), WorkerConfig{
		EnvironmentName: "test",
	}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func modeControlBody(sessionID, requestID, mode string) json.RawMessage {
	return mustJSON(map[string]any{
		"type":       "set_permission_mode",
		"session_id": sessionID,
		"request_id": requestID,
		"mode":       mode,
	})
}

func TestWorker_SetPermissionMode_AppliesToInflightAgent(t *testing.T) {
	be := &modeFakeBrain{}
	w := newModeWorker(t, be)
	build, upstream := newStubAgentBuilder(t)
	defer upstream.Close()
	agent, err := build(context.Background(), WorkPayload{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	sid := uuid.New()
	w.trackAgent(sid, agent)

	w.handleControl(context.Background(), uuid.New(), &ControlItem{
		AckToken: "tok-m1",
		Body:     modeControlBody(sid.String(), "req-m1", "auto"),
	})

	if got := agent.Permissions().Mode(); got != "auto" {
		t.Errorf("mode = %q, want auto", got)
	}
	// ack 帧：control_response success，response 体带落地后的 mode。
	frame := be.lastPublished(t)
	if frame["type"] != "control_response" {
		t.Fatalf("published frame type = %v, want control_response", frame["type"])
	}
	resp, _ := frame["response"].(map[string]any)
	if resp["subtype"] != "success" || resp["request_id"] != "req-m1" {
		t.Errorf("ack response wrong: %v", resp)
	}
	body, _ := resp["response"].(map[string]any)
	if body["mode"] != "auto" {
		t.Errorf("ack mode payload = %v, want auto", resp["response"])
	}
}

// plan 进出走 EnterPlanMode / ExitPlanMode 簿记：plan → auto 后
// prePlanMode 清空、模式落在 auto。
func TestWorker_SetPermissionMode_PlanBookkeeping(t *testing.T) {
	be := &modeFakeBrain{}
	w := newModeWorker(t, be)
	build, upstream := newStubAgentBuilder(t)
	defer upstream.Close()
	agent, err := build(context.Background(), WorkPayload{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	sid := uuid.New()
	w.trackAgent(sid, agent)

	ctrl := &ControlItem{AckToken: "tok", Body: modeControlBody(sid.String(), "r1", "plan")}
	w.handleControl(context.Background(), uuid.New(), ctrl)
	if got := agent.Permissions().Mode(); got != "plan" {
		t.Fatalf("mode = %q, want plan", got)
	}

	ctrl.Body = modeControlBody(sid.String(), "r2", "auto")
	w.handleControl(context.Background(), uuid.New(), ctrl)
	if got := agent.Permissions().Mode(); got != "auto" {
		t.Errorf("mode = %q, want auto after leaving plan", got)
	}
	if pre := agent.Permissions().PrePlanMode(); pre != "" {
		t.Errorf("prePlanMode should be cleared after exit; got %q", pre)
	}
}

// session 不在飞：回 error ack 而不是静默 —— client 据此知道本次
// 切换只在下个会话生效。
func TestWorker_SetPermissionMode_UnknownSession(t *testing.T) {
	be := &modeFakeBrain{}
	w := newModeWorker(t, be)

	w.handleControl(context.Background(), uuid.New(), &ControlItem{
		AckToken: "tok-m2",
		Body:     modeControlBody(uuid.New().String(), "req-m2", "auto"),
	})

	frame := be.lastPublished(t)
	resp, _ := frame["response"].(map[string]any)
	if resp["subtype"] != "error" {
		t.Errorf("ack subtype = %v, want error", resp["subtype"])
	}
}

// 非法 session_id：不 panic、不投递 ack（没法寻址）。
func TestWorker_SetPermissionMode_InvalidSessionID(t *testing.T) {
	be := &modeFakeBrain{}
	w := newModeWorker(t, be)
	w.handleControl(context.Background(), uuid.New(), &ControlItem{
		AckToken: "tok-m3",
		Body:     modeControlBody("not-a-uuid", "req-m3", "auto"),
	})
	if len(be.published) != 0 {
		t.Errorf("invalid session_id should not publish an ack; got %v", be.published)
	}
}
