// Package headless implements `biu --headless --json` mode.
//
// Reads a single prompt from --prompt or stdin; writes AG-UI compatible
// JSONL events to stdout. Designed to be spawned by GUI / CI / SDKs.
package headless

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/internal/client"
	"github.com/biumind/biumind/apps/cli/biu/pkg/biumindkit"
)

type Options struct {
	Provider client.Provider
	Model    string
	System   string
	Prompt   string // if empty, read from stdin
	JSON     bool   // when true, emit AG-UI JSONL events; otherwise plain text deltas

	// Agent, when non-nil, routes the prompt through the full agent
	// loop (tool calls, permissions, hooks, compact). Falls back to
	// the legacy chat-only path when nil so the existing
	// --headless flow keeps working without an API key configured
	// for engine mode.
	Agent *biumindkit.Agent
}

// Run executes one prompt and writes either plain text or AG-UI JSONL to w.
// Returns on stream end.
func Run(ctx context.Context, w io.Writer, opt Options) error {
	prompt := opt.Prompt
	if prompt == "" {
		raw, err := io.ReadAll(bufio.NewReader(os.Stdin))
		if err != nil {
			return err
		}
		prompt = string(raw)
	}
	if prompt == "" {
		return fmt.Errorf("headless: empty prompt")
	}

	threadID := newID()
	runID := newID()
	msgID := newID()

	if opt.JSON {
		emit(w, "RUN_STARTED", map[string]any{"threadId": threadID, "runId": runID})
		emit(w, "TEXT_MESSAGE_START", map[string]any{"messageId": msgID, "role": "assistant"})
	}

	// Engine path — route through the full SDK agent so tools fire.
	if opt.Agent != nil {
		return runEngine(ctx, w, opt, prompt, threadID, runID, msgID)
	}

	if opt.Provider == nil {
		return fmt.Errorf("headless: no provider configured")
	}
	frames, err := opt.Provider.ChatStream(ctx, client.ChatRequest{
		Model:     opt.Model,
		System:    opt.System,
		Messages:  []client.Message{{Role: "user", Content: prompt}},
		MaxTokens: 4096,
	})
	if err != nil {
		if opt.JSON {
			emit(w, "RUN_ERROR", map[string]any{"message": err.Error()})
		} else {
			fmt.Fprintln(w, "[error]", err)
		}
		return err
	}
	for f := range frames {
		switch f.Kind {
		case client.KindDelta:
			if opt.JSON {
				emit(w, "TEXT_MESSAGE_CONTENT", map[string]any{"messageId": msgID, "delta": f.Text})
			} else {
				_, _ = w.Write([]byte(f.Text))
			}
		case client.KindStop:
			// model stop reason; relayed via RUN_FINISHED in JSON mode
		case client.KindEnd:
			// stream end
		case client.KindError:
			if opt.JSON {
				emit(w, "RUN_ERROR", map[string]any{"message": f.Err.Error()})
			} else {
				fmt.Fprintln(w, "[stream error]", f.Err)
			}
			return f.Err
		}
	}
	if opt.JSON {
		emit(w, "TEXT_MESSAGE_END", map[string]any{"messageId": msgID})
		emit(w, "RUN_FINISHED", map[string]any{"threadId": threadID, "runId": runID})
	} else {
		fmt.Fprintln(w)
	}
	return nil
}

// runEngine routes prompt through the SDK agent and translates each
// SDK event into either AG-UI JSONL or plain text. Mirrors the legacy
// path's emit shape so existing consumers keep working with both.
func runEngine(
	ctx context.Context,
	w io.Writer,
	opt Options,
	prompt, threadID, runID, msgID string,
) error {
	for ev := range opt.Agent.Submit(ctx, prompt) {
		switch e := ev.(type) {
		case biumindkit.AssistantText:
			if opt.JSON {
				emit(w, "TEXT_MESSAGE_CONTENT", map[string]any{
					"messageId": msgID, "delta": e.Text,
				})
			} else {
				_, _ = w.Write([]byte(e.Text))
				_, _ = w.Write([]byte{'\n'})
			}
		case biumindkit.ToolStart:
			if opt.JSON {
				emit(w, "TOOL_CALL_START", map[string]any{
					"id": e.ID, "name": e.Name, "input": e.Input,
				})
			}
		case biumindkit.ToolResult:
			if opt.JSON {
				emit(w, "TOOL_CALL_END", map[string]any{
					"id": e.ID, "name": e.Name,
					"output":     e.Output,
					"is_error":   e.IsError,
					"elapsed_ms": e.Elapsed.Milliseconds(),
				})
			}
		case biumindkit.Error:
			if opt.JSON {
				emit(w, "RUN_ERROR", map[string]any{"message": e.Err.Error()})
			} else {
				fmt.Fprintln(w, "[error]", e.Err)
			}
			return e.Err
		case biumindkit.Done:
			if opt.JSON {
				emit(w, "TEXT_MESSAGE_END", map[string]any{"messageId": msgID})
				emit(w, "RUN_FINISHED", map[string]any{
					"threadId": threadID, "runId": runID,
					"stop_reason":   e.StopReason,
					"input_tokens":  e.InputTokens,
					"output_tokens": e.OutputTokens,
				})
			}
			return nil
		}
	}
	return nil
}

func emit(w io.Writer, eventType string, payload map[string]any) {
	body := map[string]any{
		"id":   newID(),
		"type": eventType,
		"ts":   time.Now().UTC().Format(time.RFC3339Nano),
	}
	for k, v := range payload {
		body[k] = v
	}
	out, _ := json.Marshal(body)
	_, _ = w.Write(out)
	_, _ = w.Write([]byte{'\n'})
}

func newID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
