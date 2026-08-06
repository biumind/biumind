// Background extractor — periodically asks the model to summarise
// the recent conversation into the SessionMemory's structured
// sections (Current State, Files and Functions, Errors &
// Corrections, Workflow, Codebase Documentation).
//
// Runs on a per-N-tool-call cadence rather than
// every turn so we don't pay extraction cost on chat-only turns
// (no Edit / Write / Bash means there's nothing new to write
// down).
//
// Architecture: the extractor doesn't run a forked sub-agent — a
// forked-agent surface is heavier than what one extraction needs.
// Instead we use a pluggable Summariser — the same interface compact's
// SummaryProvider exposes — so callers can wire either a direct
// LLM call or, in the future, a forked agent.
//
// Output format: a single LLM call with the structured prompt
// asking for "yaml-like" key=value pairs per section. We parse
// the response back into Section updates and merge into the
// existing SessionMemory file (preserving sections the model
// didn't have new info for).

package sessionmemory

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/biumind/biumind/apps/cli/biu/internal/state"
)

// Summariser is the LLM-call surface the extractor needs. The
// engine's compact provider already implements this shape
// (Run(ctx, messages, instruction) → string); reusing the same
// interface lets the wiring layer pass one summariser to both
// compact and the extractor.
type Summariser interface {
	Summarise(ctx context.Context, messages []state.Message, instruction string) (string, error)
}

// ExtractorConfig tunes the trigger cadence + prompt.
type ExtractorConfig struct {
	// MinToolCallsBetweenRuns — extraction fires after this many
	// distinct tool_use blocks have appeared since the last run.
	// Default 10. Set to 0 to disable cadence gating (every
	// MaybeRun call extracts; useful for tests).
	MinToolCallsBetweenRuns int

	// MinMessagesForFirstRun — initialisation threshold; the
	// session must have at least this many messages before the
	// FIRST extraction run. Avoids burning a token call on a
	// fresh empty session. Default 6 (a couple of turns).
	MinMessagesForFirstRun int
}

// DefaultExtractorConfig returns the standard trigger cadence + init
// threshold tuned for typical chat sessions.
func DefaultExtractorConfig() ExtractorConfig {
	return ExtractorConfig{
		MinToolCallsBetweenRuns: 10,
		MinMessagesForFirstRun:  6,
	}
}

// Extractor coordinates periodic SessionMemory extraction. Stateful:
// remembers the last extraction's tool-call count so the cadence
// gate works without the caller threading state through.
type Extractor struct {
	cfg     ExtractorConfig
	summer  Summariser
	mem     *SessionMemory
	mu      sync.Mutex
	lastRun int  // tool-call count at last run; 0 = never run
	running bool // single-flight gate
}

// NewExtractor builds an extractor for the given session memory +
// summariser. Returns nil when summariser is nil — caller can use
// that signal to keep the rest of the wiring clean.
func NewExtractor(mem *SessionMemory, summer Summariser, cfg ExtractorConfig) *Extractor {
	if summer == nil || mem == nil {
		return nil
	}
	if cfg.MinToolCallsBetweenRuns < 0 {
		cfg = DefaultExtractorConfig()
	}
	return &Extractor{cfg: cfg, summer: summer, mem: mem}
}

// MaybeRun checks the cadence + initialisation thresholds and runs
// extraction when both clear. Single-flight: if a previous run is
// still in flight, this returns immediately without queueing.
//
// Returns (true, nil) when an extraction completed; (false, nil)
// when the cadence didn't allow it; (false, err) when the
// summariser or parse failed. Never blocks the caller — the
// extraction itself is synchronous because it uses the supplied
// context, but the caller decides whether to invoke from a
// background goroutine.
func (e *Extractor) MaybeRun(ctx context.Context, messages []state.Message) (bool, error) {
	if e == nil {
		return false, nil
	}
	e.mu.Lock()
	if e.running {
		e.mu.Unlock()
		return false, nil
	}
	tools := countToolUses(messages)
	if !e.shouldRun(messages, tools) {
		e.mu.Unlock()
		return false, nil
	}
	e.running = true
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		e.running = false
		e.lastRun = tools
		e.mu.Unlock()
	}()

	return e.run(ctx, messages)
}

// shouldRun encapsulates the cadence + init checks so MaybeRun's
// happy path stays one screenful.
func (e *Extractor) shouldRun(messages []state.Message, tools int) bool {
	// First run: requires the message-count threshold.
	if e.lastRun == 0 {
		return len(messages) >= e.cfg.MinMessagesForFirstRun
	}
	// Subsequent runs: cadence gate.
	if e.cfg.MinToolCallsBetweenRuns == 0 {
		return true
	}
	return tools-e.lastRun >= e.cfg.MinToolCallsBetweenRuns
}

// run is the inner extraction call. Builds the prompt, calls the
// summariser, parses the response, merges into the SessionMemory
// file, persists.
func (e *Extractor) run(ctx context.Context, messages []state.Message) (bool, error) {
	instruction := buildExtractionPrompt(e.mem)
	resp, err := e.summer.Summarise(ctx, messages, instruction)
	if err != nil {
		return false, fmt.Errorf("extractor summarise: %w", err)
	}
	updates := parseExtractionResponse(resp)
	if len(updates) == 0 {
		return false, errors.New("extractor: model returned no parseable section updates")
	}
	for name, body := range updates {
		body = strings.TrimSpace(body)
		if body == "" {
			continue
		}
		e.mem.SetSection(name, body)
	}
	e.mem.Truncate()
	if err := e.mem.Save(); err != nil {
		return false, fmt.Errorf("extractor save: %w", err)
	}
	return true, nil
}

// buildExtractionPrompt assembles the instruction string. We
// include the current sections so the model knows what's already
// there (it should ADD, not REPLACE, useful detail) and a strict
// output format so parsing is deterministic.
func buildExtractionPrompt(mem *SessionMemory) string {
	var b strings.Builder
	b.WriteString(`You are maintaining a long-running session memory file. Read the prior conversation and produce updates to the following named sections. Each section gets a fresh body — preserve any prior information that is still relevant, drop anything obsolete.

Output format — emit EXACTLY this shape, one section header per line followed by its body content. No preamble, no commentary, no markdown headers:

<<<SECTION:Session Title>>>
<one short descriptive line, 5-10 words, super info dense>
<<<SECTION:Current State>>>
<what is actively being worked on right now, pending tasks, immediate next steps>
<<<SECTION:Task specification>>>
<what the user asked to build + design decisions>
<<<SECTION:Files and Functions>>>
<important files + 1-line description of why each is relevant>
<<<SECTION:Workflow>>>
<bash commands run + how to interpret their output>
<<<SECTION:Errors & Corrections>>>
<errors encountered + fixes; approaches that failed>
<<<END>>>

Constraints:
- Each section body should be plain markdown (lists ok, sub-headers ok).
- Skip any section where you have nothing material to add — emit only the header line followed by the existing body verbatim.
- Do NOT echo the conversation or summarise turn-by-turn. Distil into the sections only.
- Cap each section under 1000 characters.

Current memory state for reference (you may copy unchanged sections verbatim):
`)
	if mem != nil {
		b.WriteString(mem.Render())
	}
	return b.String()
}

// parseExtractionResponse pulls section bodies out of the
// `<<<SECTION:Name>>>` block format the prompt requires. Returns
// a map name → body. Ignores body content before the first
// section header (model preamble) and after the <<<END>>> marker
// (trailing model commentary).
//
// Robust to a couple of common deviations:
//   - extra leading whitespace before headers
//   - missing <<<END>>> marker (everything from last header to EOF)
//   - duplicate sections (last wins)
func parseExtractionResponse(resp string) map[string]string {
	out := map[string]string{}
	const headerPrefix = "<<<SECTION:"
	const headerSuffix = ">>>"
	const endMarker = "<<<END>>>"

	lines := strings.Split(resp, "\n")
	currentName := ""
	var currentBody []string
	flush := func() {
		if currentName != "" && len(currentBody) > 0 {
			out[currentName] = strings.TrimSpace(strings.Join(currentBody, "\n"))
		}
	}
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == endMarker {
			flush()
			return out
		}
		if strings.HasPrefix(line, headerPrefix) && strings.HasSuffix(line, headerSuffix) {
			flush()
			currentName = strings.TrimSpace(line[len(headerPrefix) : len(line)-len(headerSuffix)])
			currentBody = nil
			continue
		}
		if currentName != "" {
			currentBody = append(currentBody, raw)
		}
	}
	flush()
	return out
}

// countToolUses returns the number of tool_use blocks in messages.
// Used by the cadence gate. We count distinct uses — a single
// message with two tool_use blocks counts as two.
func countToolUses(messages []state.Message) int {
	n := 0
	for _, m := range messages {
		for _, b := range m.Content {
			if b.Type == state.ContentToolUse {
				n++
			}
		}
	}
	return n
}
