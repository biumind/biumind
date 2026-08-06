package plugins

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCompatClaudeRoot_returnsPathWhenPresent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	target := filepath.Join(home, ".claude", "plugins")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	got := CompatClaudeRoot()
	if got != target {
		t.Errorf("got %q, want %q", got, target)
	}
}

func TestCompatClaudeRoot_emptyWhenAbsent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if got := CompatClaudeRoot(); got != "" {
		t.Errorf("absent dir should return empty, got %q", got)
	}
}

func TestCompatClaudeRoot_emptyWhenFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Create a file at the expected path — must be a directory for
	// the discovery to claim it. A symlink-loop edge case is left
	// to the os.Stat layer.
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", "plugins"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := CompatClaudeRoot(); got != "" {
		t.Errorf("file-at-path should return empty, got %q", got)
	}
}

// DefaultRoots integration: when ~/.claude/plugins/ exists, the
// compat root is appended after user/project so first-wins
// precedence still favours biu-native installs.
func TestDefaultRoots_appendsCompatLast(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Need both biu-native and compat dirs present to confirm order.
	if err := os.MkdirAll(filepath.Join(home, ".biumind", "plugins"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".claude", "plugins"), 0o755); err != nil {
		t.Fatal(err)
	}
	cwd := home
	if err := os.MkdirAll(filepath.Join(cwd, ".biumind", "plugins"), 0o755); err != nil {
		t.Fatal(err)
	}

	roots := DefaultRoots(cwd)
	if len(roots) != 3 {
		t.Fatalf("want 3 roots (user, project, compat); got %d: %+v",
			len(roots), roots)
	}
	if roots[0].Source != SrcUser {
		t.Errorf("roots[0].Source = %q, want %q", roots[0].Source, SrcUser)
	}
	if roots[1].Source != SrcProject {
		t.Errorf("roots[1].Source = %q, want %q", roots[1].Source, SrcProject)
	}
	if roots[2].Source != SrcCompat {
		t.Errorf("roots[2].Source = %q, want %q", roots[2].Source, SrcCompat)
	}
}

func TestDefaultRoots_skipsCompatWhenAbsent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".biumind", "plugins"), 0o755); err != nil {
		t.Fatal(err)
	}
	roots := DefaultRoots("")
	for _, r := range roots {
		if r.Source == SrcCompat {
			t.Errorf("compat root should not be present when ~/.claude/plugins/ doesn't exist; got %+v", r)
		}
	}
}

// LoadAll integration: a plugin under ~/.claude/plugins/ loads
// successfully when discovered via DefaultRoots, and gets the
// SrcCompat source label. Foreign manifest keys (channels) survive
// in Unrecognised without breaking the load.
func TestLoadAll_discoversreferencePluginsViaCompatRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Build a compat-style plugin under ~/.claude/plugins/.
	pluginDir := filepath.Join(home, ".claude", "plugins", "occ-style")
	if err := os.MkdirAll(filepath.Join(pluginDir, ".claude-plugin"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{
		"name": "occ-style",
		"version": "1.0.0",
		"author": { "name": "reference user" },
		"description": "imported plugin fixture",
		"channels": { "telegram": { "token": "xxx" } },
		"userConfig": { "theme": "dark" }
	}`
	if err := os.WriteFile(
		filepath.Join(pluginDir, ".claude-plugin", "plugin.json"),
		[]byte(manifest), 0o644,
	); err != nil {
		t.Fatal(err)
	}

	a := LoadAll(DefaultRoots(""), nil)
	var got *LoadedPlugin
	for _, lp := range a.Plugins {
		if lp.Manifest.Name == "occ-style" {
			got = lp
			break
		}
	}
	if got == nil {
		t.Fatalf("reference plugin not discovered; plugins=%+v errors=%+v",
			a.Plugins, a.Errors)
	}
	if got.Source != SrcCompat {
		t.Errorf("Source = %q, want %q", got.Source, SrcCompat)
	}
	for _, key := range []string{"channels", "userConfig"} {
		if _, ok := got.Manifest.Unrecognised[key]; !ok {
			t.Errorf("reference-only key %q lost from Unrecognised", key)
		}
	}
}

// First-wins precedence: same plugin name in both ~/.biumind/plugins/
// AND ~/.claude/plugins/ → user wins, compat goes to Errors.
func TestLoadAll_userOverridesCompat(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Both versions of the same name.
	pluginRoot1 := filepath.Join(home, ".biumind", "plugins", "shared", ".claude-plugin")
	pluginRoot2 := filepath.Join(home, ".claude", "plugins", "shared", ".claude-plugin")
	for _, p := range []string{pluginRoot1, pluginRoot2} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(
		filepath.Join(pluginRoot1, "plugin.json"),
		[]byte(`{"name":"shared","version":"1","author":{"name":"a"},"description":"biu-native"}`),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(pluginRoot2, "plugin.json"),
		[]byte(`{"name":"shared","version":"2","author":{"name":"a"},"description":"reference"}`),
		0o644,
	); err != nil {
		t.Fatal(err)
	}

	a := LoadAll(DefaultRoots(""), nil)
	var winner *LoadedPlugin
	for _, lp := range a.Plugins {
		if lp.Manifest.Name == "shared" {
			winner = lp
		}
	}
	if winner == nil {
		t.Fatal("shared plugin not loaded at all")
	}
	if winner.Source != SrcUser {
		t.Errorf("biu-native should win; got Source=%q desc=%q",
			winner.Source, winner.Manifest.Description)
	}
}
