package vim

// Motion functions are pure: they read s.Buf + start position, apply
// `count` repeats, and return the new cursor. They do not mutate
// state. The state machine in state.go composes them.

// motionH moves the cursor left by count, clamped at 0.
func motionH(buf []rune, pos, count int) int {
	pos -= count
	if pos < 0 {
		return 0
	}
	return pos
}

// motionL moves right by count, clamped at len-1 (NORMAL mode rule).
func motionL(buf []rune, pos, count int) int {
	pos += count
	if pos > len(buf)-1 {
		if len(buf) == 0 {
			return 0
		}
		return len(buf) - 1
	}
	return pos
}

// motion0 jumps to column 0.
func motion0(buf []rune, pos, count int) int { return 0 }

// motionDollar jumps to end-of-line (last char in NORMAL).
func motionDollar(buf []rune, pos, count int) int {
	if len(buf) == 0 {
		return 0
	}
	return len(buf) - 1
}

// motionCaret jumps to first non-whitespace character.
func motionCaret(buf []rune, pos, count int) int {
	for i, r := range buf {
		if r != ' ' && r != '\t' {
			return i
		}
	}
	return 0
}

// motionW: next word start.
func motionW(buf []rune, pos, count int) int {
	for i := 0; i < count; i++ {
		pos = stepW(buf, pos)
	}
	return pos
}

func stepW(buf []rune, pos int) int {
	n := len(buf)
	if pos >= n-1 {
		return n - 1
	}
	// Skip current word/punct chunk, then any whitespace.
	cur := classify(buf[pos])
	for pos < n && classify(buf[pos]) == cur && cur != classWhite {
		pos++
	}
	for pos < n && classify(buf[pos]) == classWhite {
		pos++
	}
	if pos >= n {
		pos = n - 1
	}
	return pos
}

// motionB: previous word start.
func motionB(buf []rune, pos, count int) int {
	for i := 0; i < count; i++ {
		pos = stepB(buf, pos)
	}
	return pos
}

func stepB(buf []rune, pos int) int {
	if pos <= 0 {
		return 0
	}
	pos--
	for pos > 0 && classify(buf[pos]) == classWhite {
		pos--
	}
	cur := classify(buf[pos])
	for pos > 0 && classify(buf[pos-1]) == cur {
		pos--
	}
	return pos
}

// motionE: end of current/next word.
func motionE(buf []rune, pos, count int) int {
	for i := 0; i < count; i++ {
		pos = stepE(buf, pos)
	}
	return pos
}

func stepE(buf []rune, pos int) int {
	n := len(buf)
	if pos >= n-1 {
		return n - 1
	}
	pos++
	for pos < n && classify(buf[pos]) == classWhite {
		pos++
	}
	if pos >= n {
		return n - 1
	}
	cur := classify(buf[pos])
	for pos+1 < n && classify(buf[pos+1]) == cur {
		pos++
	}
	return pos
}

// classify a rune for motion purposes: word, punct, or whitespace.
type runeClass int

const (
	classWhite runeClass = iota
	classWord
	classPunct
)

func classify(r rune) runeClass {
	switch {
	case r == ' ' || r == '\t':
		return classWhite
	case isWordRune(r):
		return classWord
	}
	return classPunct
}
