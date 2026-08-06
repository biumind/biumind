// Package cost tracks per-session token usage and converts it to USD
// using model-specific pricing tables.
//
// Per-Mtok pricing: input + output + cacheRead + cacheWrite components.
// Web-search requests are tallied separately (a tool emits CostUpdate
// events; engine.QueryEngine doesn't see them).
//
// All prices are USD per million tokens. The Tracker is safe for
// concurrent reads — the engine adds usage from a single goroutine
// and the UI reads from the bubbletea loop.

package cost

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// ModelCost is the per-Mtok price tier for one model.
type ModelCost struct {
	InputTokens          float64
	OutputTokens         float64
	PromptCacheReadTokens float64
	PromptCacheWriteTokens float64
}

// Standard pricing tiers:
var (
	// Sonnet family: $3 in / $15 out.
	TierSonnet = ModelCost{
		InputTokens: 3, OutputTokens: 15,
		PromptCacheWriteTokens: 3.75, PromptCacheReadTokens: 0.3,
	}
	// Opus 4.0/4.1: $15 in / $75 out.
	TierOpusOld = ModelCost{
		InputTokens: 15, OutputTokens: 75,
		PromptCacheWriteTokens: 18.75, PromptCacheReadTokens: 1.5,
	}
	// Opus 4.5/4.6: $5 in / $25 out (default unknown-model cost).
	TierOpus = ModelCost{
		InputTokens: 5, OutputTokens: 25,
		PromptCacheWriteTokens: 6.25, PromptCacheReadTokens: 0.5,
	}
	// Haiku 3.5: $0.80 in / $4 out.
	TierHaiku35 = ModelCost{
		InputTokens: 0.8, OutputTokens: 4,
		PromptCacheWriteTokens: 1, PromptCacheReadTokens: 0.08,
	}
	// Haiku 4.5: $1 in / $5 out.
	TierHaiku45 = ModelCost{
		InputTokens: 1, OutputTokens: 5,
		PromptCacheWriteTokens: 1.25, PromptCacheReadTokens: 0.1,
	}

	// Default for unknown models — conservative.
	DefaultCost = TierOpus
)

// modelTable maps known model IDs (and short aliases) to their tier.
// We match by case-insensitive prefix so e.g. "claude-sonnet-4-6"
// matches both the canonical and the abbreviated form.
var modelTable = []struct {
	prefix string
	cost   ModelCost
}{
	{"claude-haiku-3-5", TierHaiku35},
	{"claude-3-5-haiku", TierHaiku35},
	{"claude-haiku-4-5", TierHaiku45},
	{"claude-haiku-4", TierHaiku45},
	{"claude-3-5-sonnet", TierSonnet},
	{"claude-3-7-sonnet", TierSonnet},
	{"claude-sonnet-4", TierSonnet},
	{"claude-opus-4-1", TierOpusOld},
	{"claude-opus-4-0", TierOpusOld},
	{"claude-opus-4", TierOpus}, // matches 4-5 / 4-6 / 4-7 unless an earlier rule matches
}

// CostFor returns the price tier for a model ID. Falls back to
// DefaultCost when nothing matches.
func CostFor(model string) ModelCost {
	m := strings.ToLower(strings.TrimSpace(model))
	for _, e := range modelTable {
		if strings.HasPrefix(m, e.prefix) {
			return e.cost
		}
	}
	return DefaultCost
}

// USD computes the dollar cost of one usage record under the supplied
// tier. Cache read/write contributions can be zero when the provider
// doesn't report them.
func USD(c ModelCost, in, out, cacheRead, cacheWrite int) float64 {
	const M = 1_000_000.0
	return float64(in)/M*c.InputTokens +
		float64(out)/M*c.OutputTokens +
		float64(cacheRead)/M*c.PromptCacheReadTokens +
		float64(cacheWrite)/M*c.PromptCacheWriteTokens
}

// ToolUsage is the per-tool slice of session usage. We do NOT split
// LLM tokens to a tool — input tokens are context length (LLM-side,
// not tool-side) and output tokens are the model's prose / tool_use
// blocks (also LLM-side). Splitting either to a tool would produce
// numbers that look authoritative but mean nothing.
//
// What we DO track per tool, because it cleanly attributes:
//
//   - Calls         total invocations
//   - ElapsedMs     wall time the tool's Call ran (the runner's elapsed)
//   - OutputBytes   length of the tool result content (proxy for "size")
//   - Errors        invocations that came back with IsError=true
//
// UI usage: render a small leaderboard of "Read 12× / 0.8s / 4KB",
// "Bash 3× / 12.4s / 30KB". Useful for sniffing wasteful tools and
// rate-limiting decisions; not for billing.
type ToolUsage struct {
	Calls       int
	ElapsedMs   int64
	OutputBytes int64
	Errors      int
}

// Tracker accumulates usage for one session. Safe for concurrent
// access from the engine + UI.
type Tracker struct {
	mu    sync.RWMutex
	model string
	cost  ModelCost

	in, out, cacheRead, cacheWrite int
	usd                            float64

	// byTool is the per-tool slice. Populated via AddTool from the
	// runner's runOne exit path; nil-safe.
	byTool map[string]ToolUsage
}

// NewTracker returns a Tracker pre-loaded with the price tier for
// `model`. Pass empty string to use DefaultCost (the tier can be
// updated later via SetModel when the user runs /model <id>).
func NewTracker(model string) *Tracker {
	return &Tracker{model: model, cost: CostFor(model)}
}

// SetModel switches the active price tier. Existing accumulated USD
// is preserved (it represents real money already spent).
func (t *Tracker) SetModel(model string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.model = model
	t.cost = CostFor(model)
}

// Add records a usage delta and adds its dollar cost.
func (t *Tracker) Add(in, out, cacheRead, cacheWrite int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.in += in
	t.out += out
	t.cacheRead += cacheRead
	t.cacheWrite += cacheWrite
	t.usd += USD(t.cost, in, out, cacheRead, cacheWrite)
}

// AddTool records one tool invocation in the per-tool slice. Engine's
// runOne calls this at the result event site so success / soft-error
// / permission-denied / unknown-tool all roll up into the same per-
// tool entry. Concurrency-safe — multiple parallel batches can hit it.
//
// elapsed is the wall time including permission ask + hook fan-out
// (matches what the runner passes to ToolUseResultEvent.Elapsed).
// outputBytes is the byte length of all text content blocks combined.
// isError is true when the result is a soft / hard error so UIs can
// render success/failure rates per tool.
func (t *Tracker) AddTool(name string, elapsed time.Duration, outputBytes int, isError bool) {
	if name == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.byTool == nil {
		t.byTool = map[string]ToolUsage{}
	}
	u := t.byTool[name]
	u.Calls++
	u.ElapsedMs += elapsed.Milliseconds()
	u.OutputBytes += int64(outputBytes)
	if isError {
		u.Errors++
	}
	t.byTool[name] = u
}

// SnapshotByTool returns a copy of the per-tool usage map. Safe to
// call concurrently with AddTool — the returned map is fresh, not a
// reference into Tracker. Empty (nil-safe) when no tool has been
// recorded yet.
func (t *Tracker) SnapshotByTool() map[string]ToolUsage {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if len(t.byTool) == 0 {
		return nil
	}
	out := make(map[string]ToolUsage, len(t.byTool))
	for k, v := range t.byTool {
		out[k] = v
	}
	return out
}

// Snapshot returns a stable read-only copy.
type Snapshot struct {
	Model      string
	InputTokens int
	OutputTokens int
	CacheReadTokens int
	CacheWriteTokens int
	USD float64
}

func (t *Tracker) Snapshot() Snapshot {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return Snapshot{
		Model: t.model,
		InputTokens: t.in, OutputTokens: t.out,
		CacheReadTokens: t.cacheRead, CacheWriteTokens: t.cacheWrite,
		USD: t.usd,
	}
}

// CacheHitRate returns the share of input tokens served from cache,
// in the [0, 1] range. Useful for /cost reports and break detection.
// Returns 0 when no input tokens have been recorded yet.
func (s Snapshot) CacheHitRate() float64 {
	total := s.InputTokens + s.CacheReadTokens + s.CacheWriteTokens
	if total == 0 {
		return 0
	}
	return float64(s.CacheReadTokens) / float64(total)
}

// String formats a snapshot for /cost output.
func (s Snapshot) String() string {
	return fmt.Sprintf("model=%s in=%d out=%d cache_read=%d cache_write=%d cost=$%.4f",
		s.Model, s.InputTokens, s.OutputTokens,
		s.CacheReadTokens, s.CacheWriteTokens, s.USD)
}
