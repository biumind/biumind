// Tests for the `plugins` settings block: parse + layered merge.

package settings

import (
	"reflect"
	"sort"
	"testing"
)

func TestPluginsBlock_parses(t *testing.T) {
	cwd := t.TempDir()
	t.Setenv("HOME", cwd)
	writeFile(t, cwd, ".biumind/settings.json", `{
		"plugins": {
			"disabled": ["broken-one", "noisy"],
			"configs": {
				"code-review": { "model": "haiku", "comment": true }
			}
		}
	}`)
	l, err := Load(cwd)
	if err != nil {
		t.Fatal(err)
	}
	if l.User == nil || l.User.Plugins == nil {
		t.Fatal("Plugins block missing")
	}
	got := l.User.Plugins.Disabled
	sort.Strings(got)
	if !reflect.DeepEqual(got, []string{"broken-one", "noisy"}) {
		t.Errorf("Disabled = %v", got)
	}
	cfg, ok := l.User.Plugins.Configs["code-review"]
	if !ok {
		t.Fatal("code-review config missing")
	}
	if cfg["model"] != "haiku" {
		t.Errorf("model = %v", cfg["model"])
	}
	if cfg["comment"] != true {
		t.Errorf("comment = %v", cfg["comment"])
	}
}

// MergedDisabledPlugins unions across layers (project / local can ADD
// disables on top of user, but cannot remove user-disabled entries).
func TestMergedDisabledPlugins_unionAcrossLayers(t *testing.T) {
	cwd := t.TempDir()
	t.Setenv("HOME", cwd)
	writeFile(t, cwd, ".biumind/settings.json", `{
		"plugins": { "disabled": ["a", "b"] }
	}`)
	// We need user/project distinct; reuse the writeFile path that
	// resolves project under cwd.
	writeFile(t, cwd, ".biumind/settings.local.json", `{
		"plugins": { "disabled": ["b", "c"] }
	}`)
	l, _ := Load(cwd)
	got := l.MergedDisabledPlugins()
	sort.Strings(got)
	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("MergedDisabledPlugins = %v, want %v", got, want)
	}
}

func TestMergedDisabledPlugins_nilSafe(t *testing.T) {
	var l *Layered
	if got := l.MergedDisabledPlugins(); got != nil {
		t.Errorf("nil receiver should return nil, got %v", got)
	}
}

func TestMergedDisabledPlugins_emptyWhenNoBlock(t *testing.T) {
	cwd := t.TempDir()
	t.Setenv("HOME", cwd)
	writeFile(t, cwd, ".biumind/settings.json", `{}`)
	l, _ := Load(cwd)
	if got := l.MergedDisabledPlugins(); len(got) != 0 {
		t.Errorf("no plugins block → empty, got %v", got)
	}
}

// Project layer can disable additional plugins on top of user; the
// reverse (project trying to re-enable a user-disabled plugin) is
// not modelled here because there's no "enabled" field — user has
// the floor by declaring disable, project can only extend.
func TestMergedDisabledPlugins_projectExtendsUser(t *testing.T) {
	cwd := t.TempDir()
	t.Setenv("HOME", cwd)
	writeFile(t, cwd, ".biumind/settings.json", `{"plugins":{"disabled":["a"]}}`)
	writeFile(t, cwd, ".biumind/settings.local.json", `{"plugins":{"disabled":["b"]}}`)
	l, _ := Load(cwd)
	got := l.MergedDisabledPlugins()
	sort.Strings(got)
	if !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("got %v, want [a b]", got)
	}
}

// MergedPluginConfig: later layer keys override earlier.
func TestMergedPluginConfig_localWins(t *testing.T) {
	cwd := t.TempDir()
	t.Setenv("HOME", cwd)
	writeFile(t, cwd, ".biumind/settings.json", `{
		"plugins": { "configs": { "x": { "k": "user", "u": 1 } } }
	}`)
	writeFile(t, cwd, ".biumind/settings.local.json", `{
		"plugins": { "configs": { "x": { "k": "local", "l": 2 } } }
	}`)
	l, _ := Load(cwd)
	cfg := l.MergedPluginConfig("x")
	if cfg == nil {
		t.Fatal("config nil")
	}
	if cfg["k"] != "local" {
		t.Errorf("k = %v, want 'local'", cfg["k"])
	}
	// Both u (user) and l (local) should be preserved.
	if _, ok := cfg["u"]; !ok {
		t.Error("u (user-only key) should survive")
	}
	if _, ok := cfg["l"]; !ok {
		t.Error("l (local-only key) should survive")
	}
}

// nil result distinguishes "no config supplied" from "empty config" —
// callers can use it to fall back to manifest defaults.
func TestMergedPluginConfig_nilWhenAbsent(t *testing.T) {
	cwd := t.TempDir()
	t.Setenv("HOME", cwd)
	writeFile(t, cwd, ".biumind/settings.json", `{}`)
	l, _ := Load(cwd)
	if got := l.MergedPluginConfig("missing"); got != nil {
		t.Errorf("absent plugin → nil, got %v", got)
	}
}

// Empty plugin name is rejected by the merger; defensive check.
func TestMergedPluginConfig_emptyName(t *testing.T) {
	cwd := t.TempDir()
	t.Setenv("HOME", cwd)
	writeFile(t, cwd, ".biumind/settings.json", `{
		"plugins": { "configs": { "x": { "k": "v" } } }
	}`)
	l, _ := Load(cwd)
	if got := l.MergedPluginConfig(""); got != nil {
		t.Errorf("empty plugin name should return nil, got %v", got)
	}
}
