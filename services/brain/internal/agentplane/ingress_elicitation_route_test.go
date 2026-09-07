// daemon elicitation 回包分流测试（agent-ask-form P2-b brain 配套）。
//
// daemon worker.askUserFor 发 control_request{elicitation} 后，client 作答
// 的 control_response 与 permission 答复共用帧型（subtype 只有
// success/error），ingress 按嵌套 response 体形状分流：
// ElicitationResponse{action, content} → control 队列 type=elicitation_response
// （daemon handleControl 据此投 pendingAsks）；PermissionResult{behavior} →
// type=permission_response（原行为不变）。chat 模式进程内分流
// （request_id 命中 ElicitationCenter pending map）必须不受影响。

package agentplane

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// control_queue_payload 从 fakeJS 捕获的 Publish 里取 payload map。
func controlQueuePayload(t *testing.T, js *fakeJS, idx int) map[string]any {
	t.Helper()
	js.mu.Lock()
	defer js.mu.Unlock()
	if len(js.publishes) <= idx {
		t.Fatalf("publishes = %d, want at least %d", len(js.publishes), idx+1)
	}
	m, ok := js.publishes[idx].Payload.(map[string]any)
	if !ok {
		t.Fatalf("payload type = %T, want map[string]any", js.publishes[idx].Payload)
	}
	return m
}

func TestIngressRoutesDaemonElicitationToControlQueue(t *testing.T) {
	center := NewElicitationCenter(discardLogger())
	js := &fakeJS{}
	ingress := NewIngress(nil, nil, nil, discardLogger())
	ingress.SetElicitations(center)
	ingress.SetQueue(NewQueue(js))

	sessionID := uuid.New()
	envID := uuid.New()

	// 1. daemon elicitation 回包（request_id 不在进程内 pending map，
	// response 体 {action, content}）→ control 队列 type=elicitation_response。
	eliFrame := []byte(`{"type":"control_response","response":{"subtype":"success",` +
		`"request_id":"req-ask-1","response":{"action":"accept","content":{"answer":"red"}}}}`)
	if !ingress.maybeRoutePermissionResponse(context.Background(), sessionID, &envID, eliFrame) {
		t.Fatal("control_response should be claimed")
	}
	got := controlQueuePayload(t, js, 0)
	if got["type"] != "elicitation_response" {
		t.Fatalf("payload type = %v, want elicitation_response", got["type"])
	}
	if got["session_id"] != sessionID.String() || got["request_id"] != "req-ask-1" {
		t.Fatalf("payload addressing wrong: %v", got)
	}
	js.mu.Lock()
	if subj := js.publishes[0].Subject; subj != ControlSubject(envID.String()) {
		t.Fatalf("subject = %q, want %q", subj, ControlSubject(envID.String()))
	}
	js.mu.Unlock()

	// 2. permission 回包（response 体 {behavior}）→ type=permission_response，
	// 原行为不变。
	permFrame := []byte(`{"type":"control_response","response":{"subtype":"success",` +
		`"request_id":"req-perm-1","response":{"behavior":"allow"}}}`)
	if !ingress.maybeRoutePermissionResponse(context.Background(), sessionID, &envID, permFrame) {
		t.Fatal("control_response should be claimed")
	}
	if got := controlQueuePayload(t, js, 1); got["type"] != "permission_response" {
		t.Fatalf("payload type = %v, want permission_response", got["type"])
	}

	// 3. chat 模式进程内分流不受影响：request_id 命中 pending map →
	// 进程内 Resolve，不投 control 队列（即使帧带 envID）。
	ch := center.Register("req-chat-1")
	chatFrame := []byte(`{"type":"control_response","response":{"subtype":"success",` +
		`"request_id":"req-chat-1","response":{"action":"decline"}}}`)
	if !ingress.maybeRoutePermissionResponse(context.Background(), sessionID, &envID, chatFrame) {
		t.Fatal("control_response should be claimed")
	}
	select {
	case ans := <-ch:
		if ans.Action != "decline" {
			t.Fatalf("in-process ans = %+v, want decline", ans)
		}
	default:
		t.Fatal("chat-mode elicitation not resolved in-process")
	}
	js.mu.Lock()
	if len(js.publishes) != 2 {
		t.Fatalf("chat-mode elicitation leaked to control queue: publishes = %d, want 2",
			len(js.publishes))
	}
	js.mu.Unlock()
}

// isElicitationResultBody 判别矩阵：体形状是分流唯一依据。
func TestIsElicitationResultBody(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want bool
	}{
		{"elicitation accept", `{"action":"accept","content":{"answer":"red"}}`, true},
		{"elicitation decline 无 content", `{"action":"decline"}`, true},
		{"permission allow", `{"behavior":"allow"}`, false},
		{"permission deny 带 message", `{"behavior":"deny","message":"no"}`, false},
		{"action+behavior 同时在 → permission 兜底", `{"action":"x","behavior":"allow"}`, false},
		{"空体（error subtype）→ permission 兜底", ``, false},
		{"非对象 → permission 兜底", `"not-an-object"`, false},
		{"两者都不在 → permission 兜底", `{"foo":1}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isElicitationResultBody([]byte(tc.raw)); got != tc.want {
				t.Errorf("isElicitationResultBody(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}
