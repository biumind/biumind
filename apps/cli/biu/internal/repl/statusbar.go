// Status bar rendering + helpers (mode badge / mode background /
// context bar / cost+ctx note / user status-line script segment).

package repl

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/biumind/biumind/apps/cli/biu/internal/cost"
	"github.com/biumind/biumind/apps/cli/biu/internal/permissions"
	"github.com/biumind/biumind/apps/cli/biu/internal/statusline"
)

func (m model) statusBar() string {
	left := styleStatus.Render("✦ biu") +
		styleStatusMuted.Render(" · "+m.modelID)

	// Mode badge (plan / acceptEdits / bypass / dontAsk) painted into
	// the left side so it's always visible. Default mode → no badge,
	// keeping the bar clean when nothing unusual is going on.
	if m.engine != nil {
		mode := m.engine.Permissions().Mode()
		if badge := modeBadge(mode); badge != "" {
			left = left + " " + badge
		}
	}

	// Plan-pinned indicator: when the engine is carrying an approved
	// plan that survives compact, surface that on the bar so the user
	// knows the model is bound by it. Subtle by design — a tiny `📋`
	// is enough; details live in `/plan diff`.
	if m.engine != nil && m.engine.Permissions().PlanAttachment() != "" {
		left = left + styleStatusMuted.Render(" · 📋 plan")
	}

	state := ""
	switch m.state {
	case stateSending:
		state = m.spinner.View() + " sending"
	case stateStreaming:
		state = m.spinner.View() + " streaming"
	case stateError:
		state = "error"
	default:
		state = fmt.Sprintf("%d turns", m.turnsCount)
	}

	// Right-side cluster: user-script · cost · context-bar · state.
	// User script first so visually it lives left of biu's own
	// metrics, blending in with the cluster rather than disrupting
	// the built-in ordering.
	rightParts := []string{}
	if seg := m.userStatusLineSegment(); seg != "" {
		rightParts = append(rightParts, styleStatusMuted.Render(seg))
	}
	if note := m.costAndContextNote(); note != "" {
		rightParts = append(rightParts, styleStatusMuted.Render(note))
	}
	rightParts = append(rightParts, styleStatusMuted.Render(state))
	right := strings.Join(rightParts, styleStatusMuted.Render(" · "))

	pad := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if pad < 1 {
		pad = 1
	}
	// Filler colour mirrors the active mode so the entire bar tints
	// when the user is in plan / bypass — peripheral-vision cue that
	// they're not in default mode.
	bg := modeBackground(m.engine)
	filler := lipgloss.NewStyle().Background(bg).
		Render(strings.Repeat(" ", pad))
	return left + filler + right
}

// costAndContextNote builds the right-side compact summary the
// status bar shows when an engine is wired:
//
//	$0.0042 · ctx 41% [████░░░░░░]
//
// Two omissions on purpose:
//   - The cost block is hidden when USD is below $0.0001 (rounding to
//     "$0.0000" looks broken). On a fresh session the user knows it's
//     zero anyway.
//   - The context block is hidden until the first StreamUsageEvent has
//     fired — before that we'd be charting "0% used", which is true but
//     misleading (no measurement yet vs. genuinely empty).
func (m model) costAndContextNote() string {
	if m.engine == nil {
		return ""
	}
	var parts []string
	snap := m.engine.Cost().Snapshot()
	if snap.USD >= 0.0001 {
		parts = append(parts, fmt.Sprintf("$%.4f", snap.USD))
	}
	usage := cost.ContextUsage{
		InputTokens:       m.lastUsageInput,
		CacheReadTokens:   m.lastUsageCacheRead,
		CacheCreateTokens: m.lastUsageCacheCreate,
	}
	if usage.Total() > 0 {
		pct := cost.ContextPercent(usage, m.modelID)
		parts = append(parts, fmt.Sprintf("ctx %d%% %s", pct.Used, contextBar(pct.Used)))
	}
	return strings.Join(parts, " · ")
}

// userStatusLineSegment asks the user-configured shell command (if
// any) for its current text. Throttled by the runner's own cache so
// every Bubble Tea repaint doesn't fork a process. Empty string when
// no command is configured, the cache is cold, or the script
// silently failed.
//
// We pass a short-lived context (1.5× the runner timeout) rather
// than context.Background so a misbehaving script can't pin a goroutine
// past the View() call. The runner itself wraps with its own
// per-call timeout — this is just an upper bound on the whole render.
func (m model) userStatusLineSegment() string {
	if m.statusLine == nil {
		return ""
	}
	cwd := safeCwd()
	// Trust gate: a malicious repo's `.biumind/settings.json` could
	// stash a shell command in `statusLine` that runs on every
	// render = guaranteed RCE. We refuse to fork the user script
	// unless this directory has been trusted (or the env bypass is
	// set for CI). Nil trust store = legacy mode, no gating.
	if m.trust != nil && !m.trust.IsTrusted(cwd) {
		return ""
	}
	in := statusline.Input{
		Model: m.modelID,
		Cwd:   cwd,
		Turns: m.turnsCount,
	}
	if m.engine != nil {
		in.Mode = string(m.engine.Permissions().Mode())
		snap := m.engine.Cost().Snapshot()
		in.CostUSD = snap.USD
		in.InputTokens = snap.InputTokens
	}
	ctx, cancel := context.WithTimeout(context.Background(),
		statusline.DefaultTimeout+statusline.DefaultTimeout/2)
	defer cancel()
	return m.statusLine.Render(ctx, in)
}

// safeCwd swallows os.Getwd errors so a missing cwd doesn't blank
// the entire status line. Returns "" on failure — the script can
// distinguish that from a real path.
func safeCwd() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return cwd
}

// contextBar renders a 10-segment progress bar from a 0-100 percent.
// Filled glyphs are FULL BLOCK, empty are LIGHT SHADE — these give a
// clean two-tone look on most terminals without relying on colour.
func contextBar(pctUsed int) string {
	const width = 10
	if pctUsed < 0 {
		pctUsed = 0
	}
	if pctUsed > 100 {
		pctUsed = 100
	}
	filled := (pctUsed * width) / 100
	if pctUsed > 0 && filled == 0 {
		// Round the smallest non-zero usage up to one segment so
		// the bar doesn't lie.
		filled = 1
	}
	return "[" + strings.Repeat("█", filled) + strings.Repeat("░", width-filled) + "]"
}

// modeBadge renders a short coloured pill when the active permission
// mode is non-default, using the ⏵⏵ / ❙❙ symbols so users coming
// from Claude Code recognise the state at a glance.
func modeBadge(mode permissions.Mode) string {
	if mode == "" || mode == permissions.ModeDefault {
		return ""
	}
	label := mode.ShortTitle()
	symbol := mode.Symbol()
	text := strings.TrimSpace(symbol + " " + label)

	style := lipgloss.NewStyle().Bold(true).Padding(0, 1)
	switch mode {
	case permissions.ModePlan:
		style = style.Background(lipgloss.Color("#1f6feb")).
			Foreground(lipgloss.Color("#ffffff"))
	case permissions.ModeAcceptEdits, permissions.ModeAutoEdit:
		style = style.Background(lipgloss.Color("#bb8009")).
			Foreground(lipgloss.Color("#000000"))
	case permissions.ModeBypass, permissions.ModeFullAccess:
		style = style.Background(lipgloss.Color("#da3633")).
			Foreground(lipgloss.Color("#ffffff"))
	case permissions.ModeDontAsk:
		style = style.Background(lipgloss.Color("#484f58")).
			Foreground(lipgloss.Color("#ffffff"))
	default:
		style = style.Background(colorPurple)
	}
	return style.Render(text)
}

// modeBackground tints the status bar filler so the WHOLE bar
// signals the active mode. Default mode keeps the existing purple.
func modeBackground(eng modeReader) lipgloss.TerminalColor {
	if eng == nil {
		return colorPurple
	}
	switch eng.Permissions().Mode() {
	case permissions.ModePlan:
		return lipgloss.Color("#1f6feb")
	case permissions.ModeAcceptEdits, permissions.ModeAutoEdit:
		return lipgloss.Color("#bb8009")
	case permissions.ModeBypass, permissions.ModeFullAccess:
		return lipgloss.Color("#da3633")
	case permissions.ModeDontAsk:
		return lipgloss.Color("#484f58")
	}
	return colorPurple
}

// modeReader is the tiny interface modeBackground needs. The engine
// satisfies it directly; tests can pass a stub.
type modeReader interface {
	Permissions() *permissions.Context
}
