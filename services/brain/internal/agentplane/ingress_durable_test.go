// ingress 两级分流 + kind 字段测试（P3-c durable resume / C3）。
//
// 第一级（内存 pending map,活 chat loop）由 ingress_elicitation_route_test.go
// 覆盖；这里覆盖：
//   - 帧头 kind 优先于形状判别（旧端无 kind 回退形状,已有测试覆盖）
//   - 第二级 DB CAS：chat 僵尸（envID=nil）落答案 + 补发 form_answer 帧；
//     daemon 模式（envID!=nil）落答案 + 照投 control 队列；CAS miss 回退兜底

package agentplane

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

// controlResponseFrame 组一帧 control_response 回包。kind 空串 = 旧端（无
// kind 字段）。
func controlResponseFrame(kind, requestID string, body string) []byte {
	f := `{"type":"control_response",`
	if kind != "" {
		f += `"kind":"` + kind + `",`
	}
	f += `"response":{"subtype":"success","request_id":"` + requestID + `","response":` + body + `}}`
	return []byte(f)
}

// TestIngressKindFieldOverridesShape：帧头 kind 显式声明时优先于 response
// 体形状判别 —— 形状像 permission（有 behavior 键）但 kind=
// elicitation_response → 投 control 队列 type=elicitation_response；反之
// 形状像 elicitation 但 kind=permission_response → permission_response。
func TestIngressKindFieldOverridesShape(t *testing.T) {
	js := &fakeJS{}
	ingress := NewIngress(nil, nil, nil, discardLogger())
	ingress.SetElicitations(NewElicitationCenter(discardLogger()))
	ingress.SetQueue(NewQueue(js))

	sessionID := uuid.New()
	envID := uuid.New()

	// kind=elicitation_response + 体形状像 permission（behavior 键）
	f1 := controlResponseFrame("elicitation_response", "req-k1", `{"behavior":"allow","action":"accept"}`)
	if !ingress.maybeRoutePermissionResponse(context.Background(), sessionID, &envID, f1) {
		t.Fatal("should be claimed")
	}
	if got := controlQueuePayload(t, js, 0)["type"]; got != "elicitation_response" {
		t.Errorf("type=%v want elicitation_response (kind wins over shape)", got)
	}

	// kind=permission_response + 体形状像 elicitation（action 无 behavior）
	f2 := controlResponseFrame("permission_response", "req-k2", `{"action":"accept","content":{"answer":"x"}}`)
	ingress.maybeRoutePermissionResponse(context.Background(), sessionID, &envID, f2)
	if got := controlQueuePayload(t, js, 1)["type"]; got != "permission_response" {
		t.Errorf("type=%v want permission_response (kind wins over shape)", got)
	}

	// 未知 kind 值 → 回退形状判别
	f3 := controlResponseFrame("future_kind", "req-k3", `{"action":"accept","content":{}}`)
	ingress.maybeRoutePermissionResponse(context.Background(), sessionID, &envID, f3)
	if got := controlQueuePayload(t, js, 2)["type"]; got != "elicitation_response" {
		t.Errorf("type=%v want elicitation_response (unknown kind → shape fallback)", got)
	}
}

// TestIngressLateAnswer_ChatZombie：chat 僵尸会话（loop 已死,envID=nil）
// 的迟到作答 → CAS 落答案 + 补发 form_answer 帧到 .out,不投 control 队列。
func TestIngressLateAnswer_ChatZombie(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()
	ctx := context.Background()
	sess := seedChatSession(t, h, "paused")

	rid := uuid.New()
	if err := h.store.InsertElicitation(ctx, rid, sess.SessionID, elicTestPayload("选哪个?")); err != nil {
		t.Fatal(err)
	}

	js := &fakeJS{}
	ingress := NewIngress(nil, h.store, nil, discardLogger())
	ingress.SetElicitations(NewElicitationCenter(discardLogger())) // 内存 miss（loop 死了）
	ingress.SetQueue(NewQueue(js))

	frame := controlResponseFrame("elicitation_response", rid.String(), `{"action":"accept","content":{"answer":"A"}}`)
	if !ingress.maybeRoutePermissionResponse(ctx, sess.SessionID, nil, frame) {
		t.Fatal("should be claimed")
	}

	// 答案落库
	e, err := h.store.GetElicitation(ctx, rid)
	if err != nil || e.Status != "answered" {
		t.Fatalf("status=%q err=%v want answered", e.Status, err)
	}
	// form_answer 帧补发到 .out（不是 control 队列）
	js.mu.Lock()
	defer js.mu.Unlock()
	if len(js.publishes) != 1 {
		t.Fatalf("publishes=%d want 1", len(js.publishes))
	}
	if want := SessionSubjectOut(sess.SessionID.String()); js.publishes[0].Subject != want {
		t.Errorf("subject=%q want %q", js.publishes[0].Subject, want)
	}
	var fa map[string]any
	raw, _ := js.publishes[0].Payload.(json.RawMessage)
	if err := json.Unmarshal(raw, &fa); err != nil {
		t.Fatalf("published payload not json: %v", err)
	}
	if fa["type"] != "system" || fa["subtype"] != "form_answer" {
		t.Errorf("frame type=%v subtype=%v want system/form_answer", fa["type"], fa["subtype"])
	}
	if fa["request_id"] != rid.String() || fa["question"] != "选哪个?" || fa["action"] != "accept" {
		t.Errorf("form_answer frame=%v", fa)
	}
}

// TestIngressLateAnswer_DaemonAlive：daemon 模式（envID!=nil,daemon 还活着
// 在等）的作答 → CAS 落答案 + 照投 control 队列（elicitation_response）。
func TestIngressLateAnswer_DaemonAlive(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()
	ctx := context.Background()
	uid := uuid.New()
	env, err := h.store.RegisterEnvironment(ctx, CreateEnvironmentReq{
		UserID: &uid, WorkerKind: "biu_daemon", MachineName: "m",
	})
	if err != nil {
		t.Fatal(err)
	}
	sess, err := h.store.InsertSession(ctx, CreateSessionReq{
		UserID: uid, EnvironmentID: &env.EnvironmentID, Mode: "agent", State: "active",
	})
	if err != nil {
		t.Fatal(err)
	}
	rid := uuid.New()
	if err := h.store.InsertElicitation(ctx, rid, sess.SessionID, elicTestPayload("q")); err != nil {
		t.Fatal(err)
	}

	js := &fakeJS{}
	ingress := NewIngress(nil, h.store, nil, discardLogger())
	ingress.SetElicitations(NewElicitationCenter(discardLogger()))
	ingress.SetQueue(NewQueue(js))

	// 旧端（无 kind）+ elicitation 形状 → 形状判别 → 第二级 CAS
	frame := controlResponseFrame("", rid.String(), `{"action":"accept","content":{"answer":"A"}}`)
	if !ingress.maybeRoutePermissionResponse(ctx, sess.SessionID, &env.EnvironmentID, frame) {
		t.Fatal("should be claimed")
	}
	e, _ := h.store.GetElicitation(ctx, rid)
	if e.Status != "answered" {
		t.Errorf("status=%q want answered (CAS before enqueue)", e.Status)
	}
	got := controlQueuePayload(t, js, 0)
	if got["type"] != "elicitation_response" || got["request_id"] != rid.String() {
		t.Errorf("control payload=%v", got)
	}
	js.mu.Lock()
	if subj := js.publishes[0].Subject; subj != ControlSubject(env.EnvironmentID.String()) {
		t.Errorf("subject=%q want control subject", subj)
	}
	js.mu.Unlock()
}

// TestIngressLateAnswer_CASMissFallsBack：无 pending 行（replay / 已过期 /
// P3-c 前提问）→ CAS miss 回退兜底：envID=nil 静默丢弃,不补帧不落库。
func TestIngressLateAnswer_CASMissFallsBack(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()
	ctx := context.Background()

	js := &fakeJS{}
	ingress := NewIngress(nil, h.store, nil, discardLogger())
	ingress.SetElicitations(NewElicitationCenter(discardLogger()))
	ingress.SetQueue(NewQueue(js))

	frame := controlResponseFrame("elicitation_response", uuid.NewString(), `{"action":"accept","content":{"answer":"A"}}`)
	if !ingress.maybeRoutePermissionResponse(ctx, uuid.New(), nil, frame) {
		t.Fatal("control_response should be claimed even when dropped")
	}
	js.mu.Lock()
	defer js.mu.Unlock()
	if len(js.publishes) != 0 {
		t.Errorf("publishes=%d want 0 (CAS miss → legacy drop)", len(js.publishes))
	}
}

// TestIngressLateAnswer_ReplayDedup：同一作答来两遍（客户端 retry × WS
// 重连）,CAS 只认领第一次;第二次走兜底（chat 僵尸场景=静默丢弃）。
func TestIngressLateAnswer_ReplayDedup(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()
	ctx := context.Background()
	sess := seedChatSession(t, h, "paused")
	rid := uuid.New()
	if err := h.store.InsertElicitation(ctx, rid, sess.SessionID, elicTestPayload("q")); err != nil {
		t.Fatal(err)
	}

	js := &fakeJS{}
	ingress := NewIngress(nil, h.store, nil, discardLogger())
	ingress.SetElicitations(NewElicitationCenter(discardLogger()))
	ingress.SetQueue(NewQueue(js))

	frame := controlResponseFrame("elicitation_response", rid.String(), `{"action":"decline"}`)
	ingress.maybeRoutePermissionResponse(ctx, sess.SessionID, nil, frame)
	ingress.maybeRoutePermissionResponse(ctx, sess.SessionID, nil, frame) // replay

	js.mu.Lock()
	defer js.mu.Unlock()
	if len(js.publishes) != 1 {
		t.Errorf("publishes=%d want 1 (replay deduped by CAS)", len(js.publishes))
	}
	e, _ := h.store.GetElicitation(ctx, rid)
	var ans map[string]any
	_ = json.Unmarshal(e.Answer, &ans)
	if ans["action"] != "decline" {
		t.Errorf("answer action=%v want decline (first wins)", ans["action"])
	}
}

// TestIngressInProcessStillFirst：内存 map 命中（活 loop）时不碰 DB ——
// 第一级优先于第二级。
func TestIngressInProcessStillFirst(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()
	ctx := context.Background()
	sess := seedChatSession(t, h, "active")
	rid := uuid.New()
	if err := h.store.InsertElicitation(ctx, rid, sess.SessionID, elicTestPayload("q")); err != nil {
		t.Fatal(err)
	}

	center := NewElicitationCenter(discardLogger())
	ch := center.Register(rid.String())
	ingress := NewIngress(nil, h.store, nil, discardLogger())
	ingress.SetElicitations(center)

	frame := controlResponseFrame("", rid.String(), `{"action":"accept","content":{"answer":"B"}}`)
	if !ingress.maybeRoutePermissionResponse(ctx, sess.SessionID, nil, frame) {
		t.Fatal("should be claimed")
	}
	select {
	case ans := <-ch:
		if ans.Action != "accept" {
			t.Errorf("in-process answer=%v", ans)
		}
	default:
		t.Fatal("in-process Resolve not delivered")
	}
	// DB 行仍 pending（活 loop 的终态由生产者补发的 form_answer 帧经
	// observer 落 answered,不是 ingress 直接写）
	e, _ := h.store.GetElicitation(ctx, rid)
	if e.Status != "pending" {
		t.Errorf("status=%q want pending (in-process path doesn't CAS)", e.Status)
	}
}
