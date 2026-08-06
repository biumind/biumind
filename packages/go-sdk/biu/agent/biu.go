// Biu adapter — drives our own `biu` CLI in headless JSON mode.
//
// `biu --headless --json --prompt "<…>"` was already designed (P1.5) to
// emit AG-UI sealed events as JSONL on stdout, so this adapter is
// effectively just a thin AG-UI → canonical-Event translation layer.
//
// AG-UI events relevant here:
//
//	TEXT_MESSAGE_CONTENT      → EventText (chunked; we DON'T re-merge)
//	THINKING_TEXT_MESSAGE_*   → EventThinking
//	TOOL_CALL_START / ARGS    → EventToolUse (collapsed at END)
//	TOOL_CALL_END             → fires the EventToolUse with full input
//	biumind.tool_result       → EventToolResult (CUSTOM ext)
//	RUN_FINISHED              → EventDone
//	RUN_ERROR                 → EventError
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

type Biu struct {
	Bin string
	// Mode controls which biu mode flag is forwarded. Empty = adapter
	// default ("cloud" via model-relay).
	Mode string
}

func NewBiu(bin string) *Biu {
	if bin == "" {
		bin = "biu"
	}
	return &Biu{Bin: bin}
}

func (b *Biu) Name() string { return "biu" }

func (b *Biu) Run(ctx context.Context, req Request) (<-chan Event, error) {
	if req.Prompt == "" {
		return nil, fmt.Errorf("agent: biu: missing prompt")
	}
	args := []string{"--headless", "--json", "--prompt", req.Prompt}
	if req.Model != "" {
		args = append(args, "--model", req.Model)
	}
	if b.Mode != "" {
		args = append(args, "--mode", b.Mode)
	}
	if req.SessionID != "" {
		args = append(args, "--session", req.SessionID)
	}

	cmdCtx := ctx
	if req.TimeoutSec > 0 {
		var cancel context.CancelFunc
		cmdCtx, cancel = context.WithTimeout(ctx, time.Duration(req.TimeoutSec)*time.Second)
		_ = cancel
	}

	cmd := exec.CommandContext(cmdCtx, b.Bin, args...)
	if req.Workdir != "" {
		cmd.Dir = req.Workdir
	}
	for k, v := range req.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("agent: biu: start: %w", err)
	}

	out := make(chan Event, 16)
	go func() {
		defer close(out)
		parseBiuStream(stdout, out)
		_ = cmd.Wait()
	}()
	return out, nil
}

// parseBiuStream consumes AG-UI sealed-event JSONL and emits canonical
// Events. Tool calls are accumulated until TOOL_CALL_END so renderers
// don't see split args.
func parseBiuStream(r io.Reader, out chan<- Event) {
	type pendingTool struct {
		ID, Name string
		Args     string // accumulated arg stream
	}
	pending := map[string]*pendingTool{}

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
		now := time.Now().UTC()
		t, _ := raw["type"].(string)
		sid, _ := raw["thread_id"].(string)
		switch t {
		case "TEXT_MESSAGE_CONTENT":
			delta, _ := raw["delta"].(string)
			out <- Event{Type: EventText, Timestamp: now, SessionID: sid, Content: delta}
		case "THINKING_TEXT_MESSAGE_CONTENT":
			delta, _ := raw["delta"].(string)
			out <- Event{Type: EventThinking, Timestamp: now, SessionID: sid, Content: delta}
		case "TOOL_CALL_START":
			id, _ := raw["tool_call_id"].(string)
			name, _ := raw["tool_call_name"].(string)
			pending[id] = &pendingTool{ID: id, Name: name}
		case "TOOL_CALL_ARGS":
			id, _ := raw["tool_call_id"].(string)
			delta, _ := raw["delta"].(string)
			if pt, ok := pending[id]; ok {
				pt.Args += delta
			}
		case "TOOL_CALL_END":
			id, _ := raw["tool_call_id"].(string)
			pt, ok := pending[id]
			if !ok {
				out <- Event{Type: EventRaw, Timestamp: now, SessionID: sid, Payload: raw}
				continue
			}
			te := &ToolEvent{ID: pt.ID, Name: pt.Name}
			if pt.Args != "" {
				_ = json.Unmarshal([]byte(pt.Args), &te.Input)
			}
			delete(pending, id)
			out <- Event{Type: EventToolUse, Timestamp: now, SessionID: sid, Tool: te}
		case "biumind.tool_result":
			te := &ToolEvent{}
			te.ID, _ = raw["tool_call_id"].(string)
			te.Output, _ = raw["output"].(string)
			te.Error, _ = raw["error"].(string)
			out <- Event{Type: EventToolResult, Timestamp: now, SessionID: sid, Tool: te}
		case "RUN_FINISHED":
			out <- Event{Type: EventDone, Timestamp: now, SessionID: sid, Payload: raw}
		case "RUN_ERROR":
			msg, _ := raw["message"].(string)
			out <- Event{Type: EventError, Timestamp: now, SessionID: sid, Content: msg, Payload: raw}
		default:
			out <- Event{Type: EventRaw, Timestamp: now, SessionID: sid, Payload: raw}
		}
	}
}
