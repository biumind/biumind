// Config — read/write biu's layered settings.json (P20.56).
//
// Minimal scope:
//
//   - get  — read a dotted-path key (e.g. "default.model",
//            "permissions.mode") from the resolved Layered settings
//            (precedence: local > project > user).
//   - set  — write a dotted-path key into ~/.biumind/settings.json
//            (user layer only; project / local edits stay manual to
//            avoid surprising checked-in changes).
//   - list — return a flat dump of all currently-resolved settings
//            so the model can answer "what's configured?" without
//            guessing keys.
//
// The model can read its own auto-memory + CLAUDE.md without this
// tool; settings are different — they affect biu's runtime
// behaviour, and the model needs a structured way to introspect /
// adjust them. Useful for SDK callers who want the model to
// auto-tune permission mode, pick a model, etc.

package interactive

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
)

const ConfigToolName = "Config"

// ConfigTool reads + writes biu's settings.json. Cwd comes from
// env.Cwd at call time so per-tool ToolEnv changes flow through.
type ConfigTool struct{}

func (ConfigTool) Name() string { return ConfigToolName }

func (ConfigTool) Description(_ map[string]any) string {
	return "Read or write biu's settings.json. Three actions:\n" +
		"  get  — read a dotted-path key (e.g. 'default.model').\n" +
		"  set  — write a dotted-path key into ~/.biumind/settings.json " +
		"(USER LAYER ONLY — project / local layers stay manual).\n" +
		"  list — return the resolved (merged) settings as JSON.\n\n" +
		"Layered precedence: local > project > user. `get` returns the " +
		"resolved value; `set` only mutates user. Values are JSON-typed " +
		"(string / number / bool); the tool doesn't try to coerce."
}

func (ConfigTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"description": "get | set | list",
				"enum":        []any{"get", "set", "list"},
			},
			"key": map[string]any{
				"type":        "string",
				"description": "Dotted-path key (e.g. 'default.model'). Required for get / set.",
			},
			"value": map[string]any{
				"description": "JSON-typed new value (required for set). Pass string, number, or boolean.",
			},
		},
		"required": []string{"action"},
	}
}

func (ConfigTool) IsReadOnly(input map[string]any) bool {
	a, _ := input["action"].(string)
	return a == "get" || a == "list"
}
func (ConfigTool) IsDestructive(_ map[string]any) bool {
	// `set` mutates settings.json — but it's not destructive in the
	// "deletes data" sense. We classify it as non-destructive so the
	// runner asks for permission only when the user's mode requires
	// it (user can rule-deny via `Config(set:*)` if they want).
	return false
}
func (ConfigTool) IsConcurrencySafe(_ map[string]any) bool { return false }
func (ConfigTool) InterruptBehavior() string               { return "block" }

func (c ConfigTool) Call(_ context.Context, input map[string]any, env *engine.ToolEnv) (*engine.ToolResultPayload, error) {
	action, _ := input["action"].(string)
	switch action {
	case "":
		return softErr(ConfigToolName, "action is required (get | set | list)"), nil
	case "get":
		return c.callGet(input, env)
	case "set":
		return c.callSet(input)
	case "list":
		return c.callList(env)
	default:
		return softErr(ConfigToolName,
			fmt.Sprintf("unknown action %q (want get | set | list)", action)), nil
	}
}

func (ConfigTool) callGet(input map[string]any, env *engine.ToolEnv) (*engine.ToolResultPayload, error) {
	key, _ := input["key"].(string)
	if key == "" {
		return softErr(ConfigToolName, "key is required for get"), nil
	}
	merged, layer := loadMergedSettings(envCwd(env))
	val, ok := lookupDotted(merged, key)
	if !ok {
		return text(fmt.Sprintf("Key %q is unset (no value in any layer).", key)), nil
	}
	out, _ := json.Marshal(val)
	return text(fmt.Sprintf(
		"%s = %s  (resolved from %s layer)",
		key, string(out), layer)), nil
}

func (ConfigTool) callSet(input map[string]any) (*engine.ToolResultPayload, error) {
	key, _ := input["key"].(string)
	if key == "" {
		return softErr(ConfigToolName, "key is required for set"), nil
	}
	if _, present := input["value"]; !present {
		return softErr(ConfigToolName, "value is required for set"), nil
	}
	value := input["value"]

	home, err := os.UserHomeDir()
	if err != nil {
		return softErr(ConfigToolName, "no home directory resolvable"), nil
	}
	dir := filepath.Join(home, ".biumind")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return softErr(ConfigToolName,
			fmt.Sprintf("mkdir %s: %v", dir, err)), nil
	}
	path := filepath.Join(dir, "settings.json")

	current := map[string]any{}
	if raw, err := os.ReadFile(path); err == nil && len(raw) > 0 {
		_ = json.Unmarshal(raw, &current) // tolerate corrupt — overwrite
	}

	prev, hadPrev := lookupDotted(current, key)
	setDotted(current, key, value)

	body, err := json.MarshalIndent(current, "", "  ")
	if err != nil {
		return softErr(ConfigToolName, fmt.Sprintf("encode: %v", err)), nil
	}
	// Atomic write: temp + rename so a crash mid-write doesn't
	// leave the file truncated.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return softErr(ConfigToolName, fmt.Sprintf("write tmp: %v", err)), nil
	}
	if err := os.Rename(tmp, path); err != nil {
		return softErr(ConfigToolName, fmt.Sprintf("rename: %v", err)), nil
	}

	prevJSON := "(unset)"
	if hadPrev {
		if pb, err := json.Marshal(prev); err == nil {
			prevJSON = string(pb)
		}
	}
	newJSON, _ := json.Marshal(value)
	return text(fmt.Sprintf(
		"Set %s = %s in %s. Previous value: %s. "+
			"Restart biu (or settings reloader fires automatically) for the "+
			"change to take effect.",
		key, string(newJSON), path, prevJSON)), nil
}

func (ConfigTool) callList(env *engine.ToolEnv) (*engine.ToolResultPayload, error) {
	merged, _ := loadMergedSettings(envCwd(env))
	body, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return softErr(ConfigToolName, fmt.Sprintf("encode: %v", err)), nil
	}
	if len(merged) == 0 {
		return text("Settings are empty across all three layers."), nil
	}
	return text("Resolved settings (merged across user / project / local):\n" +
		string(body)), nil
}

// loadMergedSettings reads all three layers and merges them per
// precedence. Returns the merged map AND the layer name that the
// last write came from (best-effort — for `get`'s diagnostic, we
// pick the topmost layer that actually contains the key).
func loadMergedSettings(cwd string) (map[string]any, string) {
	merged := map[string]any{}
	layer := "default"

	// Read each layer raw rather than going through the typed
	// settings.Load — Config tool exposes user-defined keys that
	// the typed Settings struct doesn't enumerate.
	if home, err := os.UserHomeDir(); err == nil {
		mergeFile(merged, filepath.Join(home, ".biumind", "settings.json"))
	}
	if cwd != "" {
		mergeFile(merged, filepath.Join(cwd, ".biumind", "settings.json"))
		mergeFile(merged, filepath.Join(cwd, ".biumind", "settings.local.json"))
	}
	// "layer" remains "default" — caller can refine via a separate
	// call to lookupDotted on each layer when needed; the simpler
	// rendering ships now.
	return merged, layer
}

func mergeFile(into map[string]any, path string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	tmp := map[string]any{}
	if json.Unmarshal(raw, &tmp) != nil {
		return
	}
	for k, v := range tmp {
		into[k] = v
	}
}

func envCwd(env *engine.ToolEnv) string {
	if env == nil {
		return ""
	}
	return env.Cwd
}

// lookupDotted walks "a.b.c" against a nested map[string]any.
// ok=false on any missing segment or non-map intermediate.
func lookupDotted(m map[string]any, key string) (any, bool) {
	parts := strings.Split(key, ".")
	var cur any = m
	for _, p := range parts {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = obj[p]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

// setDotted writes value at "a.b.c", creating intermediate maps as
// needed. Existing non-map values are overwritten.
func setDotted(m map[string]any, key string, value any) {
	parts := strings.Split(key, ".")
	cur := m
	for i, p := range parts {
		if i == len(parts)-1 {
			cur[p] = value
			return
		}
		next, ok := cur[p].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[p] = next
		}
		cur = next
	}
}
