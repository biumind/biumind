package keybindings

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// ─── Parser ──────────────────────────────────────────────────

func TestParseKeystroke_simple(t *testing.T) {
	cases := []struct {
		in   string
		want Keystroke
	}{
		{"a", Keystroke{Key: "a"}},
		{"ctrl+l", Keystroke{Key: "l", Ctrl: true}},
		{"ctrl+shift+k", Keystroke{Key: "k", Ctrl: true, Shift: true}},
		{"alt+enter", Keystroke{Key: "enter", Alt: true}},
		{"opt+enter", Keystroke{Key: "enter", Alt: true}},
		{"cmd+s", Keystroke{Key: "s", Super: true}},
		{"win+s", Keystroke{Key: "s", Super: true}},
		{"esc", Keystroke{Key: "escape"}},
		{"return", Keystroke{Key: "enter"}},
		{"space", Keystroke{Key: " "}},
		{"↑", Keystroke{Key: "up"}},
	}
	for _, tc := range cases {
		got, err := ParseKeystroke(tc.in)
		if err != nil {
			t.Errorf("ParseKeystroke(%q) err: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ParseKeystroke(%q) = %+v, want %+v", tc.in, got, tc.want)
		}
	}
}

func TestParseKeystroke_errors(t *testing.T) {
	cases := []string{"", "   ", "ctrl+", "ctrl+shift+"}
	for _, in := range cases {
		if _, err := ParseKeystroke(in); err == nil {
			t.Errorf("ParseKeystroke(%q) should error", in)
		}
	}
}

func TestParseChord_multi(t *testing.T) {
	c, err := ParseChord("ctrl+x ctrl+s")
	if err != nil {
		t.Fatal(err)
	}
	if len(c) != 2 {
		t.Errorf("len = %d, want 2", len(c))
	}
	if c[0].Key != "x" || !c[0].Ctrl {
		t.Errorf("first = %+v", c[0])
	}
	if c[1].Key != "s" || !c[1].Ctrl {
		t.Errorf("second = %+v", c[1])
	}
}

func TestParseChord_loneSpaceIsSpaceKey(t *testing.T) {
	c, err := ParseChord(" ")
	if err != nil {
		t.Fatal(err)
	}
	if len(c) != 1 || c[0].Key != " " {
		t.Errorf("lone space should parse as space key, got %+v", c)
	}
}

func TestChordString_roundTrip(t *testing.T) {
	c, _ := ParseChord("ctrl+shift+k")
	if got := c.String(); got != "ctrl+shift+k" {
		t.Errorf("String = %q, want ctrl+shift+k", got)
	}
}

// ─── Defaults ────────────────────────────────────────────────

func TestDefaultBindings_allParse(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("DefaultBindings panicked: %v", r)
		}
	}()
	bs := DefaultBindings()
	if len(bs) == 0 {
		t.Fatal("defaults should not be empty")
	}
	for _, b := range bs {
		if len(b.Chord) == 0 {
			t.Errorf("binding %q has empty chord", b.Action)
		}
		if b.Source != "default" {
			t.Errorf("source = %q, want default", b.Source)
		}
	}
}

// ─── Loader ──────────────────────────────────────────────────

func TestParseUserConfig_flatMap(t *testing.T) {
	doc := []byte(`{"ctrl+l": "clear", "alt+enter": "newline"}`)
	bs, err := parseUserConfig(doc, KnownActions(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(bs) != 2 {
		t.Errorf("len = %d, want 2", len(bs))
	}
	for _, b := range bs {
		if b.Source != "user" {
			t.Errorf("source should be user, got %q", b.Source)
		}
	}
}

func TestParseUserConfig_blockShape(t *testing.T) {
	doc := []byte(`{"bindings":[{"context":"repl","bindings":{"ctrl+r":"compact"}}]}`)
	bs, err := parseUserConfig(doc, KnownActions(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(bs) != 1 || bs[0].Action != ActionCompact {
		t.Errorf("got %+v", bs)
	}
}

func TestParseUserConfig_warnsOnUnknownAction(t *testing.T) {
	var warnings []string
	doc := []byte(`{"ctrl+q": "ragnarok"}`)
	_, err := parseUserConfig(doc, KnownActions(), func(s string) {
		warnings = append(warnings, s)
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) == 0 {
		t.Error("unknown action should warn")
	}
	if !strings.Contains(strings.Join(warnings, " "), "ragnarok") {
		t.Errorf("warning should mention bad action: %v", warnings)
	}
}

func TestParseUserConfig_warnsOnBadChord(t *testing.T) {
	var warnings []string
	doc := []byte(`{"++":"clear"}`)
	bs, err := parseUserConfig(doc, KnownActions(), func(s string) {
		warnings = append(warnings, s)
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(bs) != 0 {
		t.Errorf("bad chord should be skipped, got %+v", bs)
	}
	if len(warnings) == 0 {
		t.Error("bad chord should warn")
	}
}

func TestParseUserConfig_malformedJSON(t *testing.T) {
	_, err := parseUserConfig([]byte(`{not json`), KnownActions(), nil)
	if err == nil {
		t.Error("malformed JSON should error")
	}
}

// ─── Resolver ────────────────────────────────────────────────

func TestResolver_singleKey(t *testing.T) {
	r := NewResolver(DefaultBindings())
	act, ok := r.Resolve(tea.KeyMsg{Type: tea.KeyEsc})
	if !ok || act != ActionCancel {
		t.Errorf("escape → %v %q, want %v %q", ok, act, true, ActionCancel)
	}
}

func TestResolver_userOverridesDefault(t *testing.T) {
	user := []Binding{{Chord: mustChord(t, "ctrl+l"), Action: "custom", Source: "user"}}
	r := NewResolver(DefaultBindings(), user)
	act, ok := r.Resolve(tea.KeyMsg{Type: tea.KeyCtrlL})
	if !ok || act != "custom" {
		t.Errorf("user override should win: %v %q", ok, act)
	}
}

func TestResolver_unmappedReturnsFalse(t *testing.T) {
	r := NewResolver(DefaultBindings())
	_, ok := r.Resolve(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if ok {
		t.Error("plain 'q' should not match any default")
	}
}

func TestResolver_chordPending(t *testing.T) {
	user := []Binding{
		{Chord: mustChord(t, "ctrl+x ctrl+s"), Action: "save", Source: "user"},
	}
	r := NewResolver(user)

	// First keystroke arms.
	_, ok := r.Resolve(tea.KeyMsg{Type: tea.KeyCtrlX})
	if ok {
		t.Error("first stroke of chord should not fire")
	}
	if !r.Pending() {
		t.Error("resolver should be pending after first stroke")
	}

	// Second keystroke fires.
	act, ok := r.Resolve(tea.KeyMsg{Type: tea.KeyCtrlS})
	if !ok || act != "save" {
		t.Errorf("chord completion: %v %q", ok, act)
	}
	if r.Pending() {
		t.Error("pending should clear after completion")
	}
}

func TestResolver_chordExpiresOnNonMatch(t *testing.T) {
	user := []Binding{
		{Chord: mustChord(t, "ctrl+x ctrl+s"), Action: "save", Source: "user"},
	}
	r := NewResolver(user)
	_, _ = r.Resolve(tea.KeyMsg{Type: tea.KeyCtrlX})
	// Hit something unrelated:
	_, _ = r.Resolve(tea.KeyMsg{Type: tea.KeyEsc})
	if r.Pending() {
		t.Error("non-match should clear pending state")
	}
}

func TestResolver_resetClearsPending(t *testing.T) {
	user := []Binding{
		{Chord: mustChord(t, "ctrl+x ctrl+s"), Action: "save", Source: "user"},
	}
	r := NewResolver(user)
	_, _ = r.Resolve(tea.KeyMsg{Type: tea.KeyCtrlX})
	r.Reset()
	if r.Pending() {
		t.Error("Reset should clear pending")
	}
}

func TestResolver_nilSafe(t *testing.T) {
	var r *Resolver
	_, ok := r.Resolve(tea.KeyMsg{Type: tea.KeyEsc})
	if ok {
		t.Error("nil resolver should never match")
	}
	if r.Pending() {
		t.Error("nil resolver should not be pending")
	}
	r.Reset() // must not panic
	if r.Bindings() != nil {
		t.Error("nil resolver Bindings should be nil")
	}
}

// ─── Match ───────────────────────────────────────────────────

func TestMatchesKeystroke_basicKeys(t *testing.T) {
	cases := []struct {
		msg    tea.KeyMsg
		target string
		want   bool
	}{
		{tea.KeyMsg{Type: tea.KeyEnter}, "enter", true},
		{tea.KeyMsg{Type: tea.KeyEsc}, "escape", true},
		{tea.KeyMsg{Type: tea.KeyTab}, "tab", true},
		{tea.KeyMsg{Type: tea.KeyUp}, "up", true},
		{tea.KeyMsg{Type: tea.KeyCtrlL}, "ctrl+l", true},
		{tea.KeyMsg{Type: tea.KeyCtrlL}, "ctrl+k", false},
		{tea.KeyMsg{Type: tea.KeyEnter, Alt: true}, "alt+enter", true},
		{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}}, "a", true},
		{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'A'}}, "a", true},
	}
	for _, tc := range cases {
		ks, err := ParseKeystroke(tc.target)
		if err != nil {
			t.Fatalf("parse %q: %v", tc.target, err)
		}
		got := MatchesKeystroke(tc.msg, ks)
		if got != tc.want {
			t.Errorf("Matches(%+v, %q) = %v, want %v", tc.msg, tc.target, got, tc.want)
		}
	}
}

// ─── helpers ─────────────────────────────────────────────────

func mustChord(t *testing.T, s string) Chord {
	t.Helper()
	c, err := ParseChord(s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return c
}
