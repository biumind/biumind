// Package compact implements the macro-compact pipeline: when the
// conversation outgrows its budget, summarise it through an LLM call
// and replace the message history with the summary.
//
// Trimmed to P0:
//
//   * One pass, no streaming progress UI hooks (Phase D adds those).
//   * No per-section restoration (POST_COMPACT_MAX_FILES_TO_RESTORE
//     etc.) — biu's first cut just keeps the summary as the entire
//     new context. Cache-friendly and predictable.
//   * Threshold check is plain token count; factoring in attached
//     files / images is deferred until those types ship.
//
// Wire-up: the engine calls Auto.Maybe(...) before every LLM request.
// When it fires, the engine swaps state.Messages out for the summary
// and continues the turn.

package compact

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/biumind/biumind/apps/cli/biu/internal/state"
)

// CompactPrompt is the user-message body sent to the model when we
// ask for a summary. Covers nine sections (Primary Request through
// Optional Next Step). Kept as a Go const so test goldens can pin
// the exact text.
const CompactPrompt = `Your task is to create a detailed summary of the conversation so far, paying close attention to the user's explicit requests and your previous actions.
This summary should be thorough in capturing technical details, code patterns, and architectural decisions that would be essential for continuing development work without losing context.

Your summary should include the following sections:

1. Primary Request and Intent: Capture all of the user's explicit requests and intents in detail
2. Key Technical Concepts: List all important technical concepts, technologies, and frameworks discussed.
3. Files and Code Sections: Enumerate specific files and code sections examined, modified, or created. Include full code snippets where applicable and a summary of why this file read or edit is important.
4. Errors and fixes: List all errors that you ran into, and how you fixed them. Pay special attention to specific user feedback.
5. Problem Solving: Document problems solved and any ongoing troubleshooting efforts.
6. All user messages: List ALL user messages that are not tool results. These are critical for understanding the user's intent.
7. Pending Tasks: Outline any pending tasks that you have explicitly been asked to work on.
8. Current Work: Describe in detail precisely what was being worked on immediately before this summary request.
9. Optional Next Step: List the next step that you will take, in line with the user's most recent explicit request. Include direct quotes from the most recent conversation showing exactly where you left off.

Output the summary inside <summary>...</summary> tags. No other commentary.`

// SummaryProvider is the contract the compactor depends on: an LLM
// adapter that takes the existing messages + the compact prompt and
// returns the assistant's textual summary.
//
// Distinct from engine.Provider so we can stub easily and so the
// compact package doesn't import engine (avoids circular import via
// engine → compact → engine).
type SummaryProvider interface {
	Summarise(ctx context.Context, messages []state.Message, instruction string) (string, error)
}

// Options configures the compactor.
type Options struct {
	Provider SummaryProvider
	// MaxTokens is the rough budget; when the engine reports usage
	// above ThresholdRatio * MaxTokens we trigger a compact.
	// Defaults: 200K context, 70% trigger.
	MaxTokens      int
	ThresholdRatio float64

	// Instruction is optional extra guidance prepended to the
	// summary prompt (e.g. "Focus on test output and Go code").
	Instruction string

	// Attachments is an optional closure called at compact time to
	// fetch system messages that must follow the session through
	// the compaction boundary. Used by the engine to re-inject the
	// approved plan after ExitPlanMode.
	//
	// Returning nil / empty slice keeps the legacy behaviour. The
	// closure is called once per Run; it should be cheap.
	Attachments func() []state.Message
}

// Auto wraps a SummaryProvider with the trigger logic.
type Auto struct {
	opt Options
}

// New returns a configured Auto. Defaults applied where caller didn't
// override.
func New(opt Options) *Auto {
	if opt.MaxTokens == 0 {
		opt.MaxTokens = 200_000
	}
	if opt.ThresholdRatio <= 0 {
		opt.ThresholdRatio = 0.7
	}
	return &Auto{opt: opt}
}

// ShouldFire reports whether the running token usage has crossed the
// configured threshold. Engine calls this before every LLM request.
//
// The decision is intentionally conservative — once over the
// threshold, fire. If the model returns max_tokens or the provider
// rejects with prompt-too-long, the engine should also force-fire
// regardless of this hint.
func (a *Auto) ShouldFire(usedTokens int) bool {
	if a == nil || a.opt.MaxTokens == 0 {
		return false
	}
	limit := int(float64(a.opt.MaxTokens) * a.opt.ThresholdRatio)
	return usedTokens >= limit
}

// Result describes one completed compact run.
type Result struct {
	Summary       string          // the model-produced summary text
	OriginalCount int             // messages before compact
	NewCount      int             // messages after compact (typically 1)
	Replaced      []state.Message // the new message slice the caller
	// should write back into AppState.
}

// Run performs one macro compact: ask the provider to summarise
// `messages` + the configured instruction, then build the post-compact
// message slice. The caller is responsible for actually replacing
// AppState.Messages — the package never touches state directly so it
// stays trivially testable.
func (a *Auto) Run(ctx context.Context, messages []state.Message) (*Result, error) {
	if a == nil || a.opt.Provider == nil {
		return nil, errors.New("compact: no provider configured")
	}
	if len(messages) == 0 {
		return nil, errors.New("compact: nothing to summarise")
	}
	instruction := CompactPrompt
	if a.opt.Instruction != "" {
		instruction = CompactPrompt + "\n\nAdditional instructions:\n" + a.opt.Instruction
	}
	summary, err := a.opt.Provider.Summarise(ctx, messages, instruction)
	if err != nil {
		return nil, fmt.Errorf("compact: summarise: %w", err)
	}
	summary = extractSummary(summary)

	// Preserve only the very last user message so the model knows
	// what it's about to answer — the active prompt stays intact
	// across compact.
	var lastUser *state.Message
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == state.RoleUser && hasUserText(messages[i]) {
			m := messages[i]
			lastUser = &m
			break
		}
	}

	replaced := []state.Message{
		{
			Role: state.RoleSystem,
			Content: []state.ContentBlock{{
				Type: state.ContentText,
				Text: "This session was compacted. Use the summary below as the only source of prior context.\n\n" +
					"<summary>\n" + summary + "\n</summary>",
			}},
		},
	}
	// Caller-supplied attachments — currently used for the plan
	// re-injection path (ExitPlanMode → compact). Inserted after
	// the summary so the model reads "here's what happened, here's
	// what you committed to" before the resume prompt.
	if a.opt.Attachments != nil {
		for _, att := range a.opt.Attachments() {
			replaced = append(replaced, att)
		}
	}
	if lastUser != nil {
		replaced = append(replaced, *lastUser)
	}

	return &Result{
		Summary:       summary,
		OriginalCount: len(messages),
		NewCount:      len(replaced),
		Replaced:      replaced,
	}, nil
}

// extractSummary pulls the body out of a `<summary>...</summary>`
// block. Falls back to the entire text when the model forgot the
// tags — better to keep noisy context than to lose everything.
func extractSummary(s string) string {
	open := strings.Index(s, "<summary>")
	if open == -1 {
		return strings.TrimSpace(s)
	}
	close := strings.Index(s, "</summary>")
	if close == -1 || close <= open {
		return strings.TrimSpace(s[open+len("<summary>"):])
	}
	return strings.TrimSpace(s[open+len("<summary>") : close])
}

func hasUserText(m state.Message) bool {
	for _, b := range m.Content {
		if b.Type == state.ContentText && strings.TrimSpace(b.Text) != "" {
			return true
		}
	}
	return false
}
