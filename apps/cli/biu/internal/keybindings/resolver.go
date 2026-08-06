package keybindings

import (
	"sync"

	tea "github.com/charmbracelet/bubbletea"
)

// Resolver merges defaults + user bindings + plugin bindings into
// a single lookup table, then resolves incoming key events to the
// action the user expects.
//
// User bindings shadow defaults: if both bind ctrl+l, the user's
// action wins. Plugin bindings sit in between — they override
// defaults but lose to the user.
//
// Resolver also tracks chord state. If a 2-key chord has fired its
// first keystroke, the next keypress is matched only against the
// pending continuations; everything else clears the pending state.
type Resolver struct {
	mu       sync.Mutex
	bindings []Binding
	pending  Keystroke
	hasPend  bool
}

// NewResolver builds a Resolver from layered binding sources. Order
// is "least authoritative first" — defaults, then plugin, then user.
func NewResolver(layers ...[]Binding) *Resolver {
	merged := mergeLayers(layers...)
	return &Resolver{bindings: merged}
}

// Bindings returns the effective binding table for inspection (e.g.
// /keybindings command rendering). Caller must not mutate.
func (r *Resolver) Bindings() []Binding {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Binding, len(r.bindings))
	copy(out, r.bindings)
	return out
}

// Resolve takes a tea.KeyMsg and returns the action it triggers, if
// any. For 2-key chords, the first keystroke returns ("", false) and
// arms the resolver; the second keystroke returns the action.
//
// Returning ("", false) is the "no match" path; the caller falls
// through to default key handling (typing into the prompt, etc.).
func (r *Resolver) Resolve(msg tea.KeyMsg) (Action, bool) {
	if r == nil {
		return "", false
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.hasPend {
		// We're waiting for the second keystroke. Match only against
		// chords whose prefix equals the pending stroke.
		for _, b := range r.bindings {
			if len(b.Chord) != 2 {
				continue
			}
			if !keystrokeEqual(b.Chord[0], r.pending) {
				continue
			}
			if MatchesKeystroke(msg, b.Chord[1]) {
				r.hasPend = false
				return b.Action, true
			}
		}
		// Pending state expires on the first non-matching keypress.
		r.hasPend = false
		// Fall through: this keypress also gets a fresh single-key match.
	}

	// Single-key match.
	for _, b := range r.bindings {
		if len(b.Chord) != 1 {
			continue
		}
		if MatchesKeystroke(msg, b.Chord[0]) {
			return b.Action, true
		}
	}

	// Could be the start of a 2-key chord — arm.
	for _, b := range r.bindings {
		if len(b.Chord) < 2 {
			continue
		}
		if MatchesKeystroke(msg, b.Chord[0]) {
			r.pending = b.Chord[0]
			r.hasPend = true
			return "", false
		}
	}

	return "", false
}

// Pending reports whether the resolver is mid-chord. /keybindings
// can render a hint when true.
func (r *Resolver) Pending() bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.hasPend
}

// Reset clears any pending chord state. Useful after an unrelated
// async event invalidates the chord context (e.g. focus change).
func (r *Resolver) Reset() {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.hasPend = false
	r.mu.Unlock()
}

// mergeLayers folds binding lists in order. Later layers override
// earlier ones for the same chord. Two bindings are "the same" when
// their chord strings match.
func mergeLayers(layers ...[]Binding) []Binding {
	idx := map[string]int{}
	out := []Binding{}
	for _, layer := range layers {
		for _, b := range layer {
			key := b.Chord.String()
			if i, ok := idx[key]; ok {
				out[i] = b
				continue
			}
			idx[key] = len(out)
			out = append(out, b)
		}
	}
	return out
}

func keystrokeEqual(a, b Keystroke) bool {
	return a.Key == b.Key &&
		a.Ctrl == b.Ctrl &&
		(a.Alt || a.Meta) == (b.Alt || b.Meta) &&
		a.Shift == b.Shift &&
		a.Super == b.Super
}

// KnownActions returns the set of actions biu recognises out of the
// box. Plugins extend this via Register.
func KnownActions() map[Action]bool {
	out := map[Action]bool{}
	for _, b := range DefaultBindings() {
		out[b.Action] = true
	}
	// Add any actions that aren't bound by default but are still
	// "known" to biu.
	out[ActionToggleVoice] = true
	out[ActionToggleTheme] = true
	return out
}
