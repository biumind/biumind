// Package agentsummary generates short human-readable summaries of
// agent activity — both per-tool-batch ("git-commit-subject"
// shaped, for status lines and post-compact previews) and per-
// running-subagent ("3-5 word present-tense" updates that surface
// in /agents and AgentBackground polls).
//
// The summary call is kept cheap by routing through the pluggable
// Summariser interface (same shape compact + extractor use) so
// wiring decides whether to send to Haiku, Sonnet, or a local model.
//
// Two surfaces:
//
//	GenerateToolBatch  — describe a completed tool sequence in
//	                      ≤ 30 chars. Past tense + dominant noun.
//	                      Used by SDK / status line / /export.
//	GenerateAgentTick  — describe a sub-agent's most recent action
//	                      in 3-5 words present continuous. Used by
//	                      AgentBackground / /agents progress.
//
// Both are best-effort: model errors return "" + nil rather than
// failing the caller. The summary is informational; losing one
// shouldn't block the underlying tool / agent path.

package agentsummary

import (
	"context"
	"fmt"
	"strings"

	"github.com/biumind/biumind/apps/cli/biu/internal/state"
)

// MaxSummaryChars is the length cap. The prompt asks for ~30 chars;
// we enforce it on output too in case the model overshoots. The
// 30-char limit comes from the mobile / status-bar UI budget.
const MaxSummaryChars = 32

// Summariser is the LLM-call interface — same shape compact +
// sessionmemory.Extractor use. Pass the recent context as messages,
// the prompt template as instruction, get a string back.
type Summariser interface {
	Summarise(ctx context.Context, messages []state.Message, instruction string) (string, error)
}

// ToolCall is one entry in a batch summary input. We don't carry
// the full output payload — past Bash output / file content can be
// huge, and we only need name + input + first line of output for a
// useful summary.
type ToolCall struct {
	Name        string
	Input       map[string]any
	OutputFirst string // first line of output (truncated to 200 chars)
	IsError     bool
}

// GenerateToolBatch returns a ≤30-char past-tense label for a list
// of tool calls. Empty input → "" + nil error (caller decides what
// to show in place of a summary).
//
// Format is fixed: past tense, dominant noun, no
// articles. Example: "Searched in auth/", "Fixed NPE in UserService",
// "Read config.json".
func GenerateToolBatch(ctx context.Context, summer Summariser, tools []ToolCall, lastAssistant string) (string, error) {
	if summer == nil || len(tools) == 0 {
		return "", nil
	}
	instruction := buildToolBatchPrompt(tools, lastAssistant)
	resp, err := summer.Summarise(ctx, nil, instruction)
	if err != nil {
		return "", fmt.Errorf("agentsummary: tool batch: %w", err)
	}
	return clip(strings.TrimSpace(resp)), nil
}

// GenerateAgentTick returns a 3-5 word present-continuous progress
// string for a running sub-agent. previous is the last summary
// (or "" on first call) so the prompt can avoid repeating itself.
//
// recentMessages is the agent's recent history — passed as
// messages so the model sees the actual conversation rather than
// having us inline a paraphrase that loses fidelity.
func GenerateAgentTick(ctx context.Context, summer Summariser, recentMessages []state.Message, previous string) (string, error) {
	if summer == nil {
		return "", nil
	}
	instruction := buildAgentTickPrompt(previous)
	resp, err := summer.Summarise(ctx, recentMessages, instruction)
	if err != nil {
		return "", fmt.Errorf("agentsummary: agent tick: %w", err)
	}
	return clip(strings.TrimSpace(resp)), nil
}

// buildToolBatchPrompt is the system prompt for tool-batch
// summaries. It pins the "past tense + dominant noun" voice and
// the example set so output stays consistent.
func buildToolBatchPrompt(tools []ToolCall, lastAssistant string) string {
	var b strings.Builder
	b.WriteString(`Write a short summary label describing what these tool calls accomplished. It appears as a single-line row in a mobile app and truncates around 30 characters — think git-commit-subject, not sentence.

Keep the verb in past tense and the most distinctive noun. Drop articles, connectors, and long location context first.

Examples:
- Searched in auth/
- Fixed NPE in UserService
- Created signup endpoint
- Read config.json
- Ran failing tests

Output ONLY the label — no quotes, no preamble, no markdown.

Tool batch:
`)
	for i, t := range tools {
		fmt.Fprintf(&b, "%d. %s", i+1, t.Name)
		if k, v := primaryArg(t.Input); k != "" {
			fmt.Fprintf(&b, " %s=%q", k, v)
		}
		if t.IsError {
			b.WriteString(" [ERROR]")
		}
		if t.OutputFirst != "" {
			fmt.Fprintf(&b, " → %s", clipTo(t.OutputFirst, 80))
		}
		b.WriteByte('\n')
	}
	if lastAssistant != "" {
		fmt.Fprintf(&b, "\nAssistant said next: %q\n", clipTo(lastAssistant, 200))
	}
	return b.String()
}

// buildAgentTickPrompt is the per-tick prompt for live sub-agent
// summarisation. It fixes a single voice (3-5 words, present
// continuous) so ticks read consistently.
func buildAgentTickPrompt(previous string) string {
	prevLine := ""
	if previous != "" {
		prevLine = "\nPrevious: \"" + previous + "\" — say something NEW.\n"
	}
	return `Describe your most recent action in 3-5 words using present tense (-ing). Name the file or function, not the branch. Do not use tools.
` + prevLine + `
Good: "Reading runAgent.ts"
Good: "Fixing null check in validate.ts"
Good: "Running auth module tests"
Good: "Adding retry logic to fetchUser"

Bad (past tense): "Analyzed the branch diff"
Bad (too vague): "Investigating the issue"
Bad (too long): "Reviewing full branch diff and AgentTool.tsx integration"
Bad (branch name): "Analyzed adam/background-summary branch diff"

Output ONLY the phrase. No quotes, no preamble.`
}

// primaryArg picks the most-informative argument from a tool input
// map for inclusion in the batch summary prompt. The preference
// order is file_path / path before generic strings. Returns empty
// key when nothing useful is found.
func primaryArg(input map[string]any) (string, string) {
	if input == nil {
		return "", ""
	}
	for _, k := range []string{"file_path", "path", "command", "query", "pattern", "url"} {
		if v, ok := input[k].(string); ok && v != "" {
			return k, v
		}
	}
	return "", ""
}

// clip enforces MaxSummaryChars + strips outer quotes the model
// sometimes emits despite the prompt forbidding them.
func clip(s string) string {
	s = strings.Trim(s, `"'`)
	// Some models like wrapping in **bold** despite "no markdown"
	// instruction; strip a single asterisk pair if present.
	if strings.HasPrefix(s, "**") && strings.HasSuffix(s, "**") {
		s = strings.TrimPrefix(s, "**")
		s = strings.TrimSuffix(s, "**")
	}
	return clipTo(s, MaxSummaryChars)
}

// clipTo truncates s to n chars, appending an ellipsis when cut.
// Unicode-aware via rune iteration so we don't slice in the middle
// of a multibyte char.
func clipTo(s string, n int) string {
	if n <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	if n <= 1 {
		return string(runes[:n])
	}
	return string(runes[:n-1]) + "…"
}
