package keybindings

// DefaultBindings returns biu's stock key map. Kept small + opinionated:
// most user-visible actions get one chord, no aliases at the default
// layer (users add aliases in their config).
//
// Chords here MUST parse cleanly — TestDefaultsParse enforces it.
func DefaultBindings() []Binding {
	raw := []struct {
		chord  string
		action Action
	}{
		{"enter", ActionSubmit},
		{"alt+enter", ActionInsertNewline},
		{"escape", ActionCancel},
		{"ctrl+c", ActionExit},
		{"ctrl+l", ActionClear},
		{"ctrl+r", ActionCompact},
		{"up", ActionHistoryPrev},
		{"down", ActionHistoryNext},
		{"ctrl+p", ActionHistoryPrev},
		{"ctrl+n", ActionHistoryNext},
		{"tab", ActionAcceptSuggest},
		{"ctrl+e", ActionExpandPreview},
		{"ctrl+v", ActionPasteImage},
		// reserved space for /voice + /theme toggles via slash, not chord.
	}
	out := make([]Binding, 0, len(raw))
	for _, r := range raw {
		c, err := ParseChord(r.chord)
		if err != nil {
			// Bug in the table — we want the test to scream.
			panic("defaultBindings parse: " + r.chord + ": " + err.Error())
		}
		out = append(out, Binding{Chord: c, Action: r.action, Source: "default"})
	}
	return out
}
