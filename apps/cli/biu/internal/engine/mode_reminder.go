// Auto-permission-mode system reminder — one-shot history injection
// when the session crosses into / out of ModeAuto.
//
// Uses the same turn-head attachment channel as plan drift / bg-task
// / teammate completions (see turn.go): a single system message folded
// in at the head of the next user turn. Injection is LAZY — SetMode
// has many callers (wiring, /mode slash, bridge, daemon), so nothing
// fires synchronously there; runSubmit checks mode + an "already
// shown" flag at turn assembly time instead.
//
// Idempotency contract:
//
//   - repeated SetMode(ModeAuto) while already in auto → no repeat
//   - leaving auto → exit reminder injected once, and the enter flag
//     re-arms so re-entering auto injects the enter reminder again
//   - Options.ModeReminder=false → never injects
//
// Reminder text is English, matching the rest of the system-prompt /
// attachment surface (workingDirsBlock, bg-task attachment, …).

package engine

import (
	"github.com/biumind/biumind/apps/cli/biu/internal/state"
	"github.com/biumind/biumind/apps/cli/biu/pkg/biumindkit/permissions"
)

const autoModeEnterReminder = `<permission-mode-reminder>
Auto permission mode is active. Tool approvals will be handled automatically while this mode remains enabled.
- Continue normally without pausing for approval prompts; do not ask the user to confirm routine tool calls.
- Do NOT call AskUserQuestion while auto mode is active; decide and continue.
- Deny rules and the dangerous-command / sensitive-path guards still apply: a guarded operation may still be intercepted and surfaced to the user for approval. If that happens, wait for the user's decision instead of working around it.
</permission-mode-reminder>`

const autoModeExitReminder = `<permission-mode-reminder>
Auto permission mode is no longer active. Tool approvals are back under the user's control: destructive or out-of-scope tool calls will prompt the user again. You may use AskUserQuestion normally when you need the user's input.
</permission-mode-reminder>`

// maybeInjectModeReminder folds the auto-mode reminder into history
// when the active mode has crossed the auto boundary since the last
// turn. Called once per Submit at the head of runSubmit, before the
// other turn-head attachments. No-op when the reminder is disabled or
// no permissions context is wired.
func (e *QueryEngine) maybeInjectModeReminder() {
	if !e.modeReminder || e.perms == nil {
		return
	}
	inAuto := e.perms.Mode() == permissions.ModeAuto
	switch {
	case inAuto && !e.autoReminderShown:
		e.autoReminderShown = true
		e.state.AppendMessage(state.Message{
			Role: state.RoleSystem,
			Content: []state.ContentBlock{{
				Type: state.ContentText, Text: autoModeEnterReminder,
			}},
		})
	case !inAuto && e.autoReminderShown:
		e.autoReminderShown = false
		e.state.AppendMessage(state.Message{
			Role: state.RoleSystem,
			Content: []state.ContentBlock{{
				Type: state.ContentText, Text: autoModeExitReminder,
			}},
		})
	}
}
