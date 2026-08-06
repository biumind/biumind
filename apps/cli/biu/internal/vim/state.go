package vim

// Feed advances the state machine with one keystroke. The string
// `key` is a normalised key name: a single rune ("a", "0", "$"),
// "esc", "enter", "backspace", or "ctrl-<rune>" for control combos.
//
// Returns true when the key was consumed by vim mode. False means
// the caller should let its normal input handler take it (e.g. when
// in INSERT mode, regular typing falls through to the prompt's
// editor).
//
// Why fall-through instead of always-true? Because the textinput
// widget already does great Unicode handling, IME, paste, etc. Vim
// here is a thin overlay — INSERT mode is "vim shape" but we delegate
// the actual edits to the host widget. Only NORMAL mode does its own
// editing.
func (s *State) Feed(key string) bool {
	if s == nil {
		return false
	}
	if s.Mode == ModeInsert {
		return s.feedInsert(key)
	}
	return s.feedNormal(key)
}

// ─── INSERT mode ─────────────────────────────────────────────

func (s *State) feedInsert(key string) bool {
	if key == "esc" {
		s.Mode = ModeNormal
		s.clampNormal()
		// On esc, vim moves the cursor back one char (you exit at the
		// position you were about to type into, but NORMAL conventions
		// sit on a real char).
		if s.Pos > 0 && s.Pos == len(s.Buf) {
			s.Pos--
		}
		return true
	}
	// Everything else: let the host editor handle it.
	return false
}

// ─── NORMAL mode ─────────────────────────────────────────────

func (s *State) feedNormal(key string) bool {
	// Numeric count prefix — except '0' which is a motion when no
	// count is in flight.
	if len(key) == 1 && key[0] >= '0' && key[0] <= '9' {
		if key == "0" && s.pending.count == 0 {
			// fall through to motion handler
		} else {
			s.pending.count = s.pending.count*10 + int(key[0]-'0')
			if s.pending.count > 100000 {
				s.pending.count = 100000
			}
			return true
		}
	}

	// 'g' prefix (gg goes to start; we only support 'gg').
	if s.pending.gPending {
		s.pending.gPending = false
		if key == "g" {
			s.Pos = 0
			s.clampNormal()
			s.pending = pendingCmd{}
			return true
		}
		// any other char after 'g' aborts.
		s.pending = pendingCmd{}
		return true
	}

	// Operator pending: next key is treated as a motion.
	if s.pending.operator != 0 {
		return s.applyOperatorMotion(key)
	}

	count := s.pending.count
	if count == 0 {
		count = 1
	}

	// Reset semantics: most commands "complete" and need pending
	// cleared; a few ("g", "d", "c", "y") preserve / set pending so
	// the next key continues the parse. Each branch is explicit.
	clearPending := func() { s.pending = pendingCmd{} }

	switch key {
	// Mode transitions ─────────────────
	case "i":
		s.Mode = ModeInsert
		clearPending()
		return true
	case "I":
		s.Pos = motionCaret(s.Buf, s.Pos, 1)
		s.Mode = ModeInsert
		clearPending()
		return true
	case "a":
		if len(s.Buf) > 0 {
			s.Pos++
		}
		s.clampInsert()
		s.Mode = ModeInsert
		clearPending()
		return true
	case "A":
		s.Pos = len(s.Buf)
		s.Mode = ModeInsert
		clearPending()
		return true

	// Motions ───────────────────────────
	case "h", "left":
		s.Pos = motionH(s.Buf, s.Pos, count)
		clearPending()
		return true
	case "l", "right":
		s.Pos = motionL(s.Buf, s.Pos, count)
		clearPending()
		return true
	case "0":
		s.Pos = motion0(s.Buf, s.Pos, count)
		clearPending()
		return true
	case "$":
		s.Pos = motionDollar(s.Buf, s.Pos, count)
		clearPending()
		return true
	case "^":
		s.Pos = motionCaret(s.Buf, s.Pos, count)
		clearPending()
		return true
	case "w":
		s.Pos = motionW(s.Buf, s.Pos, count)
		clearPending()
		return true
	case "b":
		s.Pos = motionB(s.Buf, s.Pos, count)
		clearPending()
		return true
	case "e":
		s.Pos = motionE(s.Buf, s.Pos, count)
		clearPending()
		return true
	case "g":
		// Arm 'g'-pending; preserve count for an upcoming 'gg'.
		s.pending = pendingCmd{gPending: true, count: count}
		return true
	case "G":
		// Single-line buffer: G == $.
		s.Pos = motionDollar(s.Buf, s.Pos, 1)
		clearPending()
		return true

	// Direct edits ──────────────────────
	case "x":
		for i := 0; i < count && s.Pos < len(s.Buf); i++ {
			s.Register = string(s.Buf[s.Pos])
			s.Buf = append(s.Buf[:s.Pos], s.Buf[s.Pos+1:]...)
		}
		s.clampNormal()
		clearPending()
		return true
	case "X":
		for i := 0; i < count && s.Pos > 0; i++ {
			s.Pos--
			s.Register = string(s.Buf[s.Pos])
			s.Buf = append(s.Buf[:s.Pos], s.Buf[s.Pos+1:]...)
		}
		s.clampNormal()
		clearPending()
		return true
	case "D":
		s.Register = string(s.Buf[s.Pos:])
		s.Buf = s.Buf[:s.Pos]
		s.clampNormal()
		clearPending()
		return true
	case "C":
		s.Register = string(s.Buf[s.Pos:])
		s.Buf = s.Buf[:s.Pos]
		s.clampInsert()
		s.Mode = ModeInsert
		clearPending()
		return true
	case "p":
		insertAt := s.Pos + 1
		if len(s.Buf) == 0 {
			insertAt = 0
		}
		if insertAt > len(s.Buf) {
			insertAt = len(s.Buf)
		}
		s.Buf = insertRunes(s.Buf, insertAt, []rune(s.Register))
		s.Pos = insertAt + len([]rune(s.Register)) - 1
		s.clampNormal()
		clearPending()
		return true
	case "P":
		s.Buf = insertRunes(s.Buf, s.Pos, []rune(s.Register))
		s.Pos = s.Pos + len([]rune(s.Register)) - 1
		s.clampNormal()
		clearPending()
		return true

	// Operators ─────────────────────────
	case "d", "c", "y":
		s.pending = pendingCmd{operator: key[0], count: count}
		return true

	// Misc ──────────────────────────────
	case "esc":
		clearPending()
		return true
	}
	clearPending()
	return false
}

// applyOperatorMotion handles the second half of an operator+motion
// pair (dw, cw, yw, dd, cc, yy, …). count was captured when the
// operator started.
func (s *State) applyOperatorMotion(key string) bool {
	op := s.pending.operator
	count := s.pending.count
	if count == 0 {
		count = 1
	}
	defer func() { s.pending = pendingCmd{} }()

	// Doubled operator → whole-line. Single-line buffer: same as
	// 0 → $ inclusive, i.e. clear the buffer.
	if (op == 'd' && key == "d") ||
		(op == 'c' && key == "c") ||
		(op == 'y' && key == "y") {
		s.Register = string(s.Buf)
		if op != 'y' {
			s.Buf = s.Buf[:0]
			s.Pos = 0
		}
		if op == 'c' {
			s.Mode = ModeInsert
		}
		return true
	}

	// Resolve motion to target index.
	target, exclusive, ok := s.resolveMotion(key, count)
	if !ok {
		return true
	}

	from, to := s.Pos, target
	if from > to {
		from, to = to, from
	}
	if !exclusive {
		// Inclusive motions like 'e', '$' include the destination char.
		if to < len(s.Buf) {
			to++
		}
	}
	if from < 0 {
		from = 0
	}
	if to > len(s.Buf) {
		to = len(s.Buf)
	}

	s.Register = string(s.Buf[from:to])
	if op != 'y' {
		s.Buf = append(append([]rune{}, s.Buf[:from]...), s.Buf[to:]...)
		s.Pos = from
	}
	switch op {
	case 'c':
		s.Mode = ModeInsert
	case 'd', 'y':
		s.clampNormal()
	}
	return true
}

// resolveMotion runs a motion key against the current state and
// returns (target, exclusive, ok). Exclusive motions stop *before*
// the destination char; inclusive motions include it.
func (s *State) resolveMotion(key string, count int) (int, bool, bool) {
	switch key {
	case "h":
		return motionH(s.Buf, s.Pos, count), true, true
	case "l":
		return motionL(s.Buf, s.Pos, count), true, true
	case "0":
		return motion0(s.Buf, s.Pos, count), true, true
	case "$":
		return motionDollar(s.Buf, s.Pos, count), false, true
	case "^":
		return motionCaret(s.Buf, s.Pos, count), true, true
	case "w":
		return motionW(s.Buf, s.Pos, count), true, true
	case "b":
		return motionB(s.Buf, s.Pos, count), true, true
	case "e":
		return motionE(s.Buf, s.Pos, count), false, true
	}
	return s.Pos, false, false
}

// insertRunes returns a new slice with `ins` placed at `at` inside
// `dst`. Allocates because vim mutations are infrequent and the
// extra alloc isn't measurable.
func insertRunes(dst []rune, at int, ins []rune) []rune {
	out := make([]rune, 0, len(dst)+len(ins))
	out = append(out, dst[:at]...)
	out = append(out, ins...)
	out = append(out, dst[at:]...)
	return out
}
