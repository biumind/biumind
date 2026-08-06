package repl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// /plugin (no args) on a fresh home with no plugins prints a guide.
func TestSlashPlugin_emptyHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	got := handlePluginList(home)
	if !strings.Contains(got, "none installed") {
		t.Errorf("empty home: want 'none installed' guide, got %q", got)
	}
}

// /plugin lists discovered plugins with ✓/✗ markers.
func TestSlashPlugin_listShowsEnabledFlag(t *testing.T) {
	home := pluginEnv(t, map[string]string{
		"keep/.claude-plugin/plugin.json": `{"name":"keep","version":"1","author":{"name":"a"}}`,
		"skip/.claude-plugin/plugin.json": `{"name":"skip","version":"1","author":{"name":"a"}}`,
	}, `{"plugins":{"disabled":["skip"]}}`)

	got := handlePluginList(home)
	if !strings.Contains(got, "✓") || !strings.Contains(got, "keep") {
		t.Errorf("expected '✓ keep' row, got %q", got)
	}
	if !strings.Contains(got, "✗") || !strings.Contains(got, "skip") {
		t.Errorf("expected '✗ skip' row, got %q", got)
	}
}

// /plugin <name> drills into a single plugin.
func TestSlashPlugin_showHit(t *testing.T) {
	home := pluginEnv(t, map[string]string{
		"focus/.claude-plugin/plugin.json": `{
			"name":"focus","version":"2.1.0","author":{"name":"BiuMind"},
			"description":"Stay focused"
		}`,
	}, "")
	got := handlePluginShow(home, "focus")
	for _, want := range []string{"focus", "v2.1.0", "BiuMind", "Stay focused"} {
		if !strings.Contains(got, want) {
			t.Errorf("show output missing %q; got %q", want, got)
		}
	}
}

// /plugin <name> on a missing name returns a helpful error listing
// installed plugins.
func TestSlashPlugin_showMissing(t *testing.T) {
	home := pluginEnv(t, map[string]string{
		"alpha/.claude-plugin/plugin.json": `{"name":"alpha","version":"1","author":{"name":"a"}}`,
	}, "")
	got := handlePluginShow(home, "nope")
	if !strings.Contains(got, "not found") {
		t.Errorf("want 'not found', got %q", got)
	}
	if !strings.Contains(got, "alpha") {
		t.Errorf("want list of installed names ('alpha'), got %q", got)
	}
}

// /plugin disable persists to settings.json and surfaces a hooks-hint
// when the target plugin contributes hooks.
func TestSlashPlugin_disableWithHooksHint(t *testing.T) {
	home := pluginEnv(t, map[string]string{
		"hookplug/.claude-plugin/plugin.json": `{
			"name":"hookplug","version":"1","author":{"name":"a"},
			"hooks": { "PreToolUse": [{"hooks":[{"type":"command","command":"echo"}]}] }
		}`,
	}, "")

	got := handlePluginToggle(false, "hookplug")
	if !strings.Contains(got, "added to settings.disabled") {
		t.Errorf("want disable confirmation, got %q", got)
	}
	if !strings.Contains(got, "restart biu") {
		t.Errorf("hooks hint missing; got %q", got)
	}

	// Verify settings.json reflects the change.
	data, err := os.ReadFile(filepath.Join(home, ".biumind", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"hookplug"`) {
		t.Errorf("settings.json missing hookplug; got %s", data)
	}
}

// /plugin enable removes from disabled list.
func TestSlashPlugin_enableRemovesFromDisabled(t *testing.T) {
	home := pluginEnv(t, map[string]string{
		"x/.claude-plugin/plugin.json": `{"name":"x","version":"1","author":{"name":"a"}}`,
	}, `{"plugins":{"disabled":["x"]}}`)

	got := handlePluginToggle(true, "x")
	if !strings.Contains(got, "removed from settings.disabled") {
		t.Errorf("want enable confirmation, got %q", got)
	}
	data, _ := os.ReadFile(filepath.Join(home, ".biumind", "settings.json"))
	if strings.Contains(string(data), `"x"`) {
		t.Errorf("settings.json still has 'x' in disabled list; got %s", data)
	}
}

// /plugin disable on a typo'd name still writes settings (idempotent
// add) but surfaces a hint that the name doesn't match any installed
// plugin — so the user can correct it.
func TestSlashPlugin_disableTypoSurfacesHint(t *testing.T) {
	pluginEnv(t, map[string]string{
		"real/.claude-plugin/plugin.json": `{"name":"real","version":"1","author":{"name":"a"}}`,
	}, "")
	got := handlePluginToggle(false, "typo")
	if !strings.Contains(got, "no plugin with that name is installed") {
		t.Errorf("typo path should surface hint, got %q", got)
	}
}

// /plugin reload prints a stat line, no error on missing dirs.
func TestSlashPlugin_reload(t *testing.T) {
	pluginEnv(t, map[string]string{
		"a/.claude-plugin/plugin.json": `{"name":"a","version":"1","author":{"name":"a"}}`,
	}, "")
	parts := []string{"/plugin", "reload"}
	got := model{}.handlePlugin(parts)
	if !strings.Contains(got, "1 plugin(s) discovered") {
		t.Errorf("reload should report count, got %q", got)
	}
}

// Helper: build a home with the given plugin tree + optional
// settings.json content. Returns the home path. Sets HOME for the
// test so plugins.DefaultRoots resolves there. Sets the test cwd
// to home as well so project-layer search points to the same tree
// (we don't currently put a project plugin, but this keeps the test
// rig deterministic).
func pluginEnv(t *testing.T, tree map[string]string, settingsJSON string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)

	for rel, body := range tree {
		full := filepath.Join(home, ".biumind", "plugins", rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if settingsJSON != "" {
		if err := os.WriteFile(
			filepath.Join(home, ".biumind", "settings.json"),
			[]byte(settingsJSON), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Slash handlers call os.Getwd(); chdir to home so the project
	// search root resolves under our test tree (project layer just
	// checks <cwd>/.biumind/plugins/ which doesn't exist, that's fine).
	t.Chdir(home)
	return home
}
