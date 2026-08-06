package vim

import "testing"

// ─── Mode transitions ────────────────────────────────────────

func TestNew_startsInInsert(t *testing.T) {
	s := New("hello")
	if s.Mode != ModeInsert {
		t.Errorf("mode = %v, want insert", s.Mode)
	}
	if s.Pos != 5 {
		t.Errorf("pos = %d, want 5", s.Pos)
	}
}

func TestEsc_entersNormalAndPullsBack(t *testing.T) {
	s := New("hi")
	if !s.Feed("esc") {
		t.Error("esc should be consumed")
	}
	if s.Mode != ModeNormal {
		t.Errorf("mode = %v", s.Mode)
	}
	// Cursor was at end (2), should pull back to last char (1).
	if s.Pos != 1 {
		t.Errorf("pos = %d, want 1", s.Pos)
	}
}

func TestI_entersInsert(t *testing.T) {
	s := New("hi")
	s.Feed("esc")
	s.Feed("i")
	if s.Mode != ModeInsert {
		t.Errorf("i should enter INSERT, got %v", s.Mode)
	}
}

func TestA_entersInsertAfterChar(t *testing.T) {
	s := New("hi")
	s.Feed("esc")
	s.Pos = 0
	s.Feed("a")
	if s.Mode != ModeInsert || s.Pos != 1 {
		t.Errorf("a: mode=%v pos=%d, want insert/1", s.Mode, s.Pos)
	}
}

func TestCapA_entersInsertAtEnd(t *testing.T) {
	s := New("hi")
	s.Feed("esc")
	s.Pos = 0
	s.Feed("A")
	if s.Mode != ModeInsert || s.Pos != 2 {
		t.Errorf("A: mode=%v pos=%d, want insert/2", s.Mode, s.Pos)
	}
}

func TestInsertModeFallsThrough(t *testing.T) {
	s := New("")
	if s.Feed("a") {
		t.Error("INSERT mode 'a' should fall through to host editor")
	}
}

// ─── Motions ─────────────────────────────────────────────────

func TestMotion_h_l(t *testing.T) {
	s := New("hello")
	s.Feed("esc")
	if s.Pos != 4 {
		t.Fatalf("setup pos = %d", s.Pos)
	}
	s.Feed("h")
	if s.Pos != 3 {
		t.Errorf("after h, pos = %d, want 3", s.Pos)
	}
	s.Feed("l")
	if s.Pos != 4 {
		t.Errorf("after l, pos = %d, want 4", s.Pos)
	}
	s.Feed("l") // already at end-1
	if s.Pos != 4 {
		t.Errorf("clamp: pos = %d, want 4", s.Pos)
	}
}

func TestMotion_0_dollar_caret(t *testing.T) {
	s := New("  hello world")
	s.Feed("esc")
	s.Feed("0")
	if s.Pos != 0 {
		t.Errorf("0: pos = %d", s.Pos)
	}
	s.Feed("$")
	if s.Pos != 12 {
		t.Errorf("$: pos = %d, want 12", s.Pos)
	}
	s.Feed("^")
	if s.Pos != 2 {
		t.Errorf("^: pos = %d, want 2", s.Pos)
	}
}

func TestMotion_w_b_e(t *testing.T) {
	s := New("foo bar baz")
	s.Feed("esc")
	s.Pos = 0
	s.Feed("w")
	if s.Pos != 4 {
		t.Errorf("w: pos = %d, want 4", s.Pos)
	}
	s.Feed("e")
	if s.Pos != 6 {
		t.Errorf("e: pos = %d, want 6", s.Pos)
	}
	s.Feed("b")
	if s.Pos != 4 {
		t.Errorf("b: pos = %d, want 4", s.Pos)
	}
}

func TestMotion_count(t *testing.T) {
	s := New("a b c d e")
	s.Feed("esc")
	s.Pos = 0
	s.Feed("3")
	s.Feed("w")
	// "a "→2 "b "→4 "c "→6 ; 3w lands at 'd' index 6.
	if s.Pos != 6 {
		t.Errorf("3w: pos = %d, want 6", s.Pos)
	}
}

func TestMotion_gg(t *testing.T) {
	s := New("hello")
	s.Feed("esc")
	s.Feed("g")
	s.Feed("g")
	if s.Pos != 0 {
		t.Errorf("gg: pos = %d, want 0", s.Pos)
	}
}

// ─── Edits ───────────────────────────────────────────────────

func TestX_deletesUnderCursor(t *testing.T) {
	s := New("abcd")
	s.Feed("esc")
	s.Pos = 1
	s.Feed("x")
	if s.String() != "acd" {
		t.Errorf("x: got %q, want acd", s.String())
	}
	if s.Register != "b" {
		t.Errorf("register = %q, want b", s.Register)
	}
}

func TestX_count(t *testing.T) {
	s := New("abcdef")
	s.Feed("esc")
	s.Pos = 1
	s.Feed("3")
	s.Feed("x")
	if s.String() != "aef" {
		t.Errorf("3x: got %q, want aef", s.String())
	}
}

func TestCapX_deletesBefore(t *testing.T) {
	s := New("abc")
	s.Feed("esc")
	s.Pos = 2
	s.Feed("X")
	if s.String() != "ac" {
		t.Errorf("X: got %q, want ac", s.String())
	}
}

func TestD_deletesToEnd(t *testing.T) {
	s := New("hello world")
	s.Feed("esc")
	s.Pos = 5
	s.Feed("D")
	if s.String() != "hello" {
		t.Errorf("D: got %q, want hello", s.String())
	}
	if s.Register != " world" {
		t.Errorf("register = %q", s.Register)
	}
}

func TestC_changesToEndAndEntersInsert(t *testing.T) {
	s := New("hello world")
	s.Feed("esc")
	s.Pos = 5
	s.Feed("C")
	if s.String() != "hello" {
		t.Errorf("C: got %q", s.String())
	}
	if s.Mode != ModeInsert {
		t.Errorf("C should enter INSERT, got %v", s.Mode)
	}
}

// ─── Operator + motion ───────────────────────────────────────

func TestDW_deletesWord(t *testing.T) {
	s := New("foo bar baz")
	s.Feed("esc")
	s.Pos = 0
	s.Feed("d")
	s.Feed("w")
	if s.String() != "bar baz" {
		t.Errorf("dw: got %q, want 'bar baz'", s.String())
	}
}

func TestCW_changesWord(t *testing.T) {
	s := New("foo bar")
	s.Feed("esc")
	s.Pos = 0
	s.Feed("c")
	s.Feed("w")
	if s.String() != "bar" {
		t.Errorf("cw: got %q", s.String())
	}
	if s.Mode != ModeInsert {
		t.Errorf("cw should enter INSERT, got %v", s.Mode)
	}
}

func TestYW_yanksWord(t *testing.T) {
	s := New("foo bar")
	s.Feed("esc")
	s.Pos = 0
	s.Feed("y")
	s.Feed("w")
	if s.String() != "foo bar" {
		t.Errorf("yw should not modify buffer: %q", s.String())
	}
	if s.Register != "foo " {
		t.Errorf("register = %q, want 'foo '", s.Register)
	}
}

func TestDD_clearsLine(t *testing.T) {
	s := New("hello world")
	s.Feed("esc")
	s.Feed("d")
	s.Feed("d")
	if s.String() != "" {
		t.Errorf("dd: got %q", s.String())
	}
	if s.Register != "hello world" {
		t.Errorf("register = %q", s.Register)
	}
}

// ─── Paste ───────────────────────────────────────────────────

func TestP_pastesAfter(t *testing.T) {
	s := New("hello")
	s.Feed("esc")
	s.Register = "X"
	s.Pos = 0
	s.Feed("p")
	if s.String() != "hXello" {
		t.Errorf("p: got %q, want hXello", s.String())
	}
}

func TestCapP_pastesBefore(t *testing.T) {
	s := New("hello")
	s.Feed("esc")
	s.Register = "X"
	s.Pos = 0
	s.Feed("P")
	if s.String() != "Xhello" {
		t.Errorf("P: got %q, want Xhello", s.String())
	}
}

// ─── Misc ────────────────────────────────────────────────────

func TestNilSafe(t *testing.T) {
	var s *State
	if s.Feed("x") {
		t.Error("nil state should not consume keys")
	}
	if s.String() != "" {
		t.Error("nil String should be empty")
	}
	s.Reset() // must not panic
}

func TestReset_clearsPending(t *testing.T) {
	s := New("abc")
	s.Feed("esc")
	s.Feed("d") // operator pending
	s.Reset()
	if s.pending.operator != 0 {
		t.Error("Reset should clear operator")
	}
}

func TestUnknownKeyInNormalIsConsumedSilently(t *testing.T) {
	s := New("abc")
	s.Feed("esc")
	// 'z' isn't bound — should return false (not consumed) so we don't
	// pretend to handle it.
	if s.Feed("z") {
		t.Error("unbound NORMAL key should return false")
	}
}

func TestMode_string(t *testing.T) {
	if ModeInsert.String() != "insert" || ModeNormal.String() != "normal" {
		t.Error("mode strings broken")
	}
}
