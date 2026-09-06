// Chat 模式 agent 提问表单（user.ask / agent-ask-form P1-b）——
// biumindkit AskUserFn 的 brain 实现：把引擎的一次提问转成
// control_request{subtype:elicitation, mode:form} 帧推到客户端，
// 阻塞等 ElicitationCenter 里的回包（ingress 路由进来），校验后映射回
// biumindkit.UserAnswer。
//
// 协议形状（schema/sdk/v1/control/elicitation.json 的 form 模式）：
//
//	requested_schema 是一个 JSON Schema 风格的表单描述：
//	{
//	  "type": "object",
//	  "title": "<问题全文>",
//	  "properties": {
//	    "answer": 单选 {"type":"string","enum":[labels...]}
//	              多选 {"type":"array","items":{"type":"string","enum":[...]},"minItems":1}
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
// 所有失败路径（发布失败 / 超时 / ctx 取消 / decline / cancel / 回包非法）
// 都收口成「工具 soft error」：模型看到 "user cancelled/unanswered" 自行
// 降级，session 绝不死锁（设计 §2.3.3「丢帧也安全」）。

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
const elicitationServerName = "biumind.agent"

// askUserFn 返回绑定到指定 chat session 的 biumindkit.AskUserFn。
// Elicitations 为 nil（未注入 / dev 无 NATS）时不该被调用 —— 调用方
// （runSessionImpl）据此决定要不要把 AskUser 透传给 chat.RunSingleTurn。
func (cr *ChatRunner) askUserFn(sessionID uuid.UUID) biumindkit.AskUserFn {
	return func(ctx context.Context, q biumindkit.UserQuestion) (biumindkit.UserAnswer, error) {
		requestID := uuid.NewString()
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
		raw, err := json.Marshal(frame)
		if err != nil {
			return biumindkit.UserAnswer{}, fmt.Errorf("elicitation: marshal frame: %w", err)
		}

		answers := cr.Elicitations.Register(requestID)
		defer cr.Elicitations.Cancel(requestID)

		cr.Logger.Debug("chat runner: ask user (elicitation)",
			"session_id", sessionID, "request_id", requestID,
			"header", q.Header, "options", len(q.Options), "multi", q.MultiSelect)
		if err := cr.Queue.PublishSessionFrame(ctx, sessionID, raw); err != nil {
			return biumindkit.UserAnswer{}, fmt.Errorf("elicitation: publish frame: %w", err)
		}

		timer := time.NewTimer(ElicitationTimeout)
		defer timer.Stop()
		select {
		case ans := <-answers:
			return answerFromElicitation(q, ans)
		case <-ctx.Done():
			// turn 被 interrupt / 父 ctx 取消 —— 跟 daemon askPermission
			// 的 ctx 分支同语义。
			return biumindkit.UserAnswer{}, ctx.Err()
		case <-timer.C:
			cr.Logger.Info("chat runner: elicitation timed out (user unanswered)",
				"session_id", sessionID, "request_id", requestID)
			return biumindkit.UserAnswer{}, fmt.Errorf("elicitation: user unanswered within %s", ElicitationTimeout)
		}
	}
}

// questionToRequestedSchema 把一道引擎提问映射成 elicitation 的
// requested_schema（见文件头注释的形状定义）。
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

// answerFromElicitation 校验客户端回包并映射成 UserAnswer（设计 §3.3：
// content 是自由 JSON，必须服务端校验 —— 每题有答、选项值在声明集合内、
// multiSelect 是数组形态）。decline / cancel / 未知 action → Cancelled。
// 非法 content 返回 error（biumindkit 转成 Cancelled → 工具 soft error）。
func answerFromElicitation(q biumindkit.UserQuestion, ans ElicitationAnswer) (biumindkit.UserAnswer, error) {
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
