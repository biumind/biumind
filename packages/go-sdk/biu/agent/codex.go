// Codex adapter — drives OpenAI's `codex` CLI in JSON mode.
//
// Expected invocation:
//
//	codex exec --json [--model=…] [--cd=workdir] "<prompt>"
//
// Output is JSONL where each line is one of:
//
//	{"type":"agent_message","content":"…"}
//	{"type":"agent_thinking","content":"…"}
//	{"type":"shell_command","command":["bash","-c","ls"]}
//	{"type":"shell_output","output":"…","exit_code":0}
//	{"type":"task_complete","reason":"…"}
//	{"type":"error","message":"…"}
//
// (Codex's exact wire format is a moving target; the adapter is defensive
// — anything we don't recognize falls through as EventRaw and is still
// captured in the session JSONL for replay.)
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

type Codex struct {
	Bin string
}

func NewCodex(bin string) *Codex {
	if bin == "" {
		bin = "codex"
	}
	return &Codex{Bin: bin}
}

func (c *Codex) Name() string { return "codex" }

func (c *Codex) Run(ctx context.Context, req Request) (<-chan Event, error) {
	if req.Prompt == "" {
		return nil, fmt.Errorf("agent: codex: missing prompt")
	}

	args := []string{"exec", "--json"}
	if req.Model != "" {
		args = append(args, "--model", req.Model)
	}
	if req.Workdir != "" {
		args = append(args, "--cd", req.Workdir)
	}
	args = append(args, req.Prompt)

	cmdCtx := ctx
	if req.TimeoutSec > 0 {
		var cancel context.CancelFunc
		cmdCtx, cancel = context.WithTimeout(ctx, time.Duration(req.TimeoutSec)*time.Second)
		_ = cancel
	}

	cmd := exec.CommandContext(cmdCtx, c.Bin, args...)
	if req.Workdir != "" {
		cmd.Dir = req.Workdir
	}
	// 同 claude.go：继承父环境 → 删 ClearEnv → merge req.Env（修裸环境 bug）。
	cmd.Env = childEnv(req.ClearEnv, req.Env)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("agent: codex: start: %w", err)
	}

	out := make(chan Event, 16)
	go func() {
		defer close(out)
		// Panic isolation: a malformed codex frame must not crash the host
		// (agent-plane daemon shares the process with heartbeat/poll loops).
		defer func() {
			if r := recover(); r != nil {
				out <- Event{Type: EventError, Timestamp: time.Now().UTC(),
					Content: fmt.Sprintf("agent: codex: panic: %v", r)}
			}
		}()
		parseCodexStream(stdout, out)
		_ = cmd.Wait()
	}()
	return out, nil
}

func parseCodexStream(r io.Reader, out chan<- Event) {
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
		emitCodexFrame(raw, out)
	}
}

func emitCodexFrame(raw map[string]any, out chan<- Event) {
	now := time.Now().UTC()
	t, _ := raw["type"].(string)
	sid, _ := raw["session_id"].(string)

	switch t {
	case "agent_message":
		txt, _ := raw["content"].(string)
		out <- Event{Type: EventText, Timestamp: now, SessionID: sid, Content: txt}
	case "agent_thinking":
		txt, _ := raw["content"].(string)
		out <- Event{Type: EventThinking, Timestamp: now, SessionID: sid, Content: txt}
	case "shell_command":
		cmd := ""
		if cs, ok := raw["command"].([]any); ok {
			for i, c := range cs {
				if i > 0 {
					cmd += " "
				}
				if s, ok := c.(string); ok {
					cmd += s
				}
			}
		} else if s, ok := raw["command"].(string); ok {
			cmd = s
		}
		out <- Event{Type: EventCommand, Timestamp: now, SessionID: sid, Content: cmd}
	case "shell_output":
		te := &ToolEvent{Name: "shell"}
		te.Output, _ = raw["output"].(string)
		if ec, ok := raw["exit_code"].(float64); ok && ec != 0 {
			te.Error = fmt.Sprintf("exit %d", int(ec))
		}
		out <- Event{Type: EventToolResult, Timestamp: now, SessionID: sid, Tool: te}
	case "task_complete", "result":
		out <- Event{Type: EventDone, Timestamp: now, SessionID: sid, Payload: raw}
	case "error":
		msg, _ := raw["message"].(string)
		out <- Event{Type: EventError, Timestamp: now, SessionID: sid, Content: msg, Payload: raw}
	default:
		out <- Event{Type: EventRaw, Timestamp: now, SessionID: sid, Payload: raw}
	}
}
