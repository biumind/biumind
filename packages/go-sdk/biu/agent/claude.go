// ClaudeCode adapter — drives Anthropic's `claude` CLI.
//
// Invocation pattern:
//
//	claude -p "<prompt>" --output-format=stream-json --verbose
//	  [--continue]                  resume most recent session
//	  [--resume=<session_id>]       resume a specific session
//	  [--model=<sonnet|opus|…>]
//	  [--append-system-prompt=<…>]
//
// The CLI emits JSONL on stdout. Each line is one event with `type` ∈
// {"system", "assistant", "user", "result"}. We map them onto the
// canonical [Event] vocabulary documented in agent.go.
//
// Reference: https://docs.claude.com/en/docs/claude-code/cli-reference#json-output
package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"time"
)

type ClaudeCode struct {
	// Bin overrides the CLI binary path (default "claude").
	Bin string
}

func NewClaudeCode(bin string) *ClaudeCode {
	if bin == "" {
		bin = "claude"
	}
	return &ClaudeCode{Bin: bin}
}

func (c *ClaudeCode) Name() string { return "claude-code" }

func (c *ClaudeCode) Run(ctx context.Context, req Request) (<-chan Event, error) {
	if req.Prompt == "" {
		return nil, fmt.Errorf("agent: claude: missing prompt")
	}

	args := []string{"-p", req.Prompt, "--output-format=stream-json", "--verbose"}
	if req.SessionID != "" {
		args = append(args, "--resume", req.SessionID)
	} else if req.Continue {
		args = append(args, "--continue")
	}
	if req.Model != "" {
		args = append(args, "--model", req.Model)
	}
	if req.SystemPrompt != "" {
		args = append(args, "--append-system-prompt", req.SystemPrompt)
	}

	cmdCtx := ctx
	if req.TimeoutSec > 0 {
		var cancel context.CancelFunc
		cmdCtx, cancel = context.WithTimeout(ctx, time.Duration(req.TimeoutSec)*time.Second)
		_ = cancel // released when ctx cancels
	}
	cmd := exec.CommandContext(cmdCtx, c.Bin, args...)
	if req.Workdir != "" {
		cmd.Dir = req.Workdir
	}
	// 继承父进程环境 → 删 ClearEnv（A1：平台 key）→ merge req.Env（A2 注入）。
	// 不能只塞 req.Env：否则子进程裸环境丢 PATH/HOME，claude CLI 起不来 +
	// 找不到 ~/.claude 凭据。
	cmd.Env = childEnv(req.ClearEnv, req.Env)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = nil // dropped — adapter surfaces errors via EventError

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("agent: claude: start: %w", err)
	}

	out := make(chan Event, 16)
	go func() {
		defer close(out)
		// Panic isolation: a malformed stream-json frame must not crash the
		// host (agent-plane daemon shares the process with heartbeat/poll).
		defer func() {
			if r := recover(); r != nil {
				out <- Event{Type: EventError, Timestamp: time.Now().UTC(),
					Content: fmt.Sprintf("agent: claude: panic: %v", r)}
			}
		}()
		parseClaudeStream(stdout, out)
		// Wait closes stdout and reaps; ignore exit error here because
		// non-zero exits are surfaced by EventError frames upstream.
		_ = cmd.Wait()
	}()
	return out, nil
}

// parseClaudeStream consumes JSONL frames and emits canonical Events.
// Exposed for tests.
func parseClaudeStream(r io.Reader, out chan<- Event) {
	dec := json.NewDecoder(bufio.NewReader(r))
	for dec.More() {
		var raw map[string]any
		if err := dec.Decode(&raw); err != nil {
			out <- Event{
				Type:      EventError,
				Timestamp: time.Now().UTC(),
				Content:   "decode: " + err.Error(),
			}
			return
		}
		emitClaudeFrame(raw, out)
	}
}

func emitClaudeFrame(raw map[string]any, out chan<- Event) {
	now := time.Now().UTC()
	t, _ := raw["type"].(string)
	sid, _ := raw["session_id"].(string)

	switch t {
	case "system":
		out <- Event{Type: EventSystem, Timestamp: now, SessionID: sid, Payload: raw}

	case "assistant":
		// Assistant frames carry an Anthropic-style content array of
		// blocks. Each block is text | thinking | tool_use; emit one
		// canonical event per block so the renderer can interleave.
		msg, _ := raw["message"].(map[string]any)
		blocks, _ := msg["content"].([]any)
		for _, b := range blocks {
			block, ok := b.(map[string]any)
			if !ok {
				continue
			}
			switch block["type"] {
			case "text":
				txt, _ := block["text"].(string)
				out <- Event{Type: EventText, Timestamp: now, SessionID: sid, Content: txt}
			case "thinking":
				txt, _ := block["thinking"].(string)
				out <- Event{Type: EventThinking, Timestamp: now, SessionID: sid, Content: txt}
			case "tool_use":
				te := &ToolEvent{}
				te.ID, _ = block["id"].(string)
				te.Name, _ = block["name"].(string)
				te.Input, _ = block["input"].(map[string]any)
				out <- Event{Type: EventToolUse, Timestamp: now, SessionID: sid, Tool: te}
			default:
				out <- Event{Type: EventRaw, Timestamp: now, SessionID: sid, Payload: block}
			}
		}

	case "user":
		// User frames in stream-json mostly carry tool_result blocks
		// the agent injected back into its own context.
		msg, _ := raw["message"].(map[string]any)
		blocks, _ := msg["content"].([]any)
		for _, b := range blocks {
			block, ok := b.(map[string]any)
			if !ok {
				continue
			}
			if block["type"] == "tool_result" {
				te := &ToolEvent{}
				te.ID, _ = block["tool_use_id"].(string)
				if c, ok := block["content"].(string); ok {
					te.Output = c
				} else if cs, ok := block["content"].([]any); ok {
					// content is sometimes an array of {type:"text", text:…}
					var combined string
					for _, c := range cs {
						cm, _ := c.(map[string]any)
						if s, ok := cm["text"].(string); ok {
							combined += s
						}
					}
					te.Output = combined
				}
				if isErr, _ := block["is_error"].(bool); isErr {
					te.Error = te.Output
					te.Output = ""
				}
				out <- Event{Type: EventToolResult, Timestamp: now, SessionID: sid, Tool: te}
			} else {
				out <- Event{Type: EventRaw, Timestamp: now, SessionID: sid, Payload: block}
			}
		}

	case "result":
		out <- Event{Type: EventDone, Timestamp: now, SessionID: sid, Payload: raw}

	default:
		out <- Event{Type: EventRaw, Timestamp: now, SessionID: sid, Payload: raw}
	}
}
