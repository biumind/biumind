package agentplane

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	sdkproto "github.com/biumind/biumind/packages/go-sdk/biu/sdkproto/v1"
	chatpkg "github.com/biumind/biumind/services/brain/internal/chat"
	"github.com/google/uuid"
)

// fakeMsgStore 捕获 CreateMessage 调用,供断言。
type fakeMsgStore struct {
	mu   sync.Mutex
	msgs []chatpkg.CreateMessageInput
}

func (f *fakeMsgStore) CreateMessage(_ context.Context, in chatpkg.CreateMessageInput) (*chatpkg.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.msgs = append(f.msgs, in)
	return &chatpkg.Message{ID: uuid.New()}, nil
}

func frameBytes(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal frame: %v", err)
	}
	return b
}

// 累积多帧 streamlined_text,result 帧终止 → 落一条 assistant 轮(拼接全文)。
func TestTranscriptRecorder_AccumulatesAndPersistsAssistant(t *testing.T) {
	fake := &fakeMsgStore{}
	rec := NewTranscriptRecorder(fake, nil)
	sid := uuid.New()
	tid := uuid.New()
	uid := uuid.New()
	rec.Begin(sid, tid, uid, "claude-opus-4-8", nil)

	ctx := context.Background()
	rec.ObserveFrame(ctx, sid, frameBytes(t, &sdkproto.SDKStreamlinedText{
		Type: sdkproto.TypeStreamlinedTxt, Text: "当前目录有 ", SessionID: sid.String(),
	}))
	rec.ObserveFrame(ctx, sid, frameBytes(t, &sdkproto.SDKStreamlinedText{
		Type: sdkproto.TypeStreamlinedTxt, Text: "3 个文件。", SessionID: sid.String(),
	}))
	rec.ObserveFrame(ctx, sid, frameBytes(t, &sdkproto.SDKResultSuccess{
		Type: sdkproto.TypeResult, Subtype: "success", SessionID: sid.String(),
	}))

	if len(fake.msgs) != 1 {
		t.Fatalf("应落 1 条 assistant 轮, got %d", len(fake.msgs))
	}
	m := fake.msgs[0]
	if m.Role != chatpkg.RoleAssistant {
		t.Errorf("role = %q, want assistant", m.Role)
	}
	if m.Content != "当前目录有 3 个文件。" {
		t.Errorf("content = %q, want 拼接全文", m.Content)
	}
	if m.Status != chatpkg.StatusSuccess {
		t.Errorf("status = %q, want success", m.Status)
	}
	if m.ThreadID != tid || m.UserID != uid {
		t.Errorf("thread/user 元数据丢失")
	}
	if m.Model == nil || *m.Model != "claude-opus-4-8" {
		t.Errorf("model 丢失")
	}
}

// 未 Begin 的 session(无 thread)→ 帧被忽略,不落库。
func TestTranscriptRecorder_UnregisteredSessionIgnored(t *testing.T) {
	fake := &fakeMsgStore{}
	rec := NewTranscriptRecorder(fake, nil)
	sid := uuid.New()
	ctx := context.Background()
	rec.ObserveFrame(ctx, sid, frameBytes(t, &sdkproto.SDKStreamlinedText{
		Type: sdkproto.TypeStreamlinedTxt, Text: "hi", SessionID: sid.String(),
	}))
	rec.ObserveFrame(ctx, sid, frameBytes(t, &sdkproto.SDKResultSuccess{
		Type: sdkproto.TypeResult, Subtype: "success", SessionID: sid.String(),
	}))
	if len(fake.msgs) != 0 {
		t.Fatalf("未注册 session 不应落库, got %d", len(fake.msgs))
	}
}

// 失败终止且无文本(如 daemon 离线 fail 帧)→ 不落空 assistant 轮。
func TestTranscriptRecorder_ErrorWithoutTextNoPersist(t *testing.T) {
	fake := &fakeMsgStore{}
	rec := NewTranscriptRecorder(fake, nil)
	sid := uuid.New()
	rec.Begin(sid, uuid.New(), uuid.New(), "", nil)
	rec.ObserveFrame(context.Background(), sid, frameBytes(t, &sdkproto.SDKResultError{
		Type: sdkproto.TypeResult, Subtype: "error_during_execution", IsError: true,
		SessionID: sid.String(),
	}))
	if len(fake.msgs) != 0 {
		t.Fatalf("失败且无文本不应落库, got %d", len(fake.msgs))
	}
}

// 失败终止但有部分文本 → 落 assistant 轮, status=error。
func TestTranscriptRecorder_ErrorWithPartialTextPersistsAsError(t *testing.T) {
	fake := &fakeMsgStore{}
	rec := NewTranscriptRecorder(fake, nil)
	sid := uuid.New()
	rec.Begin(sid, uuid.New(), uuid.New(), "", nil)
	ctx := context.Background()
	rec.ObserveFrame(ctx, sid, frameBytes(t, &sdkproto.SDKStreamlinedText{
		Type: sdkproto.TypeStreamlinedTxt, Text: "部分输出", SessionID: sid.String(),
	}))
	rec.ObserveFrame(ctx, sid, frameBytes(t, &sdkproto.SDKResultError{
		Type: sdkproto.TypeResult, Subtype: "error_during_execution", IsError: true,
		SessionID: sid.String(),
	}))
	if len(fake.msgs) != 1 {
		t.Fatalf("有部分文本应落 1 条, got %d", len(fake.msgs))
	}
	if fake.msgs[0].Status != chatpkg.StatusError {
		t.Errorf("status = %q, want error", fake.msgs[0].Status)
	}
}

// formAnswerFrame 组一条 form_answer 终态帧（测试工具）。
func formAnswerFrame(t *testing.T, sessionID uuid.UUID, requestID, action string, multi bool, content map[string]any) []byte {
	t.Helper()
	return frameBytes(t, &sdkproto.SDKFormAnswer{
		Type:      sdkproto.TypeSystem,
		Subtype:   sdkproto.SubtypeFormAnswer,
		RequestID: requestID,
		Question:  "Pick a color?",
		Header:    "Color",
		Options: []sdkproto.SDKFormAnswerOption{
			{Label: "red", Description: "warm"},
			{Label: "blue", Description: "cool"},
		},
		Action:      action,
		MultiSelect: multi,
		Content:     content,
		UUID:        uuid.NewString(),
		SessionID:   sessionID.String(),
	})
}

// form_answer 四终态落库：accept（含 notes/多选摘要）/ decline / cancel /
// timeout 各落一行 role=assistant、status=success、client_id 幂等、
// parts 带 type=form 的可重建 payload。
func TestTranscriptRecorder_FormAnswerTerminalStates(t *testing.T) {
	cases := []struct {
		name        string
		action      string
		multi       bool
		content     map[string]any
		wantContent string
	}{
		{name: "accept single", action: "accept",
			content:     map[string]any{"answer": "blue"},
			wantContent: "Q: Pick a color?\nA: blue"},
		{name: "accept with notes", action: "accept",
			content:     map[string]any{"answer": "red", "notes": "warm tone"},
			wantContent: "Q: Pick a color?\nA: red\nNotes: warm tone"},
		{name: "accept multi", action: "accept", multi: true,
			content:     map[string]any{"answer": []any{"red", "blue"}},
			wantContent: "Q: Pick a color?\nA: red, blue"},
		{name: "decline", action: "decline",
			wantContent: "Q: Pick a color?\nA: (declined)"},
		{name: "cancel", action: "cancel",
			wantContent: "Q: Pick a color?\nA: (cancelled)"},
		{name: "timeout", action: "timeout",
			wantContent: "Q: Pick a color?\nA: (no answer — timed out)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeMsgStore{}
			rec := NewTranscriptRecorder(fake, nil)
			sid, tid, uid := uuid.New(), uuid.New(), uuid.New()
			rec.Begin(sid, tid, uid, "claude-opus-4-8", nil)
			rec.ObserveFrame(context.Background(), sid, formAnswerFrame(t, sid, "req-1", tc.action, tc.multi, tc.content))

			if len(fake.msgs) != 1 {
				t.Fatalf("应落 1 行, got %d", len(fake.msgs))
			}
			m := fake.msgs[0]
			if m.Role != chatpkg.RoleAssistant || m.Status != chatpkg.StatusSuccess {
				t.Errorf("role/status = %q/%q, want assistant/success", m.Role, m.Status)
			}
			if m.Content != tc.wantContent {
				t.Errorf("content = %q, want %q", m.Content, tc.wantContent)
			}
			if m.ClientID == nil || *m.ClientID != "elicit:req-1" {
				t.Errorf("client_id = %v, want elicit:req-1", m.ClientID)
			}
			if m.ThreadID != tid || m.UserID != uid {
				t.Error("thread/user 元数据丢失")
			}
			if m.Model == nil || *m.Model != "claude-opus-4-8" {
				t.Error("model 丢失")
			}
			var parts []map[string]any
			if err := json.Unmarshal(m.Parts, &parts); err != nil {
				t.Fatalf("parts unmarshal: %v", err)
			}
			if len(parts) != 1 || parts[0]["type"] != "form" {
				t.Fatalf("parts = %s, want [{type:form,...}]", m.Parts)
			}
			p := parts[0]
			if p["request_id"] != "req-1" || p["question"] != "Pick a color?" ||
				p["header"] != "Color" || p["action"] != tc.action {
				t.Errorf("part = %+v", p)
			}
			if p["multi_select"] != tc.multi {
				t.Errorf("multi_select = %v, want %v", p["multi_select"], tc.multi)
			}
			opts, _ := p["options"].([]any)
			if len(opts) != 2 {
				t.Errorf("options = %v, want 2 项快照", p["options"])
			}
			if tc.action == "accept" {
				if p["content"] == nil {
					t.Error("accept 的 part 应回显 content")
				}
			}
		})
	}
}

// 重放（重试/双发同一终态帧）：两次 CreateMessage 都带同一 client_id —
// store 层唯一约束 (thread_id, client_id) 去重返回既有行（store_test.go
// 已覆盖 DB 语义），这里锁定 recorder 的幂等键不随重放漂移。
func TestTranscriptRecorder_FormAnswerReplayIdempotentClientID(t *testing.T) {
	fake := &fakeMsgStore{}
	rec := NewTranscriptRecorder(fake, nil)
	sid := uuid.New()
	rec.Begin(sid, uuid.New(), uuid.New(), "", nil)
	frame := formAnswerFrame(t, sid, "req-dup", "accept", false, map[string]any{"answer": "red"})
	rec.ObserveFrame(context.Background(), sid, frame)
	rec.ObserveFrame(context.Background(), sid, frame)
	if len(fake.msgs) != 2 {
		t.Fatalf("recorder 不做内存去重（交给 store 唯一约束）, got %d calls", len(fake.msgs))
	}
	for i, m := range fake.msgs {
		if m.ClientID == nil || *m.ClientID != "elicit:req-dup" {
			t.Errorf("call %d client_id = %v, want elicit:req-dup", i, m.ClientID)
		}
	}
}

// 未注册 session（无 thread / turn 已终止清理后）的 form_answer 帧直接忽略。
func TestTranscriptRecorder_FormAnswerUnregisteredIgnored(t *testing.T) {
	fake := &fakeMsgStore{}
	rec := NewTranscriptRecorder(fake, nil)
	sid := uuid.New()
	rec.ObserveFrame(context.Background(), sid,
		formAnswerFrame(t, sid, "req-x", "accept", false, map[string]any{"answer": "red"}))
	if len(fake.msgs) != 0 {
		t.Fatalf("未注册 session 不应落库, got %d", len(fake.msgs))
	}
}
