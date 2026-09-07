// ElicitationObserver 集成测试（P3-c）：提问帧落 pending 行、form_answer
// 终态帧 CAS answered + 幂等补落 chat.messages form 行。

package agentplane

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	chatpkg "github.com/biumind/biumind/services/brain/internal/chat"
	"github.com/google/uuid"
)

// controlRequestElicitationFrame 组一帧 control_request{elicitation,
// mode:form}，形状与 chat_elicitation.go askUserFn / cli askUserFor 产出的
// 完全一致（requested_schema 带 x-biumind-question 元数据）。
func controlRequestElicitationFrame(t *testing.T, requestID string, question string) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"type":       "control_request",
		"request_id": requestID,
		"request": map[string]any{
			"subtype":         "elicitation",
			"mcp_server_name": "biumind.agent",
			"message":         question,
			"mode":            "form",
			"elicitation_id":  requestID,
			"requested_schema": map[string]any{
				"type":     "object",
				"title":    question,
				"required": []string{"answer"},
				"properties": map[string]any{
					"answer": map[string]any{"type": "string", "enum": []string{"A", "B"}},
				},
				"x-biumind-question": map[string]any{
					"question": question, "header": "Pick", "multi_select": false,
					"options": []map[string]any{{"label": "A", "description": "a"}, {"label": "B", "description": "b"}},
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func observerFormAnswerFrame(t *testing.T, requestID, sessionID, question, action string, content map[string]any) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"type": "system", "subtype": "form_answer",
		"request_id": requestID, "question": question, "header": "Pick",
		"multi_select": false,
		"options":      []map[string]any{{"label": "A"}, {"label": "B"}},
		"action":       action, "content": content,
		"uuid": uuid.NewString(), "session_id": sessionID,
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestElicitationObserver_ControlRequestInsertsPending(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()
	ctx := context.Background()
	sess := seedChatSession(t, h, "active")

	obs := NewElicitationObserver(h.store, nil, elicTestLogger)
	rid := uuid.NewString()
	obs.ObserveFrame(ctx, sess.SessionID, controlRequestElicitationFrame(t, rid, "选哪个?"))

	e, err := h.store.GetElicitation(ctx, uuid.MustParse(rid))
	if err != nil {
		t.Fatalf("pending row not inserted: %v", err)
	}
	if e.Status != "pending" || e.SessionID != sess.SessionID {
		t.Errorf("row=%+v", e)
	}
	var meta map[string]any
	if err := json.Unmarshal(e.Payload, &meta); err != nil || meta["question"] != "选哪个?" || meta["header"] != "Pick" {
		t.Errorf("payload=%s", e.Payload)
	}

	// 非 elicitation control_request / 无 x-biumind-question 的 elicitation
	// → 不落库
	other := []byte(`{"type":"control_request","request_id":"` + uuid.NewString() +
		`","request":{"subtype":"can_use_tool","tool_name":"Bash"}}`)
	obs.ObserveFrame(ctx, sess.SessionID, other)
	noMeta := []byte(`{"type":"control_request","request_id":"` + uuid.NewString() +
		`","request":{"subtype":"elicitation","mode":"url","requested_schema":{}}}`)
	obs.ObserveFrame(ctx, sess.SessionID, noMeta)
	rows, _ := h.store.ListPendingElicitations(ctx, sess.SessionID)
	if len(rows) != 1 {
		t.Errorf("pending=%d want 1 (non-form frames skipped)", len(rows))
	}
}

func TestElicitationObserver_FormAnswerCASAndPersistsChatRow(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()
	ctx := context.Background()
	sess := seedChatSession(t, h, "active")
	chatStore := chatpkg.New(h.pool)
	// form 行落 chat.messages 需要 thread 行（FK）
	if err := chatStore.EnsureThread(ctx, *sess.ThreadID, sess.UserID, "t", ""); err != nil {
		t.Fatal(err)
	}

	obs := NewElicitationObserver(h.store, chatStore, elicTestLogger)
	rid := uuid.NewString()
	obs.ObserveFrame(ctx, sess.SessionID, controlRequestElicitationFrame(t, rid, "选哪个?"))

	// 终态帧 → CAS answered + chat.messages form 行
	obs.ObserveFrame(ctx, sess.SessionID,
		observerFormAnswerFrame(t, rid, sess.SessionID.String(), "选哪个?", "accept", map[string]any{"answer": "A"}))

	e, err := h.store.GetElicitation(ctx, uuid.MustParse(rid))
	if err != nil || e.Status != "answered" {
		t.Fatalf("status=%q err=%v want answered", e.Status, err)
	}
	var ans map[string]any
	if err := json.Unmarshal(e.Answer, &ans); err != nil || ans["action"] != "accept" {
		t.Errorf("answer=%s", e.Answer)
	}

	// chat.messages form 行落库核验（role=assistant, client_id=elicit:<rid>）
	var count int
	var content string
	if err := h.pool.QueryRow(ctx, `
		SELECT count(*)::int, COALESCE(max(content), '') FROM chat.messages
		 WHERE thread_id = $1 AND client_id = $2
	`, *sess.ThreadID, "elicit:"+rid).Scan(&count, &content); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("form rows=%d want 1", count)
	}
	if want := "Q: 选哪个?"; !strings.Contains(content, want) {
		t.Errorf("content=%q want containing %q", content, want)
	}

	// 重放同一终态帧（daemon 重发 / ingress 迟到作答也补发了一帧）→
	// client_id 幂等，仍只有一行；CAS 幂等不报错。
	obs.ObserveFrame(ctx, sess.SessionID,
		observerFormAnswerFrame(t, rid, sess.SessionID.String(), "选哪个?", "accept", map[string]any{"answer": "A"}))
	if err := h.pool.QueryRow(ctx, `
		SELECT count(*)::int FROM chat.messages WHERE thread_id = $1 AND client_id = $2
	`, *sess.ThreadID, "elicit:"+rid).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("after replay rows=%d want 1", count)
	}
}

// TestElicitationObserver_ChainWithTranscript：ObserverChain 串接 transcript
// + elicitation 两个 observer，两帧各落各的表。
func TestElicitationObserver_ChainWithTranscript(t *testing.T) {
	h := newAPIHarness(t)
	defer h.close()
	ctx := context.Background()
	sess := seedChatSession(t, h, "active")
	chatStore := chatpkg.New(h.pool)
	if err := chatStore.EnsureThread(ctx, *sess.ThreadID, sess.UserID, "t", ""); err != nil {
		t.Fatal(err)
	}

	recorder := NewTranscriptRecorder(chatStore, elicTestLogger)
	recorder.Begin(sess.SessionID, *sess.ThreadID, sess.UserID, "m", nil)
	obs := NewElicitationObserver(h.store, chatStore, elicTestLogger)
	chain := ObserverChain{recorder, obs}

	rid := uuid.NewString()
	chain.ObserveFrame(ctx, sess.SessionID, controlRequestElicitationFrame(t, rid, "选哪个?"))
	chain.ObserveFrame(ctx, sess.SessionID,
		observerFormAnswerFrame(t, rid, sess.SessionID.String(), "选哪个?", "accept", map[string]any{"answer": "A"}))

	if _, err := h.store.GetElicitation(ctx, uuid.MustParse(rid)); err != nil {
		t.Errorf("elicitation row missing: %v", err)
	}
	var count int
	if err := h.pool.QueryRow(ctx, `
		SELECT count(*)::int FROM chat.messages WHERE thread_id = $1 AND client_id = $2
	`, *sess.ThreadID, "elicit:"+rid).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("form rows=%d want 1 (recorder+observer idempotent)", count)
	}
}
