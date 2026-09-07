// daemon agent 模式提问表单（user.ask / agent-ask-form P2-b）——
// biumindkit AskUserFn 的 daemon 实现：把引擎的一次提问转成
// control_request{subtype:elicitation, mode:form} 帧推到 session `.out`
// （brain ingress 透传到 client WS，FormCard 渲染作答），阻塞等 brain
// control 队列投回的 elicitation_response（handleControl 路由进来），
// 校验后映射回 biumindkit.UserAnswer。
//
// 链路（与 askPermissionFor 同构，见 worker.go）：
//
//	biumindkit 引擎 AskUserQuestion
//	  → Options.AskUser = w.askUserFor(sessionID) 组帧 + 注册 pendingAsks
//	  → publishFrame → brain `.out` → 客户端 FormCard 作答
//	  → 客户端回 control_response → brain ingress 按 request_id 分流到
//	    该 session 所在 environment 的 control 队列（type=elicitation_response）
//	  → worker.controlLoop → handleControl → answerAsk 唤醒等候的 goroutine
//
// requested_schema 形状与 brain chat 模式（chat_elicitation.go）逐字段对齐：
//
//	{
//	  "type": "object",
//	  "title": "<问题全文>",
//	  "properties": {
//	    "answer": 单选 {"type":"string","enum":[labels...]}
//	              多选 {"type":"array","items":{"type":"string","enum":[...]},"minItems":1},
//	    "notes":  {"type":"string"}
//	  },
//	  "required": ["answer"],
//	  "x-biumind-question": {   // 展示元数据（JSON Schema 表达不了 option 描述）
//	    "question": "...", "header": "...", "multi_select": false,
//	    "options": [{"label","description","preview"?}, ...]
//	  }
//	}
//
//	客户端回包 content：单选 {"answer": "<label>"} /
//	多选 {"answer": ["<label>", ...]}，均可选带 "notes": "<自由文本>"。
//
// 红线（设计 §3.4）：pendingAsks 只在内存，绝不从持久化状态重建；daemon
// 重启 = pending 丢失，未答表单按超时 soft error 收场。所有失败路径
// （发布失败 / 超时 / ctx 取消 / decline / cancel / 回包非法）都收口成
// Cancelled 或 error —— biumindkit 统一转成工具 soft error，模型自行降级，
// session 绝不死锁（设计 §2.3.3「丢帧也安全」）。

package agentplane

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/pkg/biumindkit"
	sdkproto "github.com/biumind/biumind/packages/go-sdk/biu/sdkproto/v1"
	"github.com/google/uuid"
)

// elicitationServerName 填 Elicitation.McpServerName（schema required
// 字段）。agent 提问不是 MCP server 发起，用固定占位串标识来源。
// 与 brain chat 模式同值，客户端不区分提问来自哪个进程。
const elicitationServerName = "biumind.agent"

// askUserTimeout 是单条 elicitation 等 client 回包的最大时长。对齐 brain
// chat 模式 ElicitationTimeout（5min）—— 比 permission 的 30s 长，表单要
// 人读题 + 作答（设计 §2.3.3）。var（非 const）以便测试缩小；生产不改。
var askUserTimeout = 5 * time.Minute

// elicitationAnswer 是客户端 ElicitationResponse 回包的进程内形态。
// Content 是自由 JSON（schema 校验在 answerFromElicitationContent 做，
// 那里拿得到提问的 option 集合 —— 设计 §3.3：必须服务端校验，防伪造回包）。
type elicitationAnswer struct {
	Action  string         // accept | decline | cancel
	Content map[string]any // accept 时的表单值
}

// pendingAskEntry 记录一条待答提问：ch 唤醒等候方（buffered 1，投递永不
// 阻塞），sessionID 用于 session 结束时按会话清理（cancelPendingAsks），
// 防止 turn 已结束而等候 goroutine 干等超时（goroutine 泄漏）。
type pendingAskEntry struct {
	sessionID uuid.UUID
	ch        chan elicitationAnswer
}

// askUserFor 返回一个绑定到指定 sessionID 的 biumindkit.AskUserFn。engine
// 触发 AskUserQuestion 时由 biumindkit 内部调用（replyToUserQuestion，
// 独立 goroutine，阻塞不影响事件 drain）：
//
//  1. 生成 request_id；注册 chan 到 pendingAsks map
//  2. 组 control_request{elicitation} 帧通过 publishFrame 推到 .out —
//     brain ingress 透传到 client WS
//  3. 阻塞等 chan（答复经 brain control queue → handleControl → answerAsk）
//     或 askUserTimeout 或 ctx 取消（turn 被 interrupt）
//  4. 超时 / decline / cancel → UserAnswer{Cancelled:true}；发布失败 /
//     回包非法 → error。两者经 biumindkit 都是工具 soft error。
func (w *Worker) askUserFor(sessionID uuid.UUID) biumindkit.AskUserFn {
	return func(ctx context.Context, q biumindkit.UserQuestion) (biumindkit.UserAnswer, error) {
		requestID := uuid.NewString()
		respCh := make(chan elicitationAnswer, 1)

		w.pendingAsksMu.Lock()
		w.pendingAsks[requestID] = pendingAskEntry{sessionID: sessionID, ch: respCh}
		w.pendingAsksMu.Unlock()
		defer func() {
			w.pendingAsksMu.Lock()
			delete(w.pendingAsks, requestID)
			w.pendingAsksMu.Unlock()
		}()

		schema, err := questionToRequestedSchema(q)
		if err != nil {
			return biumindkit.UserAnswer{}, fmt.Errorf("elicitation: build schema: %w", err)
		}
		frame := &sdkproto.SDKControlRequest{
			Type:      sdkproto.TypeControlRequest,
			RequestID: requestID,
			Request: &sdkproto.Elicitation{
				SubtypeF:        sdkproto.SubtypeElicitation,
				McpServerName:   elicitationServerName,
				Message:         q.Question,
				Mode:            "form",
				ElicitationID:   requestID,
				RequestedSchema: schema,
			},
		}

		w.logger.Debug("biu worker: askUser (elicitation)",
			"session_id", sessionID, "request_id", requestID,
			"header", q.Header, "options", len(q.Options), "multi", q.MultiSelect)
		// 推帧失败 → 直接出局。engine 在等结果不该卡住；用户重新触发即可。
		if err := w.publishFrame(ctx, sessionID, frame); err != nil {
			w.logger.Warn("askUser: publish frame failed",
				"err", err, "session_id", sessionID, "request_id", requestID)
			return biumindkit.UserAnswer{}, fmt.Errorf("elicitation: publish frame: %w", err)
		}

		timer := time.NewTimer(askUserTimeout)
		defer timer.Stop()
		select {
		case ans := <-respCh:
			return answerFromElicitationContent(q, ans)
		case <-ctx.Done():
			// turn 被 interrupt / 父 ctx 取消 —— 与 askPermissionFor 的 ctx
			// 分支同语义。
			return biumindkit.UserAnswer{}, ctx.Err()
		case <-timer.C:
			w.logger.Info("askUser: timed out (user unanswered)",
				"session_id", sessionID, "request_id", requestID)
			return biumindkit.UserAnswer{Cancelled: true}, nil
		}
	}
}

// answerAsk 把 brain 经 control queue 投回的 elicitation_response 投递给
// 等候的 askUserFor goroutine。找不到 request_id → 静默丢弃（可能已超时 /
// 不属于本进程 / 重复回包）。
func (w *Worker) answerAsk(requestID string, ans elicitationAnswer) {
	w.pendingAsksMu.Lock()
	entry, ok := w.pendingAsks[requestID]
	if ok {
		delete(w.pendingAsks, requestID)
	}
	w.pendingAsksMu.Unlock()
	if !ok {
		return
	}
	entry.ch <- ans // buffered(1)，不阻塞
}

// cancelPendingAsks 释放指定 session 的全部待答提问：给每条等候 chan 投
// 一个 cancel，让阻塞的 askUserFor goroutine 立刻以 Cancelled 出局，而不
// 是干等 askUserTimeout。handleWork 在 Submit channel 关闭（turn 结束）后
// defer 调用 —— session 结束 / 中断时防 goroutine 泄漏。
//
// daemon 重启则 pending 全部丢失（内存态，设计 §3.4 红线允许：绝不从持久
// 化状态重建）。
func (w *Worker) cancelPendingAsks(sessionID uuid.UUID) {
	w.pendingAsksMu.Lock()
	var released []chan elicitationAnswer
	for requestID, entry := range w.pendingAsks {
		if entry.sessionID == sessionID {
			released = append(released, entry.ch)
			delete(w.pendingAsks, requestID)
		}
	}
	w.pendingAsksMu.Unlock()
	for _, ch := range released {
		ch <- elicitationAnswer{Action: "cancel"} // buffered(1)，不阻塞
	}
	if len(released) > 0 {
		w.logger.Info("biu worker: cancelled pending asks on session end",
			"session_id", sessionID, "released", len(released))
	}
}

// decodeElicitationAnswer 把 brain 转发过来的 control_response body 解成
// elicitationAnswer。subtype=error 或解析失败一律 cancel —— 让等候方立刻
// soft error 出局，而不是干等超时（与 brain ingress 的解析失败兜底同策略）。
//
// elicitation_response schema（control queue payload，与 permission_response
// 同构）：response 字段是嵌套的 ElicitationResponse JSON
// { "action": "accept"|"decline"|"cancel", "content": {...} }。
func decodeElicitationAnswer(body controlBody) elicitationAnswer {
	if body.Subtype == sdkproto.ControlSubtypeError {
		return elicitationAnswer{Action: "cancel"}
	}
	if len(body.Response) == 0 {
		return elicitationAnswer{Action: "cancel"}
	}
	var result struct {
		Action  string         `json:"action"`
		Content map[string]any `json:"content"`
	}
	if err := json.Unmarshal(body.Response, &result); err != nil {
		return elicitationAnswer{Action: "cancel"}
	}
	return elicitationAnswer{Action: result.Action, Content: result.Content}
}

// questionToRequestedSchema 把一道引擎提问映射成 elicitation 的
// requested_schema（形状见文件头注释）。与 brain chat 模式
// （chat_elicitation.go::questionToRequestedSchema）逐字段对齐 —— 两边各
// 自实现（brain 与 cli 的 go.mod 互不依赖，不跨仓库边界引包），靠单测锁定
// 形状一致。
func questionToRequestedSchema(q biumindkit.UserQuestion) (json.RawMessage, error) {
	labels := make([]string, 0, len(q.Options))
	options := make([]map[string]any, 0, len(q.Options))
	for _, o := range q.Options {
		labels = append(labels, o.Label)
		opt := map[string]any{"label": o.Label, "description": o.Description}
		if o.Preview != "" {
			opt["preview"] = o.Preview
		}
		options = append(options, opt)
	}
	var answerProp map[string]any
	if q.MultiSelect {
		answerProp = map[string]any{
			"type":     "array",
			"items":    map[string]any{"type": "string", "enum": labels},
			"minItems": 1,
		}
	} else {
		answerProp = map[string]any{"type": "string", "enum": labels}
	}
	schema := map[string]any{
		"type":  "object",
		"title": q.Question,
		"properties": map[string]any{
			"answer": answerProp,
			"notes":  map[string]any{"type": "string"},
		},
		"required": []string{"answer"},
		"x-biumind-question": map[string]any{
			"question":     q.Question,
			"header":       q.Header,
			"multi_select": q.MultiSelect,
			"options":      options,
		},
	}
	raw, err := json.Marshal(schema)
	if err != nil {
		return nil, err
	}
	return raw, nil
}

// answerFromElicitationContent 校验客户端回包并映射成 UserAnswer（设计
// §3.3：content 是自由 JSON，必须服务端校验 —— accept 必须带 answer、选项
// 值在声明集合内、multiSelect 是非空数组且去重）。decline / cancel / 未知
// action → Cancelled。非法 content 返回 error（biumindkit 转成 Cancelled
// → 工具 soft error）。与 brain chat 模式 answerFromElicitation 同规则。
func answerFromElicitationContent(q biumindkit.UserQuestion, ans elicitationAnswer) (biumindkit.UserAnswer, error) {
	if ans.Action != "accept" {
		// decline / cancel / 未知 action 统一当用户未答处理。
		return biumindkit.UserAnswer{Cancelled: true}, nil
	}
	labelIndex := make(map[string]int, len(q.Options))
	for i, o := range q.Options {
		labelIndex[o.Label] = i
	}
	rawAnswer, ok := ans.Content["answer"]
	if !ok {
		return biumindkit.UserAnswer{}, fmt.Errorf("elicitation: content missing 'answer'")
	}
	out := biumindkit.UserAnswer{}
	if notes, _ := ans.Content["notes"].(string); notes != "" {
		out.Notes = notes
	}
	if q.MultiSelect {
		list, ok := rawAnswer.([]any)
		if !ok || len(list) == 0 {
			return biumindkit.UserAnswer{}, fmt.Errorf("elicitation: multi-select answer must be a non-empty array")
		}
		seen := map[int]bool{}
		for _, item := range list {
			label, ok := item.(string)
			if !ok {
				return biumindkit.UserAnswer{}, fmt.Errorf("elicitation: multi-select answer items must be strings")
			}
			idx, ok := labelIndex[label]
			if !ok {
				return biumindkit.UserAnswer{}, fmt.Errorf("elicitation: option %q not in declared set", label)
			}
			if !seen[idx] {
				seen[idx] = true
				out.Selected = append(out.Selected, idx)
			}
		}
		return out, nil
	}
	label, ok := rawAnswer.(string)
	if !ok {
		return biumindkit.UserAnswer{}, fmt.Errorf("elicitation: single-select answer must be a string")
	}
	idx, ok := labelIndex[label]
	if !ok {
		return biumindkit.UserAnswer{}, fmt.Errorf("elicitation: option %q not in declared set", label)
	}
	out.Selected = []int{idx}
	return out, nil
}
