// ElicitationObserver（P3-c durable resume）—— Queue.PublishSessionFrame
// 的 FrameObserver 链成员，把 elicitation 提问/终态落成 agent_elicitations
// 行（DB 为 SoT，ElicitationCenter 内存 map 只是 fast-path）。
//
// 为什么走 choke point 而不是 askUserFn 自己写库：chat 模式
// （chat_elicitation.go askUserFn → Queue.PublishSessionFrame）和 daemon
// 模式（cli worker publishFrame → POST /v1/agent/sessions/{id}/publish →
// worker_api.handlePublishFrame → 同一 Queue）的提问帧都过
// PublishSessionFrame 这一个点 —— daemon 不直连 DB 的问题由此解决，
// 两模式共用一份落库逻辑。
//
// 看到两类帧：
//   - control_request{subtype:elicitation}（提问）→ InsertElicitation 落
//     pending 行（同事务作废该 session 旧 pending 行）。payload 取
//     requested_schema 的 x-biumind-question（question/header/
//     multi_select/options），缺这坨元数据说明不是 AskUserQuestion 表单
//     → 跳过不落。
//   - system/form_answer（终态，P3-b）→ ResolveElicitationCAS 置 answered
//     + 幂等补落 chat.messages 的 form 行（client_id=elicit:<request_id>
//     撞唯一约束返回既有行 —— TranscriptRecorder 已落的活 loop 场景不会
//     双写；loop 已死（recorder 无 session 上下文）时这里兜底落库）。
//
// 热路径纪律同 TranscriptRecorder：每帧只做一次廉价 peek，非目标帧零 DB
// 交互；目标帧（人类提问/作答频率）的 DB 写失败只 Warn 不阻塞 publish。

package agentplane

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	sdkproto "github.com/biumind/biumind/packages/go-sdk/biu/sdkproto/v1"
	chatpkg "github.com/biumind/biumind/services/brain/internal/chat"
	"github.com/google/uuid"
)

// ElicitationObserver 把 elicitation 帧落成 agent_elicitations 行。
// store 必填；chat 可空（无 chat 持久化的 dev 环境退化为只落
// agent_elicitations，不补 chat.messages form 行）。
type ElicitationObserver struct {
	store  *Store
	chat   messageCreator
	logger *slog.Logger
}

func NewElicitationObserver(store *Store, chatStore messageCreator, logger *slog.Logger) *ElicitationObserver {
	if logger == nil {
		logger = slog.Default()
	}
	return &ElicitationObserver{store: store, chat: chatStore, logger: logger}
}

// elicitationFramePeek 是 control_request 帧的最小解析（只取落库字段）。
type elicitationFramePeek struct {
	Type      string `json:"type"`
	RequestID string `json:"request_id"`
	Request   *struct {
		Subtype         string          `json:"subtype"`
		Mode            string          `json:"mode"`
		RequestedSchema json.RawMessage `json:"requested_schema"`
	} `json:"request"`
}

// elicQuestionMeta 是 requested_schema["x-biumind-question"] 的形状
// （chat_elicitation.go questionToRequestedSchema 埋的展示元数据）。
type elicQuestionMeta struct {
	Question    string           `json:"question"`
	Header      string           `json:"header"`
	MultiSelect bool             `json:"multi_select"`
	Options     []map[string]any `json:"options"`
}

// ObserveFrame 实现 FrameObserver。
func (o *ElicitationObserver) ObserveFrame(ctx context.Context, sessionID uuid.UUID, payload []byte) {
	if o == nil || o.store == nil {
		return
	}
	var head framePeek // transcript.go 的轻量 peek：type + subtype
	if err := json.Unmarshal(payload, &head); err != nil {
		return
	}
	switch {
	case head.Type == sdkproto.TypeControlRequest:
		o.onControlRequest(ctx, sessionID, payload)
	case head.Type == sdkproto.TypeSystem && head.Subtype == sdkproto.SubtypeFormAnswer:
		o.onFormAnswer(ctx, sessionID, payload)
	}
}

// onControlRequest：提问帧 → 落 pending 行。
func (o *ElicitationObserver) onControlRequest(ctx context.Context, sessionID uuid.UUID, payload []byte) {
	var f elicitationFramePeek
	if err := json.Unmarshal(payload, &f); err != nil {
		return
	}
	if f.Request == nil || f.Request.Subtype != sdkproto.SubtypeElicitation {
		return
	}
	requestID, err := uuid.Parse(f.RequestID)
	if err != nil {
		o.logger.Warn("elicitation observer: bad request_id",
			"session_id", sessionID, "request_id", f.RequestID)
		return
	}
	// 只落带 x-biumind-question 元数据的 form 模式提问（AskUserQuestion）；
	// 其余 elicitation 形态（url 模式 / 无展示元数据）客户端无法重建表单，
	// 落了也没法 resume，跳过。
	var schema struct {
		Meta *elicQuestionMeta `json:"x-biumind-question"`
	}
	if len(f.Request.RequestedSchema) > 0 {
		_ = json.Unmarshal(f.Request.RequestedSchema, &schema)
	}
	if schema.Meta == nil || schema.Meta.Question == "" {
		return
	}
	metaRaw, err := json.Marshal(schema.Meta)
	if err != nil {
		return
	}
	if err := o.store.InsertElicitation(ctx, requestID, sessionID, metaRaw); err != nil {
		// ErrNotFound = session 行不存在（帧比 session 行先到属数据错乱）；
		// 其余是 DB 故障。都只 Warn —— 丢落库不阻塞提问本身（退化为 P3-c
		// 前行为：重启即丢）。
		o.logger.Warn("elicitation observer: insert failed",
			"session_id", sessionID, "request_id", requestID, "err", err)
	}
}

// onFormAnswer：终态帧 → CAS 置 answered + 幂等补落 chat.messages form 行。
func (o *ElicitationObserver) onFormAnswer(ctx context.Context, sessionID uuid.UUID, payload []byte) {
	var f sdkproto.SDKFormAnswer
	if err := json.Unmarshal(payload, &f); err != nil || f.RequestID == "" {
		return
	}
	requestID, err := uuid.Parse(f.RequestID)
	if err != nil {
		return
	}
	answer, err := json.Marshal(map[string]any{"action": f.Action, "content": f.Content})
	if err != nil {
		return
	}
	// CAS 置 answered。ok=false（无 pending 行：已 answered / 已 expired /
	// 提问落库失败 / P3-c 前的老提问）不阻塞后续 chat.messages 补落 ——
	// 有 elicitation 行才有 session 元数据可查。
	if _, _, _, err := o.store.ResolveElicitationCAS(ctx, requestID, answer); err != nil {
		o.logger.Warn("elicitation observer: CAS answer failed",
			"session_id", sessionID, "request_id", requestID, "err", err)
	}
	o.persistFormRow(ctx, requestID, &f)
}

// persistFormRow 把 form_answer 幂等落进 chat.messages（role=assistant、
// client_id=elicit:<request_id>）。与 TranscriptRecorder.persistFormAnswer
// 分工：recorder 只认本进程 Begin 注册过的活 loop session；loop 已死
// （brain 重启后迟到作答经 ingress 补发 form_answer 帧）时 recorder 静默
// 跳过，这里从 agent_elicitations 行取 session 元数据兜底落库。两边都走
// client_id 唯一约束幂等，谁先落都行，绝不双写。
func (o *ElicitationObserver) persistFormRow(ctx context.Context, requestID uuid.UUID, f *sdkproto.SDKFormAnswer) {
	if o.chat == nil {
		return
	}
	row, err := o.store.GetElicitation(ctx, requestID)
	if err != nil {
		if err != ErrNotFound {
			o.logger.Warn("elicitation observer: lookup for form row failed",
				"request_id", requestID, "err", err)
		}
		return // 无 elicitation 行 = 没有 thread/user 元数据，落不了
	}
	if row.ThreadID == nil || *row.ThreadID == uuid.Nil {
		return // 无 thread 的 session 不落 chat.messages（同 TranscriptRecorder）
	}
	parts, err := json.Marshal([]map[string]any{{
		"type":         "form",
		"request_id":   f.RequestID,
		"question":     f.Question,
		"header":       f.Header,
		"multi_select": f.MultiSelect,
		"options":      f.Options,
		"action":       f.Action,
		"content":      f.Content,
	}})
	if err != nil {
		return
	}
	in := chatpkg.CreateMessageInput{
		ThreadID: *row.ThreadID,
		UserID:   row.UserID,
		Role:     chatpkg.RoleAssistant,
		Content:  formAnswerSummary(f.Question, f.Action, f.Content),
		Parts:    parts,
		Status:   chatpkg.StatusSuccess,
	}
	cid := "elicit:" + f.RequestID
	in.ClientID = &cid
	if _, err := o.chat.CreateMessage(ctx, in); err != nil {
		o.logger.Warn("elicitation observer: persist form row failed",
			"request_id", requestID, "thread_id", *row.ThreadID, "err", err)
	}
}

// formAnswerFrameFromPayload 用 agent_elicitations.payload 快照 + 作答构一帧
// SDKFormAnswer（ingress 迟到作答路径补发用 —— 原生产者 loop 已死，没人会
// 再补这帧）。payload 缺字段时诚实留空，客户端按 action/content 渲染。
func formAnswerFrameFromPayload(sessionID, requestID uuid.UUID, payload []byte, action string, content map[string]any) ([]byte, error) {
	var meta elicQuestionMeta
	if len(payload) > 0 {
		_ = json.Unmarshal(payload, &meta)
	}
	options := make([]sdkproto.SDKFormAnswerOption, 0, len(meta.Options))
	for _, o := range meta.Options {
		opt := sdkproto.SDKFormAnswerOption{}
		opt.Label, _ = o["label"].(string)
		opt.Description, _ = o["description"].(string)
		options = append(options, opt)
	}
	raw, err := json.Marshal(&sdkproto.SDKFormAnswer{
		Type:        sdkproto.TypeSystem,
		Subtype:     sdkproto.SubtypeFormAnswer,
		RequestID:   requestID.String(),
		Question:    meta.Question,
		Header:      meta.Header,
		MultiSelect: meta.MultiSelect,
		Options:     options,
		Action:      action,
		Content:     content,
		UUID:        uuid.NewString(),
		SessionID:   sessionID.String(),
	})
	if err != nil {
		return nil, fmt.Errorf("marshal late form_answer: %w", err)
	}
	return raw, nil
}
