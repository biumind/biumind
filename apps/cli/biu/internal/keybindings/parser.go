package keybindings

import (
	"fmt"
	"strings"
)

// ParseKeystroke turns "ctrl+shift+k" into a Keystroke. Modifier
// aliases are accepted so user configs port across:
//
//	ctrl   ← ctrl, control
//	alt    ← alt, opt, option
//	shift  ← shift
//	meta   ← meta              (terminals collapse alt and meta)
//	super  ← cmd, command, super, win
//
// Unknown modifier tokens become the literal key name. Empty input
// returns an error.
func ParseKeystroke(input string) (Keystroke, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return Keystroke{}, fmt.Errorf("empty keystroke")
	}
	parts := strings.Split(input, "+")
	out := Keystroke{}
	for _, p := range parts {
		lower := strings.ToLower(strings.TrimSpace(p))
		switch lower {
		case "ctrl", "control":
			out.Ctrl = true
		case "alt", "opt", "option":
			out.Alt = true
		case "shift":
			out.Shift = true
		case "meta":
			out.Meta = true
		case "cmd", "command", "super", "win":
			out.Super = true
		case "esc":
			out.Key = "escape"
		case "return":
			out.Key = "enter"
		case "space":
			out.Key = " "
		case "↑":
			out.Key = "up"
		case "↓":
			out.Key = "down"
		case "←":
			out.Key = "left"
		case "→":
			out.Key = "right"
		default:
			out.Key = lower
		}
	}
	if out.Key == "" {
		return Keystroke{}, fmt.Errorf("no key name in %q", input)
	}
	return out, nil
}

// ParseChord turns "ctrl+x ctrl+s" into a Chord. A single bare
// space is the space key, not a separator.
func ParseChord(input string) (Chord, error) {
	if input == " " {
		return Chord{{Key: " "}}, nil
	}
	tokens := strings.Fields(strings.TrimSpace(input))
	if len(tokens) == 0 {
		return nil, fmt.Errorf("empty chord")
	}
	chord := make(Chord, 0, len(tokens))
	for _, t := range tokens {
		ks, err := ParseKeystroke(t)
		if err != nil {
			return nil, fmt.Errorf("chord %q: %w", input, err)
		}
		chord = append(chord, ks)
	}
	return chord, nil
}
