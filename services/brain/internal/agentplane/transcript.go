package agentplane

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"

	sdkproto "github.com/biumind/biumind/packages/go-sdk/biu/sdkproto/v1"
	chatpkg "github.com/biumind/biumind/services/brain/internal/chat"
	"github.com/google/uuid"
)

// TranscriptRecorder 把对话 assistant 轮落库到 chat.messages,使 brain 成为
// 对话历史的真相源(Runtime v3 §8.2 翻案)。
//
// 工作原理:chat / agent 两模式的出站帧都经 Queue.PublishSessionFrame 这一个
// choke point(chat 经 FrameEmitter,agent 经 worker_api.handlePublishFrame
// → 同一 Queue)。Recorder 注册为 FrameObserver:
//   - 路由层(createChat/AgentSession)落完 user 轮后调 Begin 注册 session
//     的 (threadID,userID,model);
//   - 每帧 streamlined_text 累积 assistant 文本;
//   - result 帧(成功/失败)= turn 终止 → 落 assistant 轮 + 清理 buffer。
//
// 不查 DB(Begin 已带齐元数据),热路径只做内存累积 + 终止时一次 INSERT。
// 未注册的 session(无 thread / dev 无 chatStore)直接忽略,退化为今天行为。
type TranscriptRecorder struct {
	chat   messageCreator
	logger *slog.Logger

	mu   sync.Mutex
	sess map[uuid.UUID]*transcriptSession
}

// messageCreator 是 TranscriptRecorder 对 chat.Store 的最小依赖(便于单测注入
// fake)。*chatpkg.Store 天然满足。
type messageCreator interface {
	CreateMessage(ctx context.Context, in chatpkg.CreateMessageInput) (*chatpkg.Message, error)
}

type transcriptSession struct {
	threadID       uuid.UUID
	userID         uuid.UUID
	model          string
	assistantMsgID uuid.UUID // client 透传（方案3）；Nil → 落库走 gen_random_uuid
	buf            strings.Builder
}

// NewTranscriptRecorder 构造。chatStore 可空(dev / 无 chat 持久化)→ 整个
// recorder 退化为 no-op。
func NewTranscriptRecorder(chatStore messageCreator, logger *slog.Logger) *TranscriptRecorder {
	return &TranscriptRecorder{
		chat:   chatStore,
		logger: logger,
		sess:   make(map[uuid.UUID]*transcriptSession),
	}
}

// Begin 注册一个会话的 assistant 轮上下文。路由层在落完 user 轮、确有 thread
// 时调用。threadID==Nil 的会话(无 thread)不应调 Begin → 该会话帧被忽略。
func (t *TranscriptRecorder) Begin(sessionID, threadID, userID uuid.UUID, model string, assistantMsgID *uuid.UUID) {
	if t == nil || t.chat == nil || threadID == uuid.Nil {
		return
	}
	var amID uuid.UUID
	if assistantMsgID != nil {
		amID = *assistantMsgID
	}
	t.mu.Lock()
	t.sess[sessionID] = &transcriptSession{threadID: threadID, userID: userID, model: model, assistantMsgID: amID}
	t.mu.Unlock()
}

// 轻量帧 peek —— 只取落库所需字段,避免依赖完整 sdkproto 解码。
type framePeek struct {
	Type       string  `json:"type"`
	Subtype    string  `json:"subtype"`
	Text       string  `json:"text"`
	Result     string  `json:"result"`
	StopReason *string `json:"stop_reason"`
}

// ObserveFrame 实现 FrameObserver,由 Queue.PublishSessionFrame 旁路调用。
func (t *TranscriptRecorder) ObserveFrame(ctx context.Context, sessionID uuid.UUID, payload []byte) {
	if t == nil || t.chat == nil {
		return
	}
	var f framePeek
	if err := json.Unmarshal(payload, &f); err != nil {
		return
	}
	switch f.Type {
	case sdkproto.TypeStreamlinedTxt: // "streamlined_text" 增量文本(chat FrameEmitter + daemon sdkbridge 同款)
		if f.Text == "" {
			return
		}
		t.mu.Lock()
		if sc := t.sess[sessionID]; sc != nil {
			sc.buf.WriteString(f.Text)
		}
		t.mu.Unlock()
	case sdkproto.TypeResult: // "result" turn 终止(success/error)
		t.finish(ctx, sessionID, f)
	}
}

// finish 在 result 帧(turn 终止)时把累积的 assistant 文本落库 + 清理。
func (t *TranscriptRecorder) finish(ctx context.Context, sessionID uuid.UUID, f framePeek) {
	t.mu.Lock()
	sc := t.sess[sessionID]
	delete(t.sess, sessionID)
	t.mu.Unlock()
	if sc == nil {
		return // 未注册(无 thread / 已落过)
	}

	content := sc.buf.String()
	if content == "" && f.Result != "" {
		content = f.Result
	}
	// 失败终止(subtype=error_* / is_error)且无文本(如 daemon 离线 fail 帧)
	// → 不落空 assistant 轮(用户没收到任何内容,历史里不该出现空助手轮)。
	isErr := strings.HasPrefix(f.Subtype, "error")
	if content == "" {
		return
	}

	status := chatpkg.StatusSuccess
	if isErr {
		status = chatpkg.StatusError
	}
	in := chatpkg.CreateMessageInput{
		ThreadID: sc.threadID,
		UserID:   sc.userID,
		Role:     chatpkg.RoleAssistant,
		Content:  content,
		Status:   status,
	}
	// 方案3：client 透传的 assistant message id（Nil 时 store 走 gen_random_uuid）。
	if sc.assistantMsgID != uuid.Nil {
		id := sc.assistantMsgID
		in.ID = &id
	}
	if sc.model != "" {
		in.Model = &sc.model
	}
	// client_id 幂等:同一 session 的 assistant 轮只落一次(result 帧若重发)。
	cid := sessionID.String() + ":assistant"
	in.ClientID = &cid
	if _, err := t.chat.CreateMessage(ctx, in); err != nil && t.logger != nil {
		t.logger.Warn("transcript: persist assistant turn failed",
			"session_id", sessionID, "thread_id", sc.threadID, "err", err)
	}
}
