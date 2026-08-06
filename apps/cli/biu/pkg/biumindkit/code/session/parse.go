package session

import "encoding/json"

// parseClaudeLine 解析一行 Claude 会话 JSONL,返回与 Dart AgentEvent.fromJson 同形的
// 事件 map(可能多个:一条 assistant 消息含多个 content block)。无关行(system /
// attachment / file-history-snapshot / permission-mode 等元数据)返回 nil。
//
// 真实格式(claude 2.x,实测):
//
//	{"type":"assistant","timestamp":"...","message":{"content":[
//	    {"type":"thinking","thinking":"..."},
//	    {"type":"text","text":"..."},
//	    {"type":"tool_use","id":"toolu_..","name":"Read","input":{..}}],
//	  "stop_reason":"tool_use","usage":{"input_tokens":..,"output_tokens":..}}}
//	{"type":"user","timestamp":"...","message":{"content":[
//	    {"type":"tool_result","tool_use_id":"toolu_..","content":..,"is_error":false}]}}
//
// 注:thinking 块 M3 暂不渲染(终端已可见;结构化视图先保持干净,后续可加 Thinking
// 事件)。task_finished 不在此发 —— 任务终止由 PTY 退出(code_pty_exit)驱动,避免双发。
// cost_update 仅带 token(USD 需 pricing 查询,超出本包职责,total_usd=0)。
func parseClaudeLine(raw []byte) []map[string]any {
	var line struct {
		Type      string          `json:"type"`
		Timestamp string          `json:"timestamp"`
		Message   json.RawMessage `json:"message"`
	}
	if err := json.Unmarshal(raw, &line); err != nil {
		return nil
	}
	switch line.Type {
	case "assistant":
		return parseClaudeAssistant(line.Message, line.Timestamp)
	case "user":
		return parseClaudeUser(line.Message, line.Timestamp)
	default:
		return nil
	}
}

type claudeMessage struct {
	Content []json.RawMessage `json:"content"`
	Usage   *struct {
		InputTokens         int `json:"input_tokens"`
		OutputTokens        int `json:"output_tokens"`
		CacheCreationTokens int `json:"cache_creation_input_tokens"`
		CacheReadTokens     int `json:"cache_read_input_tokens"`
	} `json:"usage"`
}

func parseClaudeAssistant(msg json.RawMessage, ts string) []map[string]any {
	var m claudeMessage
	if err := json.Unmarshal(msg, &m); err != nil {
		return nil
	}
	var out []map[string]any
	for _, block := range m.Content {
		var b struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		}
		if json.Unmarshal(block, &b) != nil {
			continue
		}
		switch b.Type {
		case "text":
			if b.Text != "" {
				out = append(out, evText(ts, b.Text))
			}
		case "tool_use":
			out = append(out, evToolStart(ts, b.ID, b.Name, b.Input))
		}
		// thinking 块刻意跳过(见包注释)。
	}
	if m.Usage != nil && (m.Usage.InputTokens > 0 || m.Usage.OutputTokens > 0) {
		// context_tokens = 本轮实际喂给模型的上下文 = input + 两类 cache(命中的
		// cache 仍占上下文窗口)。Claude JSONL 不报 model_context_window,留 0,
		// 端侧按模型推断或不显示百分比。
		ctx := m.Usage.InputTokens + m.Usage.CacheCreationTokens + m.Usage.CacheReadTokens
		out = append(out, evCost(ts, costParts{
			in:          m.Usage.InputTokens,
			out:         m.Usage.OutputTokens,
			cacheCreate: m.Usage.CacheCreationTokens,
			cacheRead:   m.Usage.CacheReadTokens,
			ctxTokens:   ctx,
			ctxWindow:   0,
		}))
	}
	return out
}

func parseClaudeUser(msg json.RawMessage, ts string) []map[string]any {
	var m claudeMessage
	if err := json.Unmarshal(msg, &m); err != nil {
		return nil
	}
	var out []map[string]any
	for _, block := range m.Content {
		var b struct {
			Type      string          `json:"type"`
			ToolUseID string          `json:"tool_use_id"`
			Content   json.RawMessage `json:"content"`
			IsError   bool            `json:"is_error"`
		}
		if json.Unmarshal(block, &b) != nil {
			continue
		}
		if b.Type != "tool_result" {
			continue
		}
		out = append(out, evToolResult(ts, b.ToolUseID, flattenToolResult(b.Content), b.IsError))
	}
	return out
}

// flattenToolResult 把 tool_result 的 content 拍平成字符串:可能是 string,也可能是
// [{type:text,text:..}, {type:image,..}] 数组。
func flattenToolResult(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var arr []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &arr) == nil {
		var sb []byte
		for i, b := range arr {
			if i > 0 {
				sb = append(sb, '\n')
			}
			switch b.Type {
			case "text":
				sb = append(sb, b.Text...)
			case "image":
				sb = append(sb, "[image]"...)
			}
		}
		return string(sb)
	}
	return string(raw)
}

// ── AgentEvent JSON 构造器(键名对齐 Dart AgentEvent.fromJson)──────────────

func evText(ts, text string) map[string]any {
	return map[string]any{"type": "text_delta", "ts": ts, "text": text}
}

func evToolStart(ts, id, name string, input json.RawMessage) map[string]any {
	var args any
	if len(input) > 0 {
		_ = json.Unmarshal(input, &args)
	}
	if args == nil {
		args = map[string]any{}
	}
	return map[string]any{"type": "tool_use_start", "ts": ts, "tool_id": id, "name": name, "args": args}
}

func evToolResult(ts, id, result string, isErr bool) map[string]any {
	return map[string]any{"type": "tool_use_result", "ts": ts, "tool_id": id, "result": result, "is_error": isErr}
}

// costParts 携带一次 cost_update 的全量计数。total_usd 仍为 0(USD 需 pricing
// 查询,超出本包职责)。context_tokens/context_window 让端侧算上下文利用率。
type costParts struct {
	in, out, cacheCreate, cacheRead, ctxTokens, ctxWindow int
}

func evCost(ts string, p costParts) map[string]any {
	return map[string]any{
		"type": "cost_update", "ts": ts, "total_usd": 0,
		"input_tokens": p.in, "output_tokens": p.out,
		"cache_creation_tokens": p.cacheCreate, "cache_read_tokens": p.cacheRead,
		"context_tokens": p.ctxTokens, "context_window": p.ctxWindow,
	}
}
