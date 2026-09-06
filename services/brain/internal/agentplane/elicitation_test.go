// agent 提问表单（agent-ask-form P1-b）测试：
//   - ElicitationCenter 注册 / 应答 / 取消语义
//   - askUserFn：提问 → control_request{elicitation} 帧形状 + 回包映射
//   - answerFromElicitation 服务端校验（自由 JSON 防伪造，设计 §3.3）
//   - ingress control_response 按 request_id 分流（elicitation 进程内 /
//     permission 走 control 队列，各回各家）

package agentplane

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/pkg/biumindkit"
	sdkproto "github.com/biumind/biumind/packages/go-sdk/biu/sdkproto/v1"
	"github.com/google/uuid"
)

func testQuestion() biumindkit.UserQuestion {
	return biumindkit.UserQuestion{
		Question: "Pick a color?",
		Header:   "Color",
		Options: []biumindkit.UserOption{
			{Label: "red", Description: "warm"},
			{Label: "blue", Description: "cool"},
			{Label: "green", Description: "fresh"},
		},
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestElicitationCenter_RegisterResolveCancel(t *testing.T) {
	c := NewElicitationCenter(discardLogger())
	ch := c.Register("r1")
	if !c.Has("r1") {
		t.Fatal("Has(r1) should be true after Register")
	}
	if c.Has("r2") {
		t.Fatal("Has(r2) should be false — never registered")
	}
	if !c.Resolve("r1", ElicitationAnswer{Action: "accept"}) {
		t.Fatal("Resolve(r1) should hit")
	}
	select {
	case ans := <-ch:
		if ans.Action != "accept" {
			t.Errorf("action = %q, want accept", ans.Action)
		}
	case <-time.After(time.Second):
		t.Fatal("answer chan not fed after Resolve")
	}
	// 已消费 → 再 Resolve 落空；重复回包静默丢弃。
	if c.Resolve("r1", ElicitationAnswer{Action: "accept"}) {
		t.Fatal("second Resolve should miss")
	}
	// Cancel 之后 Resolve 也落空。
	c.Register("r3")
	c.Cancel("r3")
	if c.Has("r3") || c.Resolve("r3", ElicitationAnswer{Action: "accept"}) {
		t.Fatal("Cancelled entry must be gone")
	}
}

// askUserFn 全链路：发帧 → 模拟客户端回包 → UserAnswer 映射。
func TestAskUserFn_FrameShapeAndAnswerMapping(t *testing.T) {
	js := &fakeJS{}
	center := NewElicitationCenter(discardLogger())
	cr := &ChatRunner{Queue: NewQueue(js), Elicitations: center, Logger: discardLogger()}
	sessionID := uuid.New()

	fn := cr.askUserFn(sessionID)
	type result struct {
		ans biumindkit.UserAnswer
		err error
	}
	resCh := make(chan result, 1)
	go func() {
		ans, err := fn(context.Background(), testQuestion())
		resCh <- result{ans, err}
	}()

	// 等帧发出来。
	var raw []byte
	for i := 0; i < 100; i++ {
		js.mu.Lock()
		if len(js.publishes) > 0 {
			raw = js.publishes[0].Payload.(json.RawMessage)
			js.mu.Unlock()
			break
		}
		js.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	if raw == nil {
		t.Fatal("no control frame published")
	}
	var frame sdkproto.SDKControlRequest
	if err := json.Unmarshal(raw, &frame); err != nil {
		t.Fatalf("frame unmarshal: %v", err)
	}
	eli, ok := frame.Request.(*sdkproto.Elicitation)
	if !ok {
		t.Fatalf("request inner = %T, want *Elicitation", frame.Request)
	}
	if eli.Mode != "form" || eli.Message != "Pick a color?" {
		t.Errorf("elicitation = %+v", eli)
	}
	if eli.ElicitationID != frame.RequestID {
		t.Errorf("elicitation_id %q != request_id %q", eli.ElicitationID, frame.RequestID)
	}
	// requested_schema：单选 string+enum，含展示元数据。
	var schema struct {
		Type     string   `json:"type"`
		Title    string   `json:"title"`
		Required []string `json:"required"`
		Props    struct {
			Answer struct {
				Type string   `json:"type"`
				Enum []string `json:"enum"`
			} `json:"answer"`
		} `json:"properties"`
		Question struct {
			Header      string `json:"header"`
			MultiSelect bool   `json:"multi_select"`
			Options     []struct {
				Label       string `json:"label"`
				Description string `json:"description"`
			} `json:"options"`
		} `json:"x-biumind-question"`
	}
	if err := json.Unmarshal(eli.RequestedSchema, &schema); err != nil {
		t.Fatalf("requested_schema unmarshal: %v", err)
	}
	if schema.Type != "object" || schema.Title != "Pick a color?" {
		t.Errorf("schema head = %+v", schema)
	}
	if schema.Props.Answer.Type != "string" || len(schema.Props.Answer.Enum) != 3 {
		t.Errorf("answer prop = %+v, want string enum of 3", schema.Props.Answer)
	}
	if schema.Question.Header != "Color" || len(schema.Question.Options) != 3 {
		t.Errorf("x-biumind-question = %+v", schema.Question)
	}

	// 模拟客户端回包（经 ingress 分流到 center）。
	if !center.Resolve(frame.RequestID, ElicitationAnswer{
		Action:  "accept",
		Content: map[string]any{"answer": "blue"},
	}) {
		t.Fatal("Resolve should hit the pending question")
	}
	select {
	case r := <-resCh:
		if r.err != nil {
			t.Fatalf("askUser err: %v", r.err)
		}
		if len(r.ans.Selected) != 1 || r.ans.Selected[0] != 1 {
			t.Errorf("Selected = %v, want [1] (blue)", r.ans.Selected)
		}
		if r.ans.Cancelled {
			t.Error("answered question must not be Cancelled")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("askUser did not return after Resolve")
	}
	// pending 表项必须已清。
	if center.Has(frame.RequestID) {
		t.Error("pending entry leaked after answer")
	}
}

// 超时 → error（biumindkit 转 Cancelled → 工具 soft error "user
// cancelled/unanswered"）。丢帧安全的兜底。
func TestAskUserFn_TimeoutSoftError(t *testing.T) {
	orig := ElicitationTimeout
	ElicitationTimeout = 50 * time.Millisecond
	defer func() { ElicitationTimeout = orig }()

	js := &fakeJS{}
	cr := &ChatRunner{
		Queue:        NewQueue(js),
		Elicitations: NewElicitationCenter(discardLogger()),
		Logger:       discardLogger(),
	}
	fn := cr.askUserFn(uuid.New())
	_, err := fn(context.Background(), testQuestion())
	if err == nil {
		t.Fatal("timeout should surface an error")
	}
}

// 发布失败（JetStream nil）→ 立即 error，不挂起。
func TestAskUserFn_PublishFailure(t *testing.T) {
	cr := &ChatRunner{
		Queue:        NewQueue(nil),
		Elicitations: NewElicitationCenter(discardLogger()),
		Logger:       discardLogger(),
	}
	_, err := cr.askUserFn(uuid.New())(context.Background(), testQuestion())
	if err == nil {
		t.Fatal("publish failure should surface an error")
	}
}

func TestAnswerFromElicitation_Validation(t *testing.T) {
	q := testQuestion()
	multi := testQuestion()
	multi.MultiSelect = true

	t.Run("decline → cancelled", func(t *testing.T) {
		ans, err := answerFromElicitation(q, ElicitationAnswer{Action: "decline"})
		if err != nil || !ans.Cancelled {
			t.Errorf("decline: ans=%+v err=%v", ans, err)
		}
	})
	t.Run("cancel → cancelled", func(t *testing.T) {
		ans, err := answerFromElicitation(q, ElicitationAnswer{Action: "cancel"})
		if err != nil || !ans.Cancelled {
			t.Errorf("cancel: ans=%+v err=%v", ans, err)
		}
	})
	t.Run("accept single valid + notes", func(t *testing.T) {
		ans, err := answerFromElicitation(q, ElicitationAnswer{
			Action:  "accept",
			Content: map[string]any{"answer": "green", "notes": "go fresh"},
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(ans.Selected) != 1 || ans.Selected[0] != 2 || ans.Notes != "go fresh" {
			t.Errorf("ans = %+v", ans)
		}
	})
	t.Run("accept unknown label rejected", func(t *testing.T) {
		if _, err := answerFromElicitation(q, ElicitationAnswer{
			Action:  "accept",
			Content: map[string]any{"answer": "purple"},
		}); err == nil {
			t.Error("forged label must be rejected")
		}
	})
	t.Run("accept missing answer rejected", func(t *testing.T) {
		if _, err := answerFromElicitation(q, ElicitationAnswer{
			Action: "accept", Content: map[string]any{},
		}); err == nil {
			t.Error("missing answer must be rejected")
		}
	})
	t.Run("multi valid array", func(t *testing.T) {
		ans, err := answerFromElicitation(multi, ElicitationAnswer{
			Action:  "accept",
			Content: map[string]any{"answer": []any{"red", "green", "red"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(ans.Selected) != 2 { // 去重
			t.Errorf("Selected = %v, want 2 deduped", ans.Selected)
		}
	})
	t.Run("multi non-array rejected", func(t *testing.T) {
		if _, err := answerFromElicitation(multi, ElicitationAnswer{
			Action:  "accept",
			Content: map[string]any{"answer": "red"},
		}); err == nil {
			t.Error("multi-select with scalar answer must be rejected")
		}
	})
	t.Run("multi empty array rejected", func(t *testing.T) {
		if _, err := answerFromElicitation(multi, ElicitationAnswer{
			Action:  "accept",
			Content: map[string]any{"answer": []any{}},
		}); err == nil {
			t.Error("multi-select with empty answer must be rejected")
		}
	})
	t.Run("multi forged item rejected", func(t *testing.T) {
		if _, err := answerFromElicitation(multi, ElicitationAnswer{
			Action:  "accept",
			Content: map[string]any{"answer": []any{"red", "purple"}},
		}); err == nil {
			t.Error("multi-select with forged item must be rejected")
		}
	})
}

// ingress 分流：elicitation 回包命中 pending map → 进程内 Resolve（envID
// 为 nil 的 chat 模式也通）；未命中 → 仍按 permission 处理（envID nil 时
// 丢弃，行为与改动前一致）。
func TestIngressRoutesElicitationResponse(t *testing.T) {
	center := NewElicitationCenter(discardLogger())
	ingress := NewIngress(nil, nil, nil, discardLogger())
	ingress.SetElicitations(center)

	sessionID := uuid.New()
	ch := center.Register("req-eli-1")
	frame := []byte(`{"type":"control_response","response":{"subtype":"success",` +
		`"request_id":"req-eli-1","response":{"action":"accept","content":{"answer":"red"}}}}`)
	if !ingress.maybeRoutePermissionResponse(context.Background(), sessionID, nil, frame) {
		t.Fatal("control_response should be claimed")
	}
	select {
	case ans := <-ch:
		if ans.Action != "accept" || ans.Content["answer"] != "red" {
			t.Errorf("ans = %+v", ans)
		}
	case <-time.After(time.Second):
		t.Fatal("elicitation answer not routed in-process")
	}

	// permission 回包（request_id 不在 elicitation map）+ envID nil → 丢弃
	// （改动前同款行为），不碰 elicitation map。
	permFrame := []byte(`{"type":"control_response","response":{"subtype":"success",` +
		`"request_id":"req-perm-9","response":{"behavior":"allow"}}}`)
	if !ingress.maybeRoutePermissionResponse(context.Background(), sessionID, nil, permFrame) {
		t.Fatal("control_response should be claimed")
	}
	if center.Has("req-perm-9") {
		t.Error("permission response must not touch the elicitation map")
	}

	// 非 control_response 帧不认领。
	if ingress.maybeRoutePermissionResponse(context.Background(), sessionID, nil,
		[]byte(`{"type":"user_message"}`)) {
		t.Error("non-control_response frame must not be claimed")
	}
}

// elicitation 回包体损坏 → Resolve 一个 cancel，等候方立即出局而不是
// 干等超时。
func TestIngressElicitationUnparseableCancels(t *testing.T) {
	center := NewElicitationCenter(discardLogger())
	ingress := NewIngress(nil, nil, nil, discardLogger())
	ingress.SetElicitations(center)

	ch := center.Register("req-bad")
	frame := []byte(`{"type":"control_response","response":{"subtype":"success",` +
		`"request_id":"req-bad","response":"not-an-object"}}`)
	if !ingress.maybeRoutePermissionResponse(context.Background(), uuid.New(), nil, frame) {
		t.Fatal("control_response should be claimed")
	}
	select {
	case ans := <-ch:
		if ans.Action != "cancel" {
			t.Errorf("unparseable body should degrade to cancel, got %q", ans.Action)
		}
	case <-time.After(time.Second):
		t.Fatal("unparseable elicitation response left the waiter hanging")
	}
}
