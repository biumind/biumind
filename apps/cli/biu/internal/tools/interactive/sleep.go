// Sleep — block the agent for a specified duration without holding a
// shell process.
//
// model
// uses this when the user asks to wait, when nothing's happening, or
// during long-running async work. Unlike `Bash(sleep N)`, this
// doesn't fork a shell — the engine schedules a context-aware timer
// inside the tool runner so the user can interrupt cleanly.
//
// Concurrency: Sleep is concurrency-safe — multiple parallel Sleep
// calls don't interfere with each other (Go's time.Timer is
// goroutine-private). The model can layer Sleep alongside other
// tools without coordination.
//
// Cancellation: respects ctx.Done(), so /quit / Ctrl-C aborts the
// wait without leaking the timer.
//
// Cap: any single sleep > 1h is clamped, with a result note. Long
// waits should be expressed as `ScheduleCron` for the recurring
// case or as a structured TaskCreate dependency for the one-shot
// case where the agent should resume on an external signal.

package interactive

import (
	"context"
	"fmt"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
)

// SleepTool is the engine-facing tool registration.
type SleepTool struct{}

// MaxSleepDuration caps any single Sleep call. Beyond this, the model
// should use a different mechanism (Cron / Task dependency) so the
// agent loop isn't held hostage by a long timer.
const MaxSleepDuration = time.Hour

func (SleepTool) Name() string { return "Sleep" }

func (SleepTool) Description(_ map[string]any) string {
	return "Wait for a specified duration. The user can interrupt at any time. " +
		"Prefer over `Bash(sleep ...)` — it doesn't hold a shell process. " +
		"Concurrency-safe: can run alongside other tool calls without interference. " +
		"Single calls capped at 1h; for longer waits use ScheduleCron or " +
		"TaskCreate with a wake condition."
}

func (SleepTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"duration_seconds": map[string]any{
				"type":        "number",
				"minimum":     0,
				"description": "How long to wait, in seconds. Fractional values OK (e.g. 0.5 for 500ms).",
			},
			"reason": map[string]any{
				"type":        "string",
				"description": "Optional one-line note shown in the tool result for telemetry.",
			},
		},
		"required": []string{"duration_seconds"},
	}
}

func (SleepTool) IsReadOnly(_ map[string]any) bool        { return true }
func (SleepTool) IsDestructive(_ map[string]any) bool     { return false }
func (SleepTool) IsConcurrencySafe(_ map[string]any) bool { return true }
func (SleepTool) InterruptBehavior() string               { return "cancel" }

func (SleepTool) Call(ctx context.Context, input map[string]any, env *engine.ToolEnv) (*engine.ToolResultPayload, error) {
	// Accept any numeric type the JSON / Go boundary may produce.
	// Models send float64 (from JSON decode); Go test callers pass
	// int literals. Mixed-typed inputs harmless — we coerce.
	var raw float64
	switch v := input["duration_seconds"].(type) {
	case float64:
		raw = v
	case float32:
		raw = float64(v)
	case int:
		raw = float64(v)
	case int64:
		raw = float64(v)
	default:
		return softErr("Sleep", "`duration_seconds` (number) required"), nil
	}
	if raw < 0 {
		return softErr("Sleep", "duration_seconds must be ≥ 0"), nil
	}
	requested := time.Duration(raw * float64(time.Second))

	clamped := requested
	clampNote := ""
	if clamped > MaxSleepDuration {
		clamped = MaxSleepDuration
		clampNote = fmt.Sprintf(" (clamped from %s to %s — use ScheduleCron for longer waits)",
			requested.Round(time.Second), MaxSleepDuration)
	}

	reason, _ := input["reason"].(string)

	timer := time.NewTimer(clamped)
	defer timer.Stop()

	select {
	case <-timer.C:
		msg := fmt.Sprintf("Waited %s%s.", clamped.Round(time.Millisecond), clampNote)
		if reason != "" {
			msg += " " + reason
		}
		return text(msg), nil
	case <-ctx.Done():
		// Interrupted (Ctrl-C, /quit, parent cancellation). Report as
		// a soft error so the model can decide whether to retry.
		return softErr("Sleep",
			fmt.Sprintf("interrupted after waiting (target was %s)", clamped.Round(time.Millisecond))), nil
	}
}
