// LLM stream parser — converts a raw provider stream into the
// engine.Event types defined in events.go.
//
// Anthropic Messages API streaming protocol (the one we target first):
//
//   message_start          → record model + usage.input_tokens
//   content_block_start    → begin text or tool_use block
//   content_block_delta    → either text delta or input_json_delta
//   content_block_stop     → seal the block
//   message_delta          → updates stop_reason + usage.output_tokens
//   message_stop           → end of stream
//
// We accumulate until message_stop, then emit a single
// AssistantMessageEvent with the assembled state.Message (text +
// tool_use blocks). StreamTokenEvent fires per text delta so the UI
// gets typing animation. tool_use input arrives as JSON fragments
// across many input_json_delta events — we buffer per block and
// parse once content_block_stop sees the full string.
//
// OpenAI-compatible providers use a different shape (tool_calls array
// in choices[0].delta). The provider adapter (client/openai.go in a
// future phase) is responsible for normalising into the same Anthropic
// shape before handing the stream to ParseStream. Keeps this file
// LLM-vendor-agnostic.

package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/internal/state"
)

// StreamFrame is one decoded SSE event from an Anthropic-shaped
// stream. The provider adapter produces a channel of these; ParseStream
// consumes that channel and emits engine.Events into the user-facing
// channel.
//
// We intentionally don't share types with provider adapters — having
// our own keeps this layer testable without spinning a real LLM.
type StreamFrame struct {
	Type StreamFrameType `json:"type"`

	// type=message_start
	Message *StreamMessageHead `json:"message,omitempty"`

	// type=content_block_start
	Index        int              `json:"index,omitempty"`
	ContentBlock *StreamBlockHead `json:"content_block,omitempty"`

	// type=content_block_delta / message_delta
	Delta *StreamDelta `json:"delta,omitempty"`

	// type=message_delta also carries usage at end of stream
	Usage *StreamUsage `json:"usage,omitempty"`

	// type=error (provider-emitted error frame)
	Error *StreamError `json:"error,omitempty"`
}

type StreamFrameType string

const (
	FrameMessageStart      StreamFrameType = "message_start"
	FrameContentBlockStart StreamFrameType = "content_block_start"
	FrameContentBlockDelta StreamFrameType = "content_block_delta"
	FrameContentBlockStop  StreamFrameType = "content_block_stop"
	FrameMessageDelta      StreamFrameType = "message_delta"
	FrameMessageStop       StreamFrameType = "message_stop"
	FramePing              StreamFrameType = "ping"
	FrameError             StreamFrameType = "error"
)

type StreamMessageHead struct {
	ID    string       `json:"id"`
	Model string       `json:"model"`
	Usage *StreamUsage `json:"usage,omitempty"` // input_tokens here
}

type StreamBlockHead struct {
	Type  string         `json:"type"` // text | tool_use
	ID    string         `json:"id,omitempty"`
	Name  string         `json:"name,omitempty"`
	Input map[string]any `json:"input,omitempty"` // anthropic gives {} initially
}

type StreamDelta struct {
	// content_block_delta
	Type        string `json:"type"` // text_delta | input_json_delta | thinking_delta
	Text        string `json:"text,omitempty"`
	PartialJSON string `json:"partial_json,omitempty"`
	// Thinking — content_block_delta type=thinking_delta 的载体, 出现在
	// Anthropic 原生 extended thinking 以及 model-relay 把开源推理模型
	// (glm / r1 / qwen-r1) 翻译过来的 thinking 块。
	Thinking string `json:"thinking,omitempty"`

	// message_delta
	StopReason   string `json:"stop_reason,omitempty"`
	StopSequence string `json:"stop_sequence,omitempty"`
}

type StreamUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
}

type StreamError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// ─── Parser ────────────────────────────────────────────

// ParseStream consumes frames from `frames` and emits Events into
// `out`. Returns the final state.Message + stop_reason + per-call
// usage. The caller is responsible for closing `out` (we only write).
//
// Cancellation: when ctx is canceled, ParseStream stops reading
// frames and returns ctx.Err. Any partial assistant message is still
// returned so the caller can decide whether to record it.
func ParseStream(
	ctx context.Context,
	frames <-chan StreamFrame,
	out chan<- Event,
) (msg state.Message, stopReason string, usage StreamUsage, err error) {
	msg = state.Message{
		Role:      state.RoleAssistant,
		Content:   nil,
		CreatedAt: time.Now().UTC(),
	}

	// Per-block accumulators keyed by index. Anthropic increments
	// `index` for each content block within a single message. We use
	// it to route deltas to the right buffer.
	type blockAcc struct {
		kind      string // "text" | "tool_use"
		text      strings.Builder
		toolID    string
		toolName  string
		toolInput strings.Builder // accumulates partial_json fragments
	}
	blocks := map[int]*blockAcc{}

	done := ctx.Done()

	for {
		select {
		case <-done:
			return msg, stopReason, usage, ctx.Err()
		case f, ok := <-frames:
			if !ok {
				// Provider closed without message_stop — we trust
				// what we have and return.
				return msg, stopReason, usage, nil
			}
			switch f.Type {
			case FrameMessageStart:
				if f.Message != nil {
					msg.Model = f.Message.Model
					msg.ID = f.Message.ID
					if f.Message.Usage != nil {
						usage.InputTokens = f.Message.Usage.InputTokens
						usage.CacheReadInputTokens = f.Message.Usage.CacheReadInputTokens
						usage.CacheCreationInputTokens = f.Message.Usage.CacheCreationInputTokens
					}
				}

			case FrameContentBlockStart:
				if f.ContentBlock == nil {
					continue
				}
				blocks[f.Index] = &blockAcc{
					kind:     f.ContentBlock.Type,
					toolID:   f.ContentBlock.ID,
					toolName: f.ContentBlock.Name,
				}
				// thinking 块开启时同步 emit `<think>\n` 文本 token,
				// 让端侧 reasoning_parser 在 streaming 中识别推理段
				// (无需 SDK 协议新增 ThinkingBlock 即可拉齐显示)。
				// 完整说明见 FrameContentBlockStop 的 thinking 分支。
				if f.ContentBlock.Type == "thinking" {
					SafeSend(out, &StreamTokenEvent{Text: "<think>\n"}, done)
				}

			case FrameContentBlockDelta:
				b := blocks[f.Index]
				if b == nil || f.Delta == nil {
					continue
				}
				switch f.Delta.Type {
				case "text_delta":
					b.text.WriteString(f.Delta.Text)
					SafeSend(out, &StreamTokenEvent{Text: f.Delta.Text}, done)
				case "input_json_delta":
					b.toolInput.WriteString(f.Delta.PartialJSON)
				case "thinking_delta":
					// 推理内容当 token 流出, 但不进 b.text — 进 b.text 的
					// 内容会被 ContentBlockStop 写入 msg.Content, 进而进入
					// 下一轮 history 喂给 LLM。`<think>` 标签喂回 LLM 多数
					// 模型不接受。所以 streaming 走 token 让端侧渲染,
					// 持久化绕开。
					SafeSend(out, &StreamTokenEvent{Text: f.Delta.Thinking}, done)
				}

			case FrameContentBlockStop:
				b := blocks[f.Index]
				if b == nil {
					continue
				}
				switch b.kind {
				case "text":
					msg.Content = append(msg.Content, state.ContentBlock{
						Type: state.ContentText,
						Text: b.text.String(),
					})
				case "thinking":
					// 推理段结束 — emit 闭合标签让端侧 reasoning_parser
					// 看到 `</think>` 切回 text 段渲染。
					// 不写 msg.Content: 推理内容留在端侧 streaming buffer,
					// 后端 history 不带推理过程 (符合 Anthropic 标准:
					// thinking 块不参与下轮请求 unless redacted_thinking)。
					SafeSend(out, &StreamTokenEvent{Text: "</think>\n\n"}, done)
				case "tool_use":
					var input map[string]any
					raw := strings.TrimSpace(b.toolInput.String())
					if raw == "" {
						input = map[string]any{}
					} else if err := json.Unmarshal([]byte(raw), &input); err != nil {
						// Malformed JSON from the LLM. Surface as an
						// error event and skip the block — engine will
						// retry the turn.
						SafeSend(out, &ErrorEvent{
							Err:         fmt.Errorf("tool_use input json: %w (raw=%q)", err, raw),
							Source:      ErrSrcLLM,
							Recoverable: true,
						}, done)
						continue
					}
					msg.Content = append(msg.Content, state.ContentBlock{
						Type:         state.ContentToolUse,
						ToolUseID:    b.toolID,
						ToolUseName:  b.toolName,
						ToolUseInput: input,
					})
				}
				delete(blocks, f.Index)

			case FrameMessageDelta:
				if f.Delta != nil && f.Delta.StopReason != "" {
					stopReason = f.Delta.StopReason
					msg.StopReason = stopReason
				}
				if f.Usage != nil {
					usage.OutputTokens = f.Usage.OutputTokens
					// OpenAI-compat adapters deliver full usage (prompt +
					// completion) only in the terminal message_delta, not in
					// message_start. Fall back to the delta-carried input
					// tokens so accounting / autocompact stay correct.
					// Anthropic never carries input tokens here, so a no-op
					// for the Anthropic path.
					if f.Usage.InputTokens > 0 {
						usage.InputTokens = f.Usage.InputTokens
					}
				}

			case FrameMessageStop:
				msg.UsageInput = usage.InputTokens
				msg.UsageOutput = usage.OutputTokens
				SafeSend(out, &StreamUsageEvent{
					InputTokens:       usage.InputTokens,
					OutputTokens:      usage.OutputTokens,
					CacheReadTokens:   usage.CacheReadInputTokens,
					CacheCreateTokens: usage.CacheCreationInputTokens,
				}, done)
				return msg, stopReason, usage, nil

			case FramePing:
				// keep-alive; ignore.

			case FrameError:
				if f.Error != nil {
					return msg, stopReason, usage,
						fmt.Errorf("provider stream error: %s", f.Error.Message)
				}
			}
		}
	}
}
