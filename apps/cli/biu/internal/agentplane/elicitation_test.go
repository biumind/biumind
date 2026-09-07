// daemon 提问表单（agent-ask-form P2-b）测试：
//   - 组帧形状（requested_schema 与 brain chat 模式逐字段对齐的锁定测试）
//   - 回包路由（answerAsk 按 request_id 唤醒 / handleControl 分流）
//   - 超时 / decline / cancel / ctx 取消 / 发布失败 全部降级 Cancelled|error
//   - session 结束清理 pending（防 goroutine 泄漏）+ 并发 session 隔离
//   - 答案校验各分支（设计 §3.3：content 自由 JSON 必须服务端校验）

package agentplane

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/pkg/biumindkit"
	"github.com/google/uuid"
)

// newAskTestWorker 起一个 publish 帧落进 frames chan 的 Worker（不走
// register/poll loop —— 直接调 askUserFor / handleControl 测链路）。
func newAskTestWorker(t *testing.T) (*Worker, <-chan []byte) {
	t.Helper()
	frames := make(chan []byte, 16)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/agent/sessions/{id}/publish", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		frames <- body
		w.WriteHeader(http.StatusAccepted)
	})
	mux.HandleFunc("POST /v1/agent/control/{env_id}/ack/{token}", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	w := NewWorker(NewClient(ts.URL, "tok", nil), WorkerConfig{EnvironmentName: "t"}, nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	return w, frames
}

var askTestQuestion = biumindkit.UserQuestion{
	Question: "Pick a color",
	Header:   "color",
	Options: []biumindkit.UserOption{
		{Label: "Red", Description: "warm"},
		{Label: "Blue", Description: "cool", Preview: "#00f"},
	},
}

// askResult 收 askUserFor 的一次结果。
type askResult struct {
	ans biumindkit.UserAnswer
	err error
}

// startAsk 在后台跑一问，返回结果 chan + 发出的控制帧原文（解析出
// request_id 供测试回包，也可做帧形状断言）。
func startAsk(t *testing.T, w *Worker, frames <-chan []byte, sessionID uuid.UUID, q biumindkit.UserQuestion) (<-chan askResult, string, []byte) {
	t.Helper()
	res := make(chan askResult, 1)
	go func() {
		ans, err := w.askUserFor(sessionID)(context.Background(), q)
		res <- askResult{ans: ans, err: err}
	}()
	select {
	case raw := <-frames:
		var frame struct {
			Type      string `json:"type"`
			RequestID string `json:"request_id"`
		}
		if err := json.Unmarshal(raw, &frame); err != nil {
			t.Fatalf("frame unparseable: %v", err)
		}
		if frame.Type != "control_request" {
			t.Fatalf("frame type=%q, want control_request", frame.Type)
		}
		if frame.RequestID == "" {
			t.Fatal("frame missing request_id")
		}
		return res, frame.RequestID, raw
	case <-time.After(2 * time.Second):
		t.Fatal("askUserFor 没有 publish 控制帧")
		return nil, "", nil
	}
}

func recvAsk(t *testing.T, res <-chan askResult) askResult {
	t.Helper()
	select {
	case r := <-res:
		return r
	case <-time.After(2 * time.Second):
		t.Fatal("askUserFor 未在预期内返回")
		return askResult{}
	}
}

func TestQuestionToRequestedSchema_SingleSelect(t *testing.T) {
	raw, err := questionToRequestedSchema(askTestQuestion)
	if err != nil {
		t.Fatalf("build schema: %v", err)
	}
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("schema unparseable: %v", err)
	}
	if schema["type"] != "object" {
		t.Errorf("type=%v, want object", schema["type"])
	}
	if schema["title"] != "Pick a color" {
		t.Errorf("title=%v, want 问题全文", schema["title"])
	}
	req, _ := schema["required"].([]any)
	if len(req) != 1 || req[0] != "answer" {
		t.Errorf("required=%v, want [answer]", schema["required"])
	}
	props, _ := schema["properties"].(map[string]any)
	answer, _ := props["answer"].(map[string]any)
	if answer["type"] != "string" {
		t.Errorf("answer.type=%v, want string（单选）", answer["type"])
	}
	enum, _ := answer["enum"].([]any)
	if len(enum) != 2 || enum[0] != "Red" || enum[1] != "Blue" {
		t.Errorf("answer.enum=%v, want [Red Blue]", answer["enum"])
	}
	if notes, _ := props["notes"].(map[string]any); notes["type"] != "string" {
		t.Errorf("notes prop=%v, want {type:string}", props["notes"])
	}
	xq, _ := schema["x-biumind-question"].(map[string]any)
	if xq["question"] != "Pick a color" || xq["header"] != "color" || xq["multi_select"] != false {
		t.Errorf("x-biumind-question=%v", xq)
	}
	opts, _ := xq["options"].([]any)
	if len(opts) != 2 {
		t.Fatalf("options=%v, want 2 entries", xq["options"])
	}
	opt0, _ := opts[0].(map[string]any)
	if opt0["label"] != "Red" || opt0["description"] != "warm" {
		t.Errorf("options[0]=%v", opt0)
	}
	if _, hasPreview := opt0["preview"]; hasPreview {
		t.Errorf("options[0] 不该带 preview（空值省略）: %v", opt0)
	}
	opt1, _ := opts[1].(map[string]any)
	if opt1["preview"] != "#00f" {
		t.Errorf("options[1].preview=%v, want #00f", opt1["preview"])
	}
}

func TestQuestionToRequestedSchema_MultiSelect(t *testing.T) {
	q := askTestQuestion
	q.MultiSelect = true
	raw, err := questionToRequestedSchema(q)
	if err != nil {
		t.Fatalf("build schema: %v", err)
	}
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("schema unparseable: %v", err)
	}
	answer, _ := schema["properties"].(map[string]any)["answer"].(map[string]any)
	if answer["type"] != "array" {
		t.Errorf("answer.type=%v, want array（多选）", answer["type"])
	}
	if answer["minItems"] != float64(1) {
		t.Errorf("answer.minItems=%v, want 1", answer["minItems"])
	}
	items, _ := answer["items"].(map[string]any)
	if items["type"] != "string" {
		t.Errorf("items.type=%v, want string", items["type"])
	}
	if enum, _ := items["enum"].([]any); len(enum) != 2 {
		t.Errorf("items.enum=%v, want 2 labels", items["enum"])
	}
	if xq, _ := schema["x-biumind-question"].(map[string]any); xq["multi_select"] != true {
		t.Errorf("x-biumind-question.multi_select=%v, want true", xq["multi_select"])
	}
}

func TestAnswerFromElicitationContent(t *testing.T) {
	multiQ := askTestQuestion
	multiQ.MultiSelect = true

	cases := []struct {
		name       string
		q          biumindkit.UserQuestion
		ans        elicitationAnswer
		wantSel    []int
		wantNote   string
		wantCancel bool
		wantErr    bool
	}{
		{name: "accept single valid", q: askTestQuestion,
			ans:     elicitationAnswer{Action: "accept", Content: map[string]any{"answer": "Blue"}},
			wantSel: []int{1}},
		{name: "accept single with notes", q: askTestQuestion,
			ans:     elicitationAnswer{Action: "accept", Content: map[string]any{"answer": "Red", "notes": "why not"}},
			wantSel: []int{0}, wantNote: "why not"},
		{name: "accept missing answer", q: askTestQuestion,
			ans:     elicitationAnswer{Action: "accept", Content: map[string]any{"notes": "x"}},
			wantErr: true},
		{name: "accept non-string answer", q: askTestQuestion,
			ans:     elicitationAnswer{Action: "accept", Content: map[string]any{"answer": float64(1)}},
			wantErr: true},
		{name: "accept out-of-enum", q: askTestQuestion,
			ans:     elicitationAnswer{Action: "accept", Content: map[string]any{"answer": "Green"}},
			wantErr: true},
		{name: "multi valid dedup", q: multiQ,
			ans:     elicitationAnswer{Action: "accept", Content: map[string]any{"answer": []any{"Blue", "Red", "Blue"}}},
			wantSel: []int{1, 0}},
		{name: "multi empty array", q: multiQ,
			ans:     elicitationAnswer{Action: "accept", Content: map[string]any{"answer": []any{}}},
			wantErr: true},
		{name: "multi non-array", q: multiQ,
			ans:     elicitationAnswer{Action: "accept", Content: map[string]any{"answer": "Red"}},
			wantErr: true},
		{name: "multi non-string item", q: multiQ,
			ans:     elicitationAnswer{Action: "accept", Content: map[string]any{"answer": []any{"Red", float64(2)}}},
			wantErr: true},
		{name: "multi out-of-enum item", q: multiQ,
			ans:     elicitationAnswer{Action: "accept", Content: map[string]any{"answer": []any{"Red", "Green"}}},
			wantErr: true},
		{name: "decline → cancelled", q: askTestQuestion,
			ans:        elicitationAnswer{Action: "decline"},
			wantCancel: true},
		{name: "cancel → cancelled", q: askTestQuestion,
			ans:        elicitationAnswer{Action: "cancel"},
			wantCancel: true},
		{name: "unknown action → cancelled", q: askTestQuestion,
			ans:        elicitationAnswer{Action: "bogus", Content: map[string]any{"answer": "Red"}},
			wantCancel: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := answerFromElicitationContent(tc.q, tc.ans)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want error, got %+v", out)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if out.Cancelled != tc.wantCancel {
				t.Errorf("Cancelled=%v, want %v", out.Cancelled, tc.wantCancel)
			}
			if tc.wantCancel {
				return
			}
			if len(out.Selected) != len(tc.wantSel) {
				t.Fatalf("Selected=%v, want %v", out.Selected, tc.wantSel)
			}
			for i, idx := range tc.wantSel {
				if out.Selected[i] != idx {
					t.Fatalf("Selected=%v, want %v", out.Selected, tc.wantSel)
				}
			}
			if out.Notes != tc.wantNote {
				t.Errorf("Notes=%q, want %q", out.Notes, tc.wantNote)
			}
		})
	}
}

func TestWorker_AskUserFor_PublishesFrameAndRoutesAnswer(t *testing.T) {
	w, frames := newAskTestWorker(t)
	sessionID := uuid.New()
	res, requestID, raw := startAsk(t, w, frames, sessionID, askTestQuestion)

	// 组帧形状：control_request{elicitation, mode:form}，elicitation_id ==
	// request_id（回包关联键），requested_schema 在帧内。
	w.pendingAsksMu.Lock()
	pendingCount := len(w.pendingAsks)
	w.pendingAsksMu.Unlock()
	if pendingCount != 1 {
		t.Fatalf("pendingAsks=%d, want 1", pendingCount)
	}
	var frame struct {
		Request struct {
			Subtype         string          `json:"subtype"`
			Mode            string          `json:"mode"`
			McpServerName   string          `json:"mcp_server_name"`
			Message         string          `json:"message"`
			ElicitationID   string          `json:"elicitation_id"`
			RequestedSchema json.RawMessage `json:"requested_schema"`
		} `json:"request"`
	}
	if err := json.Unmarshal(raw, &frame); err != nil {
		t.Fatalf("frame unparseable: %v", err)
	}
	if frame.Request.Subtype != "elicitation" || frame.Request.Mode != "form" {
		t.Errorf("subtype=%q mode=%q, want elicitation/form", frame.Request.Subtype, frame.Request.Mode)
	}
	if frame.Request.McpServerName != "biumind.agent" {
		t.Errorf("mcp_server_name=%q, want biumind.agent", frame.Request.McpServerName)
	}
	if frame.Request.Message != "Pick a color" {
		t.Errorf("message=%q", frame.Request.Message)
	}
	if frame.Request.ElicitationID != requestID {
		t.Errorf("elicitation_id=%q, want == request_id %q", frame.Request.ElicitationID, requestID)
	}
	if len(frame.Request.RequestedSchema) == 0 {
		t.Error("requested_schema 为空")
	}

	// 回包路由：按 request_id 命中等候的 goroutine。
	w.answerAsk(requestID, elicitationAnswer{
		Action:  "accept",
		Content: map[string]any{"answer": "Blue", "notes": "cool tone"},
	})
	r := recvAsk(t, res)
	if r.err != nil {
		t.Fatalf("unexpected error: %v", r.err)
	}
	if r.ans.Cancelled || len(r.ans.Selected) != 1 || r.ans.Selected[0] != 1 || r.ans.Notes != "cool tone" {
		t.Errorf("answer=%+v, want Selected=[1] Notes=cool tone", r.ans)
	}
	// 应答后 pending 表项已摘除。
	w.pendingAsksMu.Lock()
	remaining := len(w.pendingAsks)
	w.pendingAsksMu.Unlock()
	if remaining != 0 {
		t.Fatalf("pendingAsks=%d after answer, want 0", remaining)
	}
}

func TestWorker_AskUserFor_TimeoutDegradesToCancelled(t *testing.T) {
	orig := askUserTimeout
	askUserTimeout = 150 * time.Millisecond
	t.Cleanup(func() { askUserTimeout = orig })

	w, frames := newAskTestWorker(t)
	res, _, _ := startAsk(t, w, frames, uuid.New(), askTestQuestion)
	r := recvAsk(t, res)
	if r.err != nil {
		t.Fatalf("超时应降级 Cancelled 而非 error, got %v", r.err)
	}
	if !r.ans.Cancelled {
		t.Errorf("answer=%+v, want Cancelled", r.ans)
	}
	// 超时返回后 pending 表项必须清掉。
	deadline := time.Now().Add(time.Second)
	for {
		w.pendingAsksMu.Lock()
		n := len(w.pendingAsks)
		w.pendingAsksMu.Unlock()
		if n == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("超时后 pendingAsks=%d, want 0", n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestWorker_AskUserFor_DeclineAndCancel(t *testing.T) {
	w, frames := newAskTestWorker(t)
	for _, action := range []string{"decline", "cancel"} {
		res, requestID, _ := startAsk(t, w, frames, uuid.New(), askTestQuestion)
		w.answerAsk(requestID, elicitationAnswer{Action: action})
		r := recvAsk(t, res)
		if r.err != nil || !r.ans.Cancelled {
			t.Errorf("action=%s: answer=%+v err=%v, want Cancelled nil-err", action, r.ans, r.err)
		}
	}
}

func TestWorker_AskUserFor_InvalidContentIsError(t *testing.T) {
	w, frames := newAskTestWorker(t)
	res, requestID, _ := startAsk(t, w, frames, uuid.New(), askTestQuestion)
	// 伪造回包：选项不在声明集合内（设计 §3.3 服务端校验）。
	w.answerAsk(requestID, elicitationAnswer{Action: "accept", Content: map[string]any{"answer": "Forged"}})
	r := recvAsk(t, res)
	if r.err == nil {
		t.Fatalf("非法 content 应返回 error（biumindkit 转 Cancelled soft error）, got %+v", r.ans)
	}
}

func TestWorker_AskUserFor_PublishFailureIsError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/agent/sessions/{id}/publish", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	w := NewWorker(NewClient(ts.URL, "tok", nil), WorkerConfig{EnvironmentName: "t"}, nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)))

	_, err := w.askUserFor(uuid.New())(context.Background(), askTestQuestion)
	if err == nil {
		t.Fatal("publish 失败应返回 error")
	}
}

func TestWorker_AskUserFor_CtxCancel(t *testing.T) {
	w, frames := newAskTestWorker(t)
	sessionID := uuid.New()
	res := make(chan askResult, 1)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		ans, err := w.askUserFor(sessionID)(ctx, askTestQuestion)
		res <- askResult{ans: ans, err: err}
	}()
	<-frames // 等帧发出（goroutine 进入阻塞等待）
	cancel()
	select {
	case r := <-res:
		if r.err == nil {
			t.Errorf("ctx 取消应返回 error, got %+v", r.ans)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ctx 取消后 askUserFor 未返回")
	}
}

func TestWorker_HandleControl_ElicitationResponse(t *testing.T) {
	w, frames := newAskTestWorker(t)
	envID := uuid.New()
	sessionID := uuid.New()
	res, requestID, _ := startAsk(t, w, frames, sessionID, askTestQuestion)

	body := mustJSON(map[string]any{
		"type":       "elicitation_response",
		"session_id": sessionID.String(),
		"request_id": requestID,
		"subtype":    "success",
		"response":   json.RawMessage(`{"action":"accept","content":{"answer":"Red"}}`),
	})
	w.handleControl(context.Background(), envID, &ControlItem{AckToken: "tok-e", Body: body})
	r := recvAsk(t, res)
	if r.err != nil || r.ans.Cancelled || len(r.ans.Selected) != 1 || r.ans.Selected[0] != 0 {
		t.Errorf("answer=%+v err=%v, want Selected=[0]", r.ans, r.err)
	}
}

func TestWorker_HandleControl_ElicitationErrorSubtypeCancels(t *testing.T) {
	w, frames := newAskTestWorker(t)
	res, requestID, _ := startAsk(t, w, frames, uuid.New(), askTestQuestion)
	body := mustJSON(map[string]any{
		"type":       "elicitation_response",
		"request_id": requestID,
		"subtype":    "error",
		"error":      "client exploded",
	})
	w.handleControl(context.Background(), uuid.New(), &ControlItem{AckToken: "tok-e2", Body: body})
	r := recvAsk(t, res)
	if r.err != nil || !r.ans.Cancelled {
		t.Errorf("answer=%+v err=%v, want Cancelled（error subtype → cancel）", r.ans, r.err)
	}
}

func TestWorker_HandleControl_UnknownElicitationIDDropped(t *testing.T) {
	w, _ := newAskTestWorker(t)
	body := mustJSON(map[string]any{
		"type":       "elicitation_response",
		"request_id": uuid.NewString(), // 不存在的 pending
		"subtype":    "success",
		"response":   json.RawMessage(`{"action":"accept","content":{"answer":"Red"}}`),
	})
	// 不 panic、不阻塞即通过（静默丢弃：已超时 / 不属于本进程 / 重复回包）。
	w.handleControl(context.Background(), uuid.New(), &ControlItem{AckToken: "tok-e3", Body: body})
}

func TestWorker_CancelPendingAsks_ReleasesOnlyThatSession(t *testing.T) {
	w, frames := newAskTestWorker(t)
	s1, s2 := uuid.New(), uuid.New()

	res1, _, _ := startAsk(t, w, frames, s1, askTestQuestion)
	res2, requestID2, _ := startAsk(t, w, frames, s2, askTestQuestion)

	// session 1 结束 → 它的 pending 全部 Cancelled 释放；session 2 不动。
	w.cancelPendingAsks(s1)
	r1 := recvAsk(t, res1)
	if r1.err != nil || !r1.ans.Cancelled {
		t.Errorf("session1 answer=%+v err=%v, want Cancelled", r1.ans, r1.err)
	}
	w.pendingAsksMu.Lock()
	if len(w.pendingAsks) != 1 {
		t.Fatalf("pendingAsks=%d, want 1（session2 的还在）", len(w.pendingAsks))
	}
	entry := w.pendingAsks[requestID2]
	w.pendingAsksMu.Unlock()
	if entry.sessionID != s2 {
		t.Errorf("剩余 pending 属于 %v, want session2 %v", entry.sessionID, s2)
	}
	// session 2 正常应答不受 session1 清理影响（并发隔离）。
	w.answerAsk(requestID2, elicitationAnswer{Action: "accept", Content: map[string]any{"answer": "Blue"}})
	r2 := recvAsk(t, res2)
	if r2.err != nil || r2.ans.Cancelled || len(r2.ans.Selected) != 1 || r2.ans.Selected[0] != 1 {
		t.Errorf("session2 answer=%+v err=%v, want Selected=[1]", r2.ans, r2.err)
	}
}

// cancelPendingAsks 对空 map / 无匹配 session 是 noop（幂等，可重复调）。
func TestWorker_CancelPendingAsks_NoMatchIsNoop(t *testing.T) {
	w, frames := newAskTestWorker(t)
	res, requestID, _ := startAsk(t, w, frames, uuid.New(), askTestQuestion)
	w.cancelPendingAsks(uuid.New()) // 别的 session
	w.cancelPendingAsks(uuid.Nil)
	select {
	case r := <-res:
		t.Fatalf("无匹配 session 的 cancelPendingAsks 不应触碰 pending: %+v", r)
	default:
	}
	w.answerAsk(requestID, elicitationAnswer{Action: "cancel"})
	if r := recvAsk(t, res); !r.ans.Cancelled {
		t.Errorf("answer=%+v, want Cancelled", r.ans)
	}
}
