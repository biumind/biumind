package keybindings

import (
	tea "github.com/charmbracelet/bubbletea"
)

// teaKeyName normalises a tea.KeyMsg into a key-name string aligned
// with our parser ("escape", "enter", "up", lowercase letters, …).
// Returns "" when bubbletea hasn't given us anything we can match.
func teaKeyName(msg tea.KeyMsg) string {
	switch msg.Type {
	case tea.KeyEnter:
		return "enter"
	case tea.KeyEsc:
		return "escape"
	case tea.KeyTab:
		return "tab"
	case tea.KeyBackspace:
		return "backspace"
	case tea.KeyDelete:
		return "delete"
	case tea.KeyUp:
		return "up"
	case tea.KeyDown:
		return "down"
	case tea.KeyLeft:
		return "left"
	case tea.KeyRight:
		return "right"
	case tea.KeyPgUp:
		return "pageup"
	case tea.KeyPgDown:
		return "pagedown"
	case tea.KeyHome:
		return "home"
	case tea.KeyEnd:
		return "end"
	case tea.KeySpace:
		return " "
	case tea.KeyRunes:
		if len(msg.Runes) != 1 {
			return ""
		}
		// lowercase the rune so "A" with shift matches "a"+shift.
		r := msg.Runes[0]
		if r >= 'A' && r <= 'Z' {
			r += 'a' - 'A'
		}
		return string(r)
	case tea.KeyCtrlA, tea.KeyCtrlB, tea.KeyCtrlC, tea.KeyCtrlD,
		tea.KeyCtrlE, tea.KeyCtrlF, tea.KeyCtrlG, tea.KeyCtrlH,
		tea.KeyCtrlJ, tea.KeyCtrlK, tea.KeyCtrlL, tea.KeyCtrlN,
		tea.KeyCtrlO, tea.KeyCtrlP, tea.KeyCtrlQ, tea.KeyCtrlR,
		tea.KeyCtrlS, tea.KeyCtrlT, tea.KeyCtrlU, tea.KeyCtrlV,
		tea.KeyCtrlW, tea.KeyCtrlX, tea.KeyCtrlY, tea.KeyCtrlZ:
		// tea.KeyMsg.String() yields "ctrl+a" etc.; strip prefix.
		s := msg.String()
		if len(s) >= 5 && s[:5] == "ctrl+" {
			return s[5:]
		}
		return s
	}
	return ""
}

// teaModifiers extracts modifier flags from a KeyMsg. Bubbletea
// folds Alt into msg.Alt (no Meta/Super), so Meta and Super are
// always false for now — kitty-protocol terminals will need a future
// upgrade.
func teaModifiers(msg tea.KeyMsg) Keystroke {
	out := Keystroke{Alt: msg.Alt}
	switch msg.Type {
	case tea.KeyCtrlA, tea.KeyCtrlB, tea.KeyCtrlC, tea.KeyCtrlD,
		tea.KeyCtrlE, tea.KeyCtrlF, tea.KeyCtrlG, tea.KeyCtrlH,
		tea.KeyCtrlJ, tea.KeyCtrlK, tea.KeyCtrlL, tea.KeyCtrlN,
		tea.KeyCtrlO, tea.KeyCtrlP, tea.KeyCtrlQ, tea.KeyCtrlR,
		tea.KeyCtrlS, tea.KeyCtrlT, tea.KeyCtrlU, tea.KeyCtrlV,
		tea.KeyCtrlW, tea.KeyCtrlX, tea.KeyCtrlY, tea.KeyCtrlZ:
		out.Ctrl = true
	}
	return out
}

// MatchesKeystroke reports whether a tea.KeyMsg matches a target
// Keystroke. Alt and Meta are treated as equivalent (terminals
// collapse them) so a config of either form fires.
func MatchesKeystroke(msg tea.KeyMsg, target Keystroke) bool {
	name := teaKeyName(msg)
	if name == "" || name != target.Key {
		return false
	}
	mods := teaModifiers(msg)
	if mods.Ctrl != target.Ctrl {
		return false
	}
	// Shift on letters is tricky — tea reports the uppercase rune
	// without a Shift flag. We approximate: shift required iff target
	// key is non-alpha, since an alpha+shift binding will land here as
	// the bare uppercase letter (and we lowercase it). Best-effort.
	// Alt/Meta collapse:
	targetMeta := target.Alt || target.Meta
	if mods.Alt != targetMeta {
		return false
	}
	if target.Super && !mods.Super {
		return false
	}
	return true
}

// MatchesBinding checks the first keystroke of a Chord. Multi-key
// chords are handled by the resolver's pending-state machine, not
// here.
func MatchesBinding(msg tea.KeyMsg, b Binding) bool {
	if len(b.Chord) == 0 {
		return false
	}
	return MatchesKeystroke(msg, b.Chord[0])
}
