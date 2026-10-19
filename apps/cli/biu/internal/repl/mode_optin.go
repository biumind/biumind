// Auto-mode opt-in dialog — one-time confirmation the first time the
// user switches to auto permission mode via /mode auto.
//
// Why this exists: auto mode stops per-tool approval prompts, so the
// user must explicitly accept that trade once. The acceptance marker
// lives in the USER layer of settings.json only
// (permissions.autoModeOptIn, see internal/settings/optin.go) — a
// malicious repository must not be able to pre-accept on the user's
// behalf via .biumind/settings.json.
//
// Scope: interactive REPL /mode auto only. Non-interactive entries
// (--permission-mode flag, headless, daemon / bridge
// set_permission_mode) already carry explicit user intent and never
// see this dialog.
//
// UI shape follows the permission / question panels: a state flag on
// `model`, a renderer folded into inputArea, and a key handler that
// steals keys while the dialog is up.

package repl

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	clauseSettings "github.com/biumind/biumind/apps/cli/biu/internal/settings"
	"github.com/biumind/biumind/apps/cli/biu/pkg/biumindkit/permissions"
)

// needsAutoOptIn reports whether a switch to newMode must be gated by
// the opt-in dialog: target is auto, the session isn't already in
// auto, and the user-layer marker is absent.
func (m model) needsAutoOptIn(newMode permissions.Mode) bool {
	if newMode != permissions.ModeAuto || m.engine == nil {
		return false
	}
	if m.engine.Permissions().Mode() == permissions.ModeAuto {
		return false
	}
	return !clauseSettings.AutoModeOptedIn()
}

// applyPermissionMode performs the actual mode switch for /mode.
// Plan mode routes through EnterPlanMode/ExitPlanMode so the session
// remembers what to restore to; every other mode is a plain SetMode.
func (m model) applyPermissionMode(newMode permissions.Mode) model {
	ctx := m.engine.Permissions()
	switch {
	case newMode == permissions.ModePlan:
		prev := ctx.EnterPlanMode()
		if prev != "" && prev != permissions.ModePlan {
			m.appendSystemNote("permission mode: plan (was " + string(prev) + ")")
		} else {
			m.appendSystemNote("permission mode: plan")
		}
	case ctx.Mode() == permissions.ModePlan:
		// Leaving plan via /mode → restore via the same path the tool
		// uses so prePlanMode bookkeeping clears.
		ctx.ExitPlanMode()
		ctx.SetMode(newMode)
		m.appendSystemNote("permission mode: " + string(newMode))
	default:
		ctx.SetMode(newMode)
		m.appendSystemNote("permission mode: " + string(newMode))
	}
	return m
}

// renderModeOptIn draws the opt-in panel above the input box while
// m.modeOptInPending is set.
func renderModeOptIn(m model) string {
	if !m.modeOptInPending {
		return ""
	}
	var b strings.Builder
	b.WriteString(styleErrorPrefix.Render("⚠ Enable auto permission mode?"))
	b.WriteString("\n  Tool calls will run WITHOUT asking for confirmation one by one.")
	b.WriteString("\n  Your deny rules and the dangerous-command / sensitive-path guards still apply.")
	b.WriteString("\n  The guards can be turned off via \"dangerousCommandGuard\" in settings.json.")
	b.WriteString("\n  ")
	b.WriteString(styleHint.Render("y enable · n / esc cancel"))
	return b.String()
}

// handleModeOptInKey routes keys while the opt-in dialog is up. Only
// an explicit "y" enables auto; n / esc cancel, everything else is
// swallowed (no default answer for a security opt-in).
func (m model) handleModeOptInKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		m.modeOptInPending = false
		if err := clauseSettings.SetAutoModeOptIn(); err != nil {
			// Persistence failure must not block a choice the user
			// just made explicitly — warn and continue; the dialog
			// will simply reappear next session.
			m.appendSystemNote("auto mode opt-in: could not persist marker: " + err.Error())
		}
		return m.applyPermissionMode(permissions.ModeAuto), nil
	case "n", "N", "esc":
		m.modeOptInPending = false
		m.appendSystemNote("auto mode not enabled — staying in " +
			string(m.engine.Permissions().Mode()))
		return m, nil
	default:
		return m, nil
	}
}
