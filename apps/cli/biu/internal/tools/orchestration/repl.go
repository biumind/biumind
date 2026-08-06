// REPLTool — execute a sequence of primitive tool calls in one
// model → agent → model round-trip.
//
// The win is API round-trip avoidance — N primitive calls inside one
// REPL block is one round-trip instead of N. The model passes a JSON
// array of `{tool: "Read", input: {...}}` objects, the tool dispatches
// each through the same engine registry the model would have called
// individually, and the concatenated results come back in one
// tool_result.
//
// Gating: when REPL mode is on (env `BIU_REPL=1` or
// `BIU_REPL_MODE=1`), the primitive tools listed in REPLOnlyTools
// get hidden from the LLM-visible catalog (the engine's catalog
// builder honours the gate via REPLPrimitiveTools()). The model
// MUST batch them through REPL — saving cost on multi-step file
// reads or coordinated edits.
//
// Without REPL mode, the tool is still registered (callers can opt
// in per-call) but primitives stay directly callable as before.

package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
	"github.com/biumind/biumind/apps/cli/biu/internal/state"
)

// REPLToolName is the name the model uses to invoke this tool.
const REPLToolName = "REPL"

// MaxBatchSize caps how many primitive calls a single REPL block
// can include. Above this we'd compound enough error / latency in
// one call to hurt rather than help; the cost of a runaway batch is
// meaningful, so biu caps it.
const MaxBatchSize = 16

// REPLOnlyTools lists primitive tools that REPL mode hides from
// direct catalog exposure. Changing this list is a behavioural
// contract — keep in sync with what the engine's catalog filter
// consults.
var REPLOnlyTools = []string{
	"Read", "Write", "Edit", "MultiEdit",
	"Glob", "Grep",
	"Bash",
	"NotebookEdit",
	"Agent",
	// Lowercase aliases biu accepts (engine.SimpleRegistry
	// double-registers some tools under both names).
	"read", "write", "edit", "glob", "grep", "bash",
}

// IsREPLModeEnabled reports whether the engine should hide primitives
// from the LLM and require REPL-batching. Two env switches: `BIU_REPL`
// (on by default for ant-style automation runners) and `BIU_REPL_MODE`
// (the imperative override).
//
// Defaults off — interactive REPL users want direct tool calls.
// SDK / headless callers opt in via env when they want batched
// execution to lower API spend.
func IsREPLModeEnabled() bool {
	if envFalsy(os.Getenv("BIU_REPL")) {
		return false
	}
	if envTruthy(os.Getenv("BIU_REPL_MODE")) {
		return true
	}
	return envTruthy(os.Getenv("BIU_REPL"))
}

// REPLPrimitiveTools returns the set of tool names REPL mode
// suppresses. Engine catalog builders check this when REPL mode is
// on to filter the tool list shown to the model.
func REPLPrimitiveTools() map[string]bool {
	out := make(map[string]bool, len(REPLOnlyTools))
	for _, n := range REPLOnlyTools {
		out[n] = true
	}
	return out
}

// REPLTool is the engine-facing tool registration.
type REPLTool struct {
	// Registry is the live tool registry the batch dispatches into.
	// Keeping it on the struct rather than reaching for a global
	// makes test fixtures trivial — pass a stub registry.
	Registry engine.ToolRegistry
}

func (REPLTool) Name() string { return REPLToolName }

func (REPLTool) Description(_ map[string]any) string {
	return "Execute a sequence of primitive tool calls in one round-trip. " +
		"Pass `calls`: an array of `{tool, input}` objects (≤ 16). Each call " +
		"runs through the same dispatcher as a direct invocation, in order. " +
		"Use this when you'd otherwise emit several Read/Edit/Bash calls back to back — " +
		"REPL collapses them into one model→agent→model round trip, saving cost. " +
		"Errors in any single call are reported with index + tool name; subsequent " +
		"calls still run unless `stop_on_error: true`."
}

func (REPLTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"calls": map[string]any{
				"type":        "array",
				"description": "Sequence of primitive calls to execute in order. Maximum 16.",
				"minItems":    1,
				"maxItems":    MaxBatchSize,
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"tool": map[string]any{
							"type":        "string",
							"description": "Primitive tool name (Read / Write / Edit / Glob / Grep / Bash / NotebookEdit / Agent / MultiEdit).",
						},
						"input": map[string]any{
							"type":        "object",
							"description": "Tool-specific input — same shape the primitive's direct invocation would take.",
						},
					},
					"required": []string{"tool", "input"},
				},
			},
			"stop_on_error": map[string]any{
				"type":        "boolean",
				"description": "When true, abort the batch on the first error. Default: false (continue, report all).",
			},
		},
		"required": []string{"calls"},
	}
}

func (REPLTool) IsReadOnly(_ map[string]any) bool { return false } // depends on the calls inside
func (REPLTool) IsDestructive(in map[string]any) bool {
	// Conservative: any batch that includes a Write/Edit/Bash/etc
	// inherits destructiveness. The engine permission gate
	// inspects this; permission for the batch matches the most
	// destructive call inside.
	calls, _ := in["calls"].([]any)
	for _, c := range calls {
		m, _ := c.(map[string]any)
		name, _ := m["tool"].(string)
		switch name {
		case "Write", "Edit", "MultiEdit", "Bash", "NotebookEdit",
			"write", "edit", "bash":
			return true
		}
	}
	return false
}
func (REPLTool) IsConcurrencySafe(_ map[string]any) bool { return false }
func (REPLTool) InterruptBehavior() string               { return "complete" }

// Call dispatches each requested primitive through the live
// registry and concatenates the results.
func (r REPLTool) Call(ctx context.Context, input map[string]any, env *engine.ToolEnv) (*engine.ToolResultPayload, error) {
	if r.Registry == nil {
		return softErrR("REPL: no tool registry wired (engine misconfigured)"), nil
	}
	rawCalls, ok := input["calls"].([]any)
	if !ok {
		return softErrR("REPL: `calls` array required"), nil
	}
	if len(rawCalls) == 0 {
		return softErrR("REPL: at least one call required"), nil
	}
	if len(rawCalls) > MaxBatchSize {
		return softErrR(fmt.Sprintf("REPL: batch too large (%d > %d)", len(rawCalls), MaxBatchSize)), nil
	}
	stopOnError, _ := input["stop_on_error"].(bool)

	results := make([]replResult, 0, len(rawCalls))

	for i, raw := range rawCalls {
		m, ok := raw.(map[string]any)
		if !ok {
			results = append(results, replResult{Index: i,
				Err: "call entry must be an object {tool, input}"})
			if stopOnError {
				break
			}
			continue
		}
		toolName, _ := m["tool"].(string)
		toolInput, _ := m["input"].(map[string]any)
		if toolName == "" {
			results = append(results, replResult{Index: i,
				Err: "missing tool name"})
			if stopOnError {
				break
			}
			continue
		}
		tool, ok := r.Registry.Get(toolName)
		if !ok {
			results = append(results, replResult{Index: i, Tool: toolName,
				Err: "unknown tool"})
			if stopOnError {
				break
			}
			continue
		}
		out, err := tool.Call(ctx, toolInput, env)
		entry := replResult{Index: i, Tool: toolName}
		if err != nil {
			entry.Err = err.Error()
		} else if out != nil {
			if out.IsError {
				entry.Err = "tool reported soft error: " + extractText(out)
			} else {
				entry.Body = extractText(out)
			}
		}
		results = append(results, entry)
		if stopOnError && entry.Err != "" {
			break
		}
	}

	return text(formatBatchResults(results, len(rawCalls))), nil
}

// replResult is one per-call entry in a REPL batch report.
type replResult struct {
	Index int
	Tool  string
	Body  string
	Err   string
}

// formatBatchResults renders the per-call outputs into one body.
// Each entry gets a header line + the body indented; errors call
// out the index so the model can correct.
func formatBatchResults(results []replResult, total int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "REPL: %d/%d calls executed\n", len(results), total)
	for _, r := range results {
		b.WriteByte('\n')
		fmt.Fprintf(&b, "── [%d] %s ──\n", r.Index, r.Tool)
		if r.Err != "" {
			fmt.Fprintf(&b, "ERROR: %s\n", r.Err)
			continue
		}
		// Indent body two spaces so the boundary between calls is
		// visually obvious.
		for _, line := range strings.Split(strings.TrimRight(r.Body, "\n"), "\n") {
			fmt.Fprintf(&b, "  %s\n", line)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// extractText flattens a tool result's text content blocks for the
// batch report.
func extractText(out *engine.ToolResultPayload) string {
	if out == nil {
		return ""
	}
	if out.SoftError != "" {
		return out.SoftError
	}
	var b strings.Builder
	for _, c := range out.Content {
		b.WriteString(c.Text)
	}
	return b.String()
}

// envTruthy / envFalsy mirror the orchestration package's existing
// env handling. Defined locally because depending on internal/compact
// would create a backwards import.
func envTruthy(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
func envFalsy(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "0", "false", "no", "off":
		return true
	}
	return false
}

// softErrR is the REPL soft-error helper. Local because the
// orchestration-package text() helper (in task.go) only wraps
// success bodies; this version sets IsError + SoftError.
func softErrR(msg string) *engine.ToolResultPayload {
	return &engine.ToolResultPayload{
		IsError:   true,
		SoftError: msg,
		Content:   []state.ContentBlock{{Type: state.ContentText, Text: msg}},
	}
}

// jsonInline is a tiny helper for tests that want to construct an
// `input` object for a primitive call without raw map literals
// every time. Not used in production paths.
func jsonInline(s string) map[string]any {
	var out map[string]any
	_ = json.Unmarshal([]byte(s), &out)
	return out
}

// errors / os imports stay reachable for future extensibility
// (env override sentinels, future error wrapping).
var _ = errors.New
var _ os.PathError
