package keybindings

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// UserConfigPath is where biu looks for user overrides. Honours
// $BIUMIND_HOME if set, falling back to ~/.biumind/keybindings.json.
func UserConfigPath() string {
	if dir := os.Getenv("BIUMIND_HOME"); dir != "" {
		return filepath.Join(dir, "keybindings.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".biumind", "keybindings.json")
}

// configFile is the JSON schema for keybindings.json. We accept
// either of two shapes — a flat map for simple users, or a list of
// blocks for context-scoped bindings:
//
//	{ "ctrl+l": "clear", "alt+enter": "newline" }
//	{ "bindings": [ { "context": "repl", "bindings": { "ctrl+l": "clear" } } ] }
//
// Either becomes a flat []Binding internally — biu doesn't yet split
// by context, so the context field is preserved but unused.
type configFile struct {
	Bindings []configBlock     `json:"bindings,omitempty"`
	Flat     map[string]string `json:"-"` // populated when the doc is a flat map
}

type configBlock struct {
	Context  string            `json:"context,omitempty"`
	Bindings map[string]string `json:"bindings"`
}

// LoadUserBindings reads + parses the user config. Missing file is
// not an error (returns nil, nil). Malformed JSON, unparseable
// chords, or unknown actions surface as warnings via the warn callback
// instead of failing the whole REPL — a typo shouldn't lock the user
// out.
func LoadUserBindings(path string, knownActions map[Action]bool, warn func(string)) ([]Binding, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return parseUserConfig(data, knownActions, warn)
}

func parseUserConfig(data []byte, knownActions map[Action]bool, warn func(string)) ([]Binding, error) {
	if warn == nil {
		warn = func(string) {}
	}
	// Try the flat-map shape first.
	var flat map[string]string
	if err := json.Unmarshal(data, &flat); err == nil && flat != nil {
		// Could also be an empty struct serializing as {}; treat empty
		// map as "nothing user-configured".
		return bindingsFromFlat(flat, "user", knownActions, warn), nil
	}
	// Then the structured shape.
	var doc configFile
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("keybindings.json: %w", err)
	}
	var out []Binding
	for _, blk := range doc.Bindings {
		out = append(out, bindingsFromFlat(blk.Bindings, "user", knownActions, warn)...)
	}
	return out, nil
}

func bindingsFromFlat(m map[string]string, source string, known map[Action]bool, warn func(string)) []Binding {
	out := make([]Binding, 0, len(m))
	for chordStr, actionStr := range m {
		chord, err := ParseChord(chordStr)
		if err != nil {
			warn(fmt.Sprintf("skipping %q: %s", chordStr, err))
			continue
		}
		act := Action(actionStr)
		if known != nil && len(known) > 0 && !known[act] {
			warn(fmt.Sprintf("unknown action %q for %q", actionStr, chordStr))
			// Still register: a plugin may add the action later.
		}
		out = append(out, Binding{Chord: chord, Action: act, Source: source})
	}
	return out
}
