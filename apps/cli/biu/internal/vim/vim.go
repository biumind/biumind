// Package vim implements a small, REPL-shaped subset of vim's normal-
// and insert-mode behaviours for biu's prompt input.
//
// The goal is *not* to ship neovim — it's to give vim users muscle-
// memory parity in the prompt without dragging in a huge dependency.
// We model the prompt as a single line of runes plus a cursor; that
// covers ~95% of what people type into a REPL. Multi-line input is
// out of scope for now.
//
// What's in:
//
//	mode toggle  : Esc → NORMAL, i/a/A/I/o → INSERT
//	motions      : h l w b e 0 $ ^
//	edits        : x X (delete char), D (delete-to-end), C (change-to-end)
//	operators    : d c y combined with motion (dw, cw, yw, …)
//	repeat       : numeric count prefix (3w, 5x)
//	paste        : p P (default register only)
//
// Out (deliberate):
//
//	visual mode, find (f/F/t/T), text objects (iw, aw, …), marks,
//	multiple registers, dot-repeat, multi-line ops (gg, G, j, k).
//	If a real vim user files a bug for one of these we'll add it; the
//	point of this skill is daily-driver parity, not feature checklist.
package vim

import "unicode"

// Mode is which mode the prompt is currently in.
type Mode int

const (
	ModeInsert Mode = iota
	ModeNormal
)

func (m Mode) String() string {
	switch m {
	case ModeInsert:
		return "insert"
	case ModeNormal:
		return "normal"
	}
	return "unknown"
}

// State is the full vim-mode state for the prompt. A fresh state
// starts in INSERT mode with an empty buffer.
type State struct {
	Mode     Mode
	Buf      []rune
	Pos      int    // cursor index into Buf, in [0, len(Buf)] (insert) or [0, len(Buf)) (normal)
	Register string // the unnamed register; populated by y / d / c / x

	// Pending normal-mode command parse state.
	pending pendingCmd
}

// pendingCmd captures a partial NORMAL-mode command. We re-parse on
// every keypress, so this is small.
type pendingCmd struct {
	count    int  // 0 means "no count specified yet"
	operator byte // 0 = none, otherwise 'd' / 'c' / 'y'
	gPending bool // a single 'g' was pressed; waiting for next char
}

// New returns a fresh State in INSERT mode with the given initial
// buffer. The cursor is placed at the end.
func New(initial string) *State {
	r := []rune(initial)
	return &State{
		Mode: ModeInsert,
		Buf:  r,
		Pos:  len(r),
	}
}

// String returns the current buffer contents.
func (s *State) String() string {
	if s == nil {
		return ""
	}
	return string(s.Buf)
}

// Reset clears any pending NORMAL-mode command. Useful after the
// prompt is submitted or focus changes.
func (s *State) Reset() {
	if s == nil {
		return
	}
	s.pending = pendingCmd{}
}

// clampNormal moves the cursor inside the [0, len(Buf)-1] window
// that NORMAL mode uses. Vim's NORMAL mode never sits past the last
// character of a line.
func (s *State) clampNormal() {
	if len(s.Buf) == 0 {
		s.Pos = 0
		return
	}
	if s.Pos >= len(s.Buf) {
		s.Pos = len(s.Buf) - 1
	}
	if s.Pos < 0 {
		s.Pos = 0
	}
}

// clampInsert keeps the cursor in [0, len(Buf)] (one past the end is
// valid in INSERT — it's the append position).
func (s *State) clampInsert() {
	if s.Pos < 0 {
		s.Pos = 0
	}
	if s.Pos > len(s.Buf) {
		s.Pos = len(s.Buf)
	}
}

// isWordRune classifies a rune as part of a vim "word" (the lower-
// case w/b/e behaviour: alphanum + underscore).
func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}
