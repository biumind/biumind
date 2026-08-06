// /effort + /fast slash — model selection shorthand.
//
// Both are sugar over `/model <id>`:
//
//	/fast              — switch to the cheapest fast model in the
//	                     current family (typically Haiku 4.5)
//	/effort            — show current effort tier + options
//	/effort high       — switch to Opus
//	/effort medium     — switch to Sonnet
//	/effort low        — switch to Haiku
//	/effort <model-id> — exact model
//
// Why two commands instead of one: /fast is a mode, not a knob —
// the pattern "I want this single response cheap, then go back to
// what I was using" is common enough that a one-shot flip beats
// remembering the prior model id. /effort is the persistent knob
// for "I'm starting a heavy/light task and want to stay there".
//
// Both rely on the `/model` slash for the actual switch — they
// resolve a tier name to a model id then dispatch.

package repl

import (
	"fmt"
	"strings"
)

// effortTier maps a human-friendly tier name to a canonical model
// id. Order matters for matching (longest-prefix wins) but the
// values intentionally use full ids so the same model always lines
// up.
var effortTiers = []struct {
	Name string
	Tier string // human label shown in help
	Model string
}{
	{"high", "high (Opus 4.7 — slowest, deepest reasoning)", "claude-opus-4-7"},
	{"medium", "medium (Sonnet 4.6 — balanced)", "claude-sonnet-4-6"},
	{"low", "low (Haiku 4.5 — fastest, cheapest)", "claude-haiku-4-5"},
}

// effortByName returns the tier entry matching name (case-insensitive).
func effortByName(name string) (string, string, bool) {
	q := strings.ToLower(strings.TrimSpace(name))
	for _, t := range effortTiers {
		if t.Name == q {
			return t.Model, t.Tier, true
		}
	}
	return "", "", false
}

// handleEffort implements /effort. Returns the (possibly updated)
// model + the note to render. Mirrors the pattern /model uses
// inline in the dispatcher — the model is a value type, so callers
// rely on the returned value to persist the change.
func (m model) handleEffort(parts []string) (model, string) {
	if len(parts) <= 1 {
		return m, renderEffortStatus(m.modelID)
	}
	target := parts[1]

	if mdl, label, ok := effortByName(target); ok {
		m.modelID = mdl
		return m, fmt.Sprintf("/effort: switched to %s\n  model: %s", label, mdl)
	}

	// Treat anything else as an explicit model id (passthrough).
	m.modelID = target
	return m, fmt.Sprintf("/effort: switched to %s (custom id)", target)
}

func renderEffortStatus(currentModel string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "/effort: current model is %s\n\n", currentModel)
	b.WriteString("available tiers (longest-prefix-wins):\n")
	for _, t := range effortTiers {
		marker := "  "
		if strings.Contains(strings.ToLower(currentModel), t.Name) ||
			strings.HasPrefix(currentModel, t.Model) {
			marker = "» "
		}
		fmt.Fprintf(&b, "%s%-7s — %s\n", marker, t.Name, t.Tier)
	}
	b.WriteString("\nusage: /effort <high|medium|low|model-id>")
	return b.String()
}

// handleFast is /fast — shorthand for /effort low. Distinct slash
// because it's the single most-common tier-flip and saves typing.
func (m model) handleFast(parts []string) (model, string) {
	mdl, label, _ := effortByName("low")
	m.modelID = mdl
	return m, fmt.Sprintf("/fast: switched to %s\n  (use /effort high to go back to Opus, "+
		"/effort medium for Sonnet)", label)
}
