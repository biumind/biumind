package plugins

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

// readSettings is a tiny test helper — reads the settings.json
// produced by SetPluginDisabled and decodes the plugins block in
// the same shape the runtime uses.
func readSettings(t *testing.T, path string) (disabled []string, configs map[string]map[string]any, extra map[string]any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	rawPlugins, _ := root["plugins"].(map[string]any)
	if rawPlugins == nil {
		return nil, nil, nil
	}
	if d, ok := rawPlugins["disabled"].([]any); ok {
		for _, v := range d {
			disabled = append(disabled, v.(string))
		}
	}
	if c, ok := rawPlugins["configs"].(map[string]any); ok {
		configs = map[string]map[string]any{}
		for k, v := range c {
			configs[k], _ = v.(map[string]any)
		}
	}
	extra = map[string]any{}
	for k, v := range rawPlugins {
		if k == "disabled" || k == "configs" {
			continue
		}
		extra[k] = v
	}
	return
}

// ─── happy paths ─────────────────────────────────────────────

func TestSetPluginDisabled_addToFreshFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := SetPluginDisabled(path, "noisy", true); err != nil {
		t.Fatal(err)
	}
	got, _, _ := readSettings(t, path)
	if !reflect.DeepEqual(got, []string{"noisy"}) {
		t.Errorf("disabled = %v", got)
	}
}

func TestSetPluginDisabled_idempotentAdd(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	for i := 0; i < 3; i++ {
		if err := SetPluginDisabled(path, "x", true); err != nil {
			t.Fatal(err)
		}
	}
	got, _, _ := readSettings(t, path)
	if len(got) != 1 || got[0] != "x" {
		t.Errorf("idempotent add failed; got %v", got)
	}
}

func TestSetPluginDisabled_removeWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := SetPluginDisabled(path, "ghost", false); err != nil {
		t.Fatal(err)
	}
	got, _, _ := readSettings(t, path)
	if len(got) != 0 {
		t.Errorf("removing absent should be no-op, got %v", got)
	}
}

func TestSetPluginDisabled_addThenRemove(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := SetPluginDisabled(path, "x", true); err != nil {
		t.Fatal(err)
	}
	if err := SetPluginDisabled(path, "x", false); err != nil {
		t.Fatal(err)
	}
	got, _, _ := readSettings(t, path)
	if len(got) != 0 {
		t.Errorf("after remove, disabled = %v", got)
	}
}

// ─── field preservation ──────────────────────────────────────

func TestSetPluginDisabled_preservesUnrelatedSettings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	original := `{
		"model": "claude-sonnet-4-6",
		"permissions": { "allow": ["Bash(npm:*)"] },
		"plugins": { "configs": { "x": { "k": "v" } } }
	}`
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SetPluginDisabled(path, "noisy", true); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(path)
	var root map[string]any
	_ = json.Unmarshal(data, &root)

	if root["model"] != "claude-sonnet-4-6" {
		t.Errorf("model field lost; got %v", root["model"])
	}
	perms, ok := root["permissions"].(map[string]any)
	if !ok || perms["allow"] == nil {
		t.Errorf("permissions field lost; got %v", root["permissions"])
	}
	_, configs, _ := readSettings(t, path)
	if configs["x"]["k"] != "v" {
		t.Errorf("plugins.configs lost; got %+v", configs)
	}
}

func TestSetPluginDisabled_preservesUnknownPluginsKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	// `autoupdate` and `marketplaces` are forward-compatible keys
	// biu doesn't model yet — they MUST round-trip.
	original := `{
		"plugins": {
			"disabled": ["a"],
			"autoupdate": true,
			"marketplaces": [{"name":"official","url":"https://x"}]
		}
	}`
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SetPluginDisabled(path, "b", true); err != nil {
		t.Fatal(err)
	}

	disabled, _, extra := readSettings(t, path)
	sort.Strings(disabled)
	if !reflect.DeepEqual(disabled, []string{"a", "b"}) {
		t.Errorf("disabled = %v", disabled)
	}
	if extra["autoupdate"] != true {
		t.Errorf("autoupdate field lost; extras = %v", extra)
	}
	if extra["marketplaces"] == nil {
		t.Errorf("marketplaces lost; extras = %v", extra)
	}
}

// ─── atomicity / errors ──────────────────────────────────────

func TestSetPluginDisabled_atomicWriteCleanup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := SetPluginDisabled(path, "x", true); err != nil {
		t.Fatal(err)
	}
	// No .tmp file should remain after a successful write.
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf(".tmp file lingered: %v", err)
	}
}

func TestSetPluginDisabled_emptyName(t *testing.T) {
	if err := SetPluginDisabled("/tmp/x", "", true); err == nil {
		t.Error("empty name should error")
	}
}

func TestSetPluginDisabled_corruptExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	_ = os.WriteFile(path, []byte("{not json"), 0o644)
	if err := SetPluginDisabled(path, "x", true); err == nil {
		t.Error("corrupt file should error rather than overwrite silently")
	}
}

// Verify that toggling several plugins produces a stable, sorted-ish
// final state — order isn't guaranteed but all entries should be
// present.
func TestSetPluginDisabled_multipleAdds(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	for _, n := range []string{"a", "b", "c"} {
		if err := SetPluginDisabled(path, n, true); err != nil {
			t.Fatal(err)
		}
	}
	if err := SetPluginDisabled(path, "b", false); err != nil {
		t.Fatal(err)
	}
	got, _, _ := readSettings(t, path)
	sort.Strings(got)
	if !reflect.DeepEqual(got, []string{"a", "c"}) {
		t.Errorf("got %v, want [a c]", got)
	}
}
