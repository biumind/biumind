// ApplyPermissionMode switches the active permission mode mid-session.
// Shared by every control-plane entry point (bridge set_permission_mode
// control request, daemon worker control queue) so they all follow the
// REPL /mode semantics:
//
//   - unknown mode strings fall back to default via ModeFromString
//     (no error — the caller-visible result is the resolved mode)
//   - plan transitions go through EnterPlanMode / ExitPlanMode to keep
//     prePlanMode bookkeeping intact; every other mode is a plain
//     SetMode
//
// The decision chain re-reads the mode on every tool call, so the
// change takes effect on the next permission check. Returns the
// resulting mode so callers can ack it back to the client.
package biumindkit

import "github.com/biumind/biumind/apps/cli/biu/pkg/biumindkit/permissions"

// ApplyPermissionMode applies mode to ctx. Nil ctx is a safe no-op
// returning ModeDefault.
func ApplyPermissionMode(ctx *permissions.Context, mode string) permissions.Mode {
	if ctx == nil {
		return permissions.ModeDefault
	}
	newMode := permissions.ModeFromString(mode)
	switch {
	case newMode == permissions.ModePlan:
		ctx.EnterPlanMode()
	case ctx.Mode() == permissions.ModePlan:
		ctx.ExitPlanMode()
		ctx.SetMode(newMode)
	default:
		ctx.SetMode(newMode)
	}
	return ctx.Mode()
}
