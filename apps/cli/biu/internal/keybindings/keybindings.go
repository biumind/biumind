// Package keybindings is biu's user-configurable shortcut layer.
//
// Organized as a focused tree (parser → matcher → resolver →
// loader), trimmed to what a Go REPL actually needs:
//
//  1. Parse human chord syntax: "ctrl+k", "alt+enter", "ctrl+x ctrl+s".
//  2. Match against bubbletea's tea.KeyMsg.
//  3. Resolve a key event to a named action (e.g. "submit",
//     "history.prev"), letting the REPL dispatch behaviorally instead
//     of by raw key.
//  4. Allow override via ~/.biumind/keybindings.json so power users
//     can rewire defaults without forking biu.
//
// Why not just hardcode? Because two of our most-active users have
// very strong opinions about ctrl-w and esc — and because plug-in
// authors will eventually want to register actions of their own.
// Naming the action separately from the chord lets us evolve the
// chord later (or per-platform) without breaking docs.
package keybindings

import "strings"

// Action is a stable identifier for a logical command. The set is
// open: callers register the actions they care about; unknown
// actions in user config are surfaced as warnings rather than fatal.
type Action string

// Common biu actions. New actions get added as features need them;
// renaming an existing one is a breaking change for user configs, so
// be deliberate.
const (
	ActionSubmit         Action = "submit"
	ActionCancel         Action = "cancel"
	ActionExit           Action = "exit"
	ActionClear          Action = "clear"
	ActionCompact        Action = "compact"
	ActionHistoryPrev    Action = "history.prev"
	ActionHistoryNext    Action = "history.next"
	ActionExpandPreview  Action = "preview.expand"
	ActionAcceptSuggest  Action = "suggest.accept"
	ActionToggleVoice    Action = "voice.toggle"
	ActionToggleTheme    Action = "theme.toggle"
	ActionPasteImage     Action = "paste.image"
	ActionInsertNewline  Action = "newline"
)

// Keystroke is a parsed single key press: a normalized key name plus
// the modifier set that was held when it fired.
type Keystroke struct {
	Key   string // lowercase name: "a", "enter", "escape", "up", etc.
	Ctrl  bool
	Alt   bool   // alias of Meta in terminals; we keep them distinct on parse.
	Shift bool
	Meta  bool
	Super bool   // cmd / win / super
}

// Chord is one or more keystrokes that must arrive in sequence.
// Single-key bindings are a chord of length 1; multi-key bindings
// like "ctrl+x ctrl+s" are length 2+.
type Chord []Keystroke

// Binding maps a chord to a logical action. Source records where
// the binding came from so /keybindings can show overrides.
type Binding struct {
	Chord  Chord
	Action Action
	Source string // "default" | "user" | name of plugin that registered it
}

// String renders a chord for display: "ctrl+k" or "ctrl+x ctrl+s".
func (c Chord) String() string {
	parts := make([]string, len(c))
	for i, k := range c {
		parts[i] = k.String()
	}
	return strings.Join(parts, " ")
}

// String renders a single keystroke. Modifier order is fixed
// (ctrl→alt→shift→meta→super) so the same chord always serializes
// the same way — important for de-duplication.
func (k Keystroke) String() string {
	var parts []string
	if k.Ctrl {
		parts = append(parts, "ctrl")
	}
	if k.Alt {
		parts = append(parts, "alt")
	}
	if k.Shift {
		parts = append(parts, "shift")
	}
	if k.Meta {
		parts = append(parts, "meta")
	}
	if k.Super {
		parts = append(parts, "cmd")
	}
	parts = append(parts, displayKey(k.Key))
	return strings.Join(parts, "+")
}

// displayKey turns a normalized key name into its display form.
// Most names are passed through; arrows + a few specials get
// pretty equivalents.
func displayKey(key string) string {
	switch key {
	case "":
		return ""
	case " ":
		return "space"
	case "escape":
		return "esc"
	case "enter":
		return "enter"
	case "up":
		return "up"
	case "down":
		return "down"
	case "left":
		return "left"
	case "right":
		return "right"
	default:
		return key
	}
}
