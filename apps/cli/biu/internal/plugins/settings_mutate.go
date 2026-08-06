// Settings mutation helpers — write the user-level settings.json
// in a way that preserves unknown fields and keeps the file shape
// stable across biu versions.
//
// Lives here (rather than in internal/settings) because settings is
// a read-mostly package; adding write paths there would require
// re-architecting Layered. Plugin enable/disable is the only mutation
// site today, so we keep the surface small and local.
//
// Both `biu plugin enable/disable` (PP5) and the REPL `/plugin
// enable/disable` (PP6) call SetPluginDisabled — same atomic write,
// same field-preservation semantics, no risk of the two paths
// diverging.
package plugins

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// SetPluginDisabled toggles `name` in settings.json's plugins.disabled
// list. add=true means "ensure present"; add=false means "ensure
// absent". The function is idempotent — calling SetPluginDisabled(x,
// true) twice leaves the file unchanged on the second call.
//
// Atomic: writes to <path>.tmp then renames into place, so a crash
// mid-write doesn't leave settings.json half-written. Creates the
// parent directory if missing, so a fresh user with no
// ~/.biumind/settings.json yet works without prior setup.
//
// Round-trips through map[string]json.RawMessage to preserve every
// key biu doesn't model (skills.* / hooks / future fields). The
// plugins block itself decodes to a typed shape but its unknown
// child keys are also preserved via Extra.
func SetPluginDisabled(path, name string, add bool) error {
	if name == "" {
		return fmt.Errorf("plugin name required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	root := map[string]json.RawMessage{}
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &root); err != nil {
			return fmt.Errorf("parse existing settings: %w", err)
		}
	}

	block, extra, err := readPluginsBlock(root)
	if err != nil {
		return err
	}
	block.Disabled = applyMembership(block.Disabled, name, add)
	encoded, err := encodePluginsBlock(block, extra)
	if err != nil {
		return err
	}
	root["plugins"] = encoded

	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// pluginsBlockTyped is the local typed shape SetPluginDisabled uses
// — kept private to this file because the public PluginsBlock lives
// in internal/settings and we don't want a circular import. The
// fields match settings.PluginsBlock byte-for-byte at the JSON
// boundary, so the wire formats are identical.
type pluginsBlockTyped struct {
	Disabled []string                  `json:"disabled,omitempty"`
	Configs  map[string]map[string]any `json:"configs,omitempty"`
}

// readPluginsBlock pulls the typed shape out of the root settings
// object and also returns any unknown child keys. Both are zero
// values when no plugins block exists, so callers can use the
// result unconditionally.
func readPluginsBlock(root map[string]json.RawMessage) (pluginsBlockTyped, map[string]json.RawMessage, error) {
	var block pluginsBlockTyped
	var extra map[string]json.RawMessage
	raw, ok := root["plugins"]
	if !ok || len(raw) == 0 {
		return block, nil, nil
	}
	if err := json.Unmarshal(raw, &block); err != nil {
		return block, nil, fmt.Errorf("parse plugins block: %w", err)
	}
	var generic map[string]json.RawMessage
	if err := json.Unmarshal(raw, &generic); err == nil {
		delete(generic, "disabled")
		delete(generic, "configs")
		if len(generic) > 0 {
			extra = generic
		}
	}
	return block, extra, nil
}

// applyMembership returns a slice that contains `name` (when add) or
// doesn't contain it (when !add). Idempotent: re-adding an existing
// member is a no-op; removing an absent member is a no-op.
func applyMembership(list []string, name string, add bool) []string {
	if add {
		for _, n := range list {
			if n == name {
				return list
			}
		}
		return append(list, name)
	}
	out := list[:0]
	for _, n := range list {
		if n != name {
			out = append(out, n)
		}
	}
	return out
}

// encodePluginsBlock serialises the typed block + the carried
// unknown keys into one JSON object. Unknown keys are emitted last
// in alphabetical order via Marshal — Go map iteration is random
// but Marshal sorts string keys, so the output is deterministic.
func encodePluginsBlock(b pluginsBlockTyped, extra map[string]json.RawMessage) (json.RawMessage, error) {
	flat := map[string]json.RawMessage{}
	if len(b.Disabled) > 0 {
		raw, err := json.Marshal(b.Disabled)
		if err != nil {
			return nil, err
		}
		flat["disabled"] = raw
	}
	if len(b.Configs) > 0 {
		raw, err := json.Marshal(b.Configs)
		if err != nil {
			return nil, err
		}
		flat["configs"] = raw
	}
	for k, v := range extra {
		flat[k] = v
	}
	if len(flat) == 0 {
		return json.RawMessage("{}"), nil
	}
	return json.Marshal(flat)
}
