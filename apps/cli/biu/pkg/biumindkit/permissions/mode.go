// Permission modes.
//
// Five modes:
//
//   default            — ask on first use of a destructive tool
//   acceptEdits        — auto-allow Edit/Write to project workspace
//   plan               — read-only browsing (denies all writes)
//   bypassPermissions  — never ask; "yolo" mode (--dangerously-skip-permissions)
//   dontAsk            — answer every ask with deny
//
// The mode is independent of rules. A rule-allow always wins over the
// mode's default ask. A rule-deny always wins over the mode's allow.

package permissions

import "strings"

// Mode is the active permission mode.
type Mode string

const (
	ModeDefault     Mode = "default"
	ModeAcceptEdits Mode = "acceptEdits"
	ModePlan        Mode = "plan"
	ModeBypass      Mode = "bypassPermissions"
	ModeDontAsk     Mode = "dontAsk"

	// ModeFullAccess is a legacy alias kept for backwards compat with
	// the old config schema. New code should use ModeBypass.
	ModeFullAccess Mode = "full_access"
	// ModeAutoEdit is a legacy alias kept for backwards compat. New
	// code should use ModeAcceptEdits.
	ModeAutoEdit Mode = "auto_edit"
	// ModeAsk is a legacy alias kept for backwards compat. New code
	// should use ModeDefault.
	ModeAsk Mode = "ask"
)

// ModeFromString parses a user-supplied mode (CLI flag, settings.json,
// env var). Unknown strings fall back to ModeDefault. Legacy biu
// aliases (ask / auto_edit / full_access) are mapped to their canonical
// equivalents so the same eval path covers both vocabularies.
func ModeFromString(s string) Mode {
	switch strings.TrimSpace(s) {
	case "default", "":
		return ModeDefault
	case "acceptEdits", "accept_edits":
		return ModeAcceptEdits
	case "plan":
		return ModePlan
	case "bypassPermissions", "bypass":
		return ModeBypass
	case "dontAsk", "dont_ask":
		return ModeDontAsk

	// Legacy biu vocabulary
	case "ask":
		return ModeDefault
	case "auto_edit":
		return ModeAcceptEdits
	case "full_access":
		return ModeBypass
	}
	return ModeDefault
}

// IsValid reports whether the mode is one of the recognised values.
func (m Mode) IsValid() bool {
	switch m {
	case ModeDefault, ModeAcceptEdits, ModePlan, ModeBypass, ModeDontAsk,
		ModeFullAccess, ModeAutoEdit, ModeAsk:
		return true
	}
	return false
}

// ShortTitle is a 1-word display label used in the status bar.
func (m Mode) ShortTitle() string {
	switch m {
	case ModeAcceptEdits, ModeAutoEdit:
		return "Accept"
	case ModePlan:
		return "Plan"
	case ModeBypass, ModeFullAccess:
		return "Bypass"
	case ModeDontAsk:
		return "DontAsk"
	default:
		return "Default"
	}
}

// Symbol is the marker shown next to the mode title in the TUI. Empty
// string for default mode.
func (m Mode) Symbol() string {
	switch m {
	case ModeAcceptEdits, ModeAutoEdit, ModeBypass, ModeFullAccess, ModeDontAsk:
		return "⏵⏵"
	case ModePlan:
		return "❙❙"
	}
	return ""
}
