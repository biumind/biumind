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
