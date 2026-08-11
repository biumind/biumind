// Anthropic-compatible SSE writer.
//
// model-relay 的 /v1/messages 默认输出 unified frame SSE
// （event: delta / tool_call_start / tool_call_args / tool_call_end / stop / end）
// —— 给 brain / 老 admin UI / hub.go RelayProvider 用。
//
// 但 biumindkit 的 NewRelayEngine 复用了 AnthropicEngineProvider 的 SSE
// parser，期望 Anthropic 原生 SSE 格式。两边契约不一致 → biumindkit 收
// 的 stop_reason 永远空，报 "unhandled stop_reason \"\""。
//
// 这里实现 Frame → Anthropic 原生 SSE 的翻译，让客户端通过 header
// `X-Stream-Format: anthropic` 或 query `?stream_format=anthropic` 选用。
//
// Anthropic SSE 规范：https://docs.anthropic.com/en/api/messages-streaming
//
//   event: message_start    data: {type, message:{id,role,model,...,usage}}
//   event: content_block_start  data: {type, index, content_block:{type:text|tool_use,...}}
//   event: content_block_delta  data: {type, index, delta:{type:text_delta,text}|{input_json_delta,partial_json}}
//   event: content_block_stop   data: {type, index}
//   event: message_delta    data: {type, delta:{stop_reason}, usage:{output_tokens}}
//   event: message_stop     data: {type}
//
// 一些细节：
//   - text 块和 tool_use 块共享 index 计数器（按出现顺序递增）
//   - 一个 turn 里 text 和 tool_use 可以交叉，但通常 text 先（model-relay
//     openai adapter 输出顺序保持，跟上游 OpenAI 一致）
//   - finish_reason 翻译：stop → end_turn, tool_calls → tool_use, length → max_tokens

package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"

	"github.com/biumind/biumind/services/model-relay/internal/relay/provider"
)

// streamAsAnthropic 把 unified Frame 翻译成 Anthropic 原生 SSE 写到 w。
// model 是 client 请求里的 model code（透回响应给 client 让它知道）。
func streamAsAnthropic(
	w http.ResponseWriter,
	flusher http.Flusher,
	frames <-chan provider.StreamFrame,
	model string,
) (provider.Usage, bool, string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	msgID := "msg_" + randHex(12)

	// message_start
	writeAnthropicSSE(w, flusher, "message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":            msgID,
			"type":          "message",
			"role":          "assistant",
			"model":         model,
			"content":       []any{},
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage": map[string]any{
				"input_tokens":  0,
				"output_tokens": 0,
			},
		},
	})

	// 状态机：跟踪 block 序号、当前 block 类型、是否已开 text block
	var (
		nextIdx  = 0
		textOpen = false
		textIdx  = 0
		// tool block 状态：toolID → block index（用于 ArgsDelta 命中正确 index）
		toolBlocks   = map[string]int{}
		thinkingOpen = false
		thinkingIdx  = 0
		usage        provider.Usage
		stopReason   = ""
		errMsg       = ""
		hasError     = false
		// stopSeen tracks whether the provider emitted a terminal
		// FrameStop (openai finish_reason / anthropic message_delta stop).
		// A stream that closes without one was truncated mid-flight (the
		// openai gateway occasionally drops before finish_reason). We do
		// NOT emit an Anthropic error frame for truncation — the engine
		// self-heals the resulting partial tool_use via malformed-input
		// recovery — but we report success=false so billing/usage reflect
		// the truth instead of masquerading truncation as a clean end_turn.
		stopSeen = false
	)

	openText := func() {
		if textOpen {
			return
		}
		// thinking 进行中先 close
		if thinkingOpen {
			writeAnthropicSSE(w, flusher, "content_block_stop", map[string]any{
				"type": "content_block_stop", "index": thinkingIdx,
			})
			thinkingOpen = false
		}
		textIdx = nextIdx
		nextIdx++
		writeAnthropicSSE(w, flusher, "content_block_start", map[string]any{
			"type":  "content_block_start",
			"index": textIdx,
			"content_block": map[string]any{
				"type": "text", "text": "",
			},
		})
		textOpen = true
	}
	closeText := func() {
		if !textOpen {
			return
		}
		writeAnthropicSSE(w, flusher, "content_block_stop", map[string]any{
			"type": "content_block_stop", "index": textIdx,
		})
		textOpen = false
	}
	openThinking := func() {
		if thinkingOpen {
			return
		}
		closeText()
		thinkingIdx = nextIdx
		nextIdx++
		writeAnthropicSSE(w, flusher, "content_block_start", map[string]any{
			"type":  "content_block_start",
			"index": thinkingIdx,
			"content_block": map[string]any{
				"type": "thinking", "thinking": "",
			},
		})
		thinkingOpen = true
	}

	for f := range frames {
		switch f.Type {
		case provider.FrameDelta:
			openText()
			writeAnthropicSSE(w, flusher, "content_block_delta", map[string]any{
				"type":  "content_block_delta",
				"index": textIdx,
				"delta": map[string]any{
					"type": "text_delta", "text": f.Delta,
				},
			})
		case provider.FrameThinking:
			openThinking()
			writeAnthropicSSE(w, flusher, "content_block_delta", map[string]any{
				"type":  "content_block_delta",
				"index": thinkingIdx,
				"delta": map[string]any{
					"type": "thinking_delta", "thinking": f.Delta,
				},
			})
		case provider.FrameToolCallStart:
			closeText()
			if thinkingOpen {
				writeAnthropicSSE(w, flusher, "content_block_stop", map[string]any{
					"type": "content_block_stop", "index": thinkingIdx,
				})
				thinkingOpen = false
			}
			idx := nextIdx
			nextIdx++
			toolBlocks[f.ToolCall.ID] = idx
			writeAnthropicSSE(w, flusher, "content_block_start", map[string]any{
				"type":  "content_block_start",
				"index": idx,
				"content_block": map[string]any{
					"type":  "tool_use",
					"id":    f.ToolCall.ID,
					"name":  f.ToolCall.Name,
					"input": map[string]any{},
				},
			})
		case provider.FrameToolCallArgs:
			idx, ok := toolBlocks[f.ToolCall.ID]
			if !ok {
				continue
			}
			writeAnthropicSSE(w, flusher, "content_block_delta", map[string]any{
				"type":  "content_block_delta",
				"index": idx,
				"delta": map[string]any{
					"type":         "input_json_delta",
					"partial_json": f.ToolCall.ArgsDelta,
				},
			})
		case provider.FrameToolCallEnd:
			idx, ok := toolBlocks[f.ToolCall.ID]
			if !ok {
				continue
			}
			writeAnthropicSSE(w, flusher, "content_block_stop", map[string]any{
				"type": "content_block_stop", "index": idx,
			})
			delete(toolBlocks, f.ToolCall.ID)
		case provider.FrameUsage:
			if f.Usage != nil {
				usage = *f.Usage
			}
		case provider.FrameStop:
			stopSeen = true
			stopReason = mapStopReason(f.Stop, len(toolBlocks) > 0)
		case provider.FrameError:
			hasError = true
			errMsg = f.Err.Error()
			closeText()
			if thinkingOpen {
				writeAnthropicSSE(w, flusher, "content_block_stop", map[string]any{
					"type": "content_block_stop", "index": thinkingIdx,
				})
				thinkingOpen = false
			}
			for id, idx := range toolBlocks {
				writeAnthropicSSE(w, flusher, "content_block_stop", map[string]any{
					"type": "content_block_stop", "index": idx,
				})
				delete(toolBlocks, id)
			}
			writeAnthropicSSE(w, flusher, "error", map[string]any{
				"type": "error",
				"error": map[string]any{
					"type":    "api_error",
					"message": errMsg,
				},
			})
			return usage, false, "stream_error"
		}
	}

	// 收尾：close 任何还开着的 block
	closeText()
	if thinkingOpen {
		writeAnthropicSSE(w, flusher, "content_block_stop", map[string]any{
			"type": "content_block_stop", "index": thinkingIdx,
		})
	}
	for id, idx := range toolBlocks {
		writeAnthropicSSE(w, flusher, "content_block_stop", map[string]any{
			"type": "content_block_stop", "index": idx,
		})
		delete(toolBlocks, id)
	}

	// 没收到 FrameStop 时兜底（OpenAI gateway 偶尔流断在 finish_reason 之前）
	if stopReason == "" {
		stopReason = "end_turn"
	}

	writeAnthropicSSE(w, flusher, "message_delta", map[string]any{
		"type": "message_delta",
		"delta": map[string]any{
			"stop_reason":   stopReason,
			"stop_sequence": nil,
		},
		"usage": map[string]any{
			"output_tokens": usage.CompletionTokens,
		},
	})
	writeAnthropicSSE(w, flusher, "message_stop", map[string]any{
		"type": "message_stop",
	})
	// success requires a clean terminal frame. A stream that closed
	// without FrameStop was truncated — report it honestly (billing/
	// usage) even though the wire above masquerades as end_turn so the
	// engine's malformed-input self-heal can recover the turn.
	if !stopSeen {
		return usage, false, "truncated"
	}
	return usage, !hasError, ""
}

// mapStopReason 把 OpenAI / 其他 provider 的 finish_reason 翻成 Anthropic
// stop_reason。空 / 未知值兜底成 end_turn 让客户端能继续。
//
// hasOpenTool: stream 结束时还有 tool_use block 没 close 说明上游 finish
// 实际是 tool_calls，但 finish_reason 报错了或没报。
func mapStopReason(s string, hasOpenTool bool) string {
	if hasOpenTool {
		return "tool_use"
	}
	switch s {
	case "stop", "end_turn", "":
		return "end_turn"
	case "tool_calls", "tool_use":
		return "tool_use"
	case "length", "max_tokens":
		return "max_tokens"
	case "content_filter", "safety":
		return "stop_sequence"
	default:
		return "end_turn"
	}
}

func writeAnthropicSSE(w http.ResponseWriter, flusher http.Flusher, event string, data map[string]any) {
	b, err := json.Marshal(data)
	if err != nil {
		return
	}
	_, _ = w.Write([]byte("event: " + event + "\ndata: "))
	_, _ = w.Write(b)
	_, _ = w.Write([]byte("\n\n"))
	flusher.Flush()
}

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// 兜底：极不可能；返定值保证不空 id
		return "00000000000000000000"
	}
	return hex.EncodeToString(b)
}
