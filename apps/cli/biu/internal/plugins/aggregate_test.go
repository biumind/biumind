package plugins

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/agents"
	"github.com/biumind/biumind/apps/cli/biu/internal/commands"
	"github.com/biumind/biumind/apps/cli/biu/internal/hooks"
	"github.com/biumind/biumind/apps/cli/biu/internal/output"
	"github.com/biumind/biumind/apps/cli/biu/internal/skills"
)

// ─── LoadAll: discovery & errors ──────────────────────────────

func TestLoadAll_emptyRoots(t *testing.T) {
	a := LoadAll(nil, nil)
	if a == nil {
		t.Fatal("nil result")
	}
	if len(a.Plugins) != 0 || len(a.Errors) != 0 {
		t.Errorf("empty roots: plugins=%d errors=%d", len(a.Plugins), len(a.Errors))
	}
}

func TestLoadAll_missingRootIgnored(t *testing.T) {
	a := LoadAll([]SearchRoot{{Path: "/no/such/path"}}, nil)
	if len(a.Errors) != 0 {
		t.Errorf("missing root should be silent: %+v", a.Errors)
	}
}

func TestLoadAll_loadsTwoPlugins(t *testing.T) {
	root := mkRoot(t, map[string]string{
		"alpha/.claude-plugin/plugin.json": `{"name":"alpha","version":"1","author":{"name":"a"}}`,
		"beta/.claude-plugin/plugin.json":  `{"name":"beta","version":"1","author":{"name":"b"}}`,
	})

	a := LoadAll([]SearchRoot{{Path: root, Source: SrcUser}}, nil)
	if len(a.Plugins) != 2 {
		t.Fatalf("want 2 plugins, got %d (errors=%+v)", len(a.Plugins), a.Errors)
	}
	if a.Plugins[0].Manifest.Name != "alpha" || a.Plugins[1].Manifest.Name != "beta" {
		t.Errorf("expected sorted load order, got %s, %s",
			a.Plugins[0].Manifest.Name, a.Plugins[1].Manifest.Name)
	}
	for _, lp := range a.Plugins {
		if lp.Source != SrcUser {
			t.Errorf("Source = %q, want %q", lp.Source, SrcUser)
		}
	}
}

func TestLoadAll_collisionRecorded(t *testing.T) {
	root1 := mkRoot(t, map[string]string{
		"x/.claude-plugin/plugin.json": `{"name":"x","version":"1","author":{"name":"a"}}`,
	})
	root2 := mkRoot(t, map[string]string{
		"x/.claude-plugin/plugin.json": `{"name":"x","version":"2","author":{"name":"b"}}`,
	})

	a := LoadAll([]SearchRoot{
		{Path: root1, Source: SrcUser},
		{Path: root2, Source: SrcProject},
	}, nil)

	if len(a.Plugins) != 1 {
		t.Errorf("want 1 plugin (first wins), got %d", len(a.Plugins))
	}
	if a.Plugins[0].Source != SrcUser {
		t.Errorf("first wins on collision; got Source=%q", a.Plugins[0].Source)
	}
	if len(a.Errors) != 1 || !strings.Contains(a.Errors[0].Err.Error(), "already loaded") {
		t.Errorf("collision should be recorded, got %+v", a.Errors)
	}
}

// PP8b: extra root providers (bundled extraction is the canonical
// caller) plug onto the end of DefaultRoots so user-installed
// plugins win first-wins precedence.
func TestRegisterRootsProvider_appendsAtEnd(t *testing.T) {
	defer resetExtraRootsProviders()
	extraDir := t.TempDir()
	RegisterRootsProvider(func() []SearchRoot {
		return []SearchRoot{{Path: extraDir, Source: SrcBundled}}
	})
	roots := DefaultRoots("")
	if len(roots) == 0 {
		t.Fatal("expected at least one root from provider")
	}
	last := roots[len(roots)-1]
	if last.Path != extraDir || last.Source != SrcBundled {
		t.Errorf("provider root not last: %+v", roots)
	}
}

// Regression: nil provider doesn't crash DefaultRoots.
func TestRegisterRootsProvider_nilSkipped(t *testing.T) {
	defer resetExtraRootsProviders()
	RegisterRootsProvider(nil)
	_ = DefaultRoots("") // must not panic
}

// First-wins precedence: a user-installed plugin shadows a same-named
// bundled provider entry. PP8b unlocks bundled plugins, so confirm
// the load order makes user installs override bundled.
func TestLoadAll_userOverridesBundled(t *testing.T) {
	defer resetExtraRootsProviders()
	home := t.TempDir()
	t.Setenv("HOME", home)

	bundledDir := t.TempDir()
	mustWritePlugin(t, bundledDir, "shared", `{"name":"shared","version":"1","author":{"name":"a"},"description":"bundled"}`)
	userDir := filepath.Join(home, ".biumind", "plugins")
	mustWritePlugin(t, userDir, "shared", `{"name":"shared","version":"2","author":{"name":"a"},"description":"user"}`)

	RegisterRootsProvider(func() []SearchRoot {
		return []SearchRoot{{Path: bundledDir, Source: SrcBundled}}
	})

	a := LoadAll(DefaultRoots(""), nil)
	var winner *LoadedPlugin
	for _, lp := range a.Plugins {
		if lp.Manifest.Name == "shared" {
			winner = lp
		}
	}
	if winner == nil {
		t.Fatal("shared not loaded")
	}
	if winner.Source != SrcUser {
		t.Errorf("user should shadow bundled; got Source=%q desc=%q",
			winner.Source, winner.Manifest.Description)
	}
}

func mustWritePlugin(t *testing.T, root, name, manifest string) {
	t.Helper()
	dir := filepath.Join(root, name, ".claude-plugin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Regression for PP7: ~/.biumind/plugins/marketplaces/ is the
// marketplace cache, not a plugin directory. LoadAll must skip it
// silently so users with marketplaces don't see noise in /plugin
// list.
func TestLoadAll_skipsReservedSiblings(t *testing.T) {
	root := mkRoot(t, map[string]string{
		"real/.claude-plugin/plugin.json": `{"name":"real","version":"1","author":{"name":"a"}}`,
		// Synthesise the marketplace cache layout.
		"marketplaces/marketplaces.json":        `{"marketplaces":[]}`,
		"marketplaces/some-cached/manifest.txt": `cache`,
	})
	a := LoadAll([]SearchRoot{{Path: root}}, nil)
	if len(a.Plugins) != 1 || a.Plugins[0].Manifest.Name != "real" {
		t.Errorf("want only 'real', got %+v", a.Plugins)
	}
	for _, e := range a.Errors {
		if strings.Contains(e.Path, "marketplaces") {
			t.Errorf("marketplaces/ should be silently skipped, got error %v", e)
		}
	}
}

func TestLoadAll_brokenPluginIsolated(t *testing.T) {
	root := mkRoot(t, map[string]string{
		"good/.claude-plugin/plugin.json": `{"name":"good","version":"1","author":{"name":"a"}}`,
		"bad/.claude-plugin/plugin.json":  `{"this":"is not a valid plugin manifest"}`,
	})

	a := LoadAll([]SearchRoot{{Path: root}}, nil)

	gotNames := []string{}
	for _, lp := range a.Plugins {
		gotNames = append(gotNames, lp.Manifest.Name)
	}
	if len(a.Plugins) != 1 || gotNames[0] != "good" {
		t.Errorf("want only 'good' loaded, got %v", gotNames)
	}
	if len(a.Errors) == 0 {
		t.Error("bad plugin should be in Errors")
	}
}

func TestLoadAll_disabledFlagged(t *testing.T) {
	root := mkRoot(t, map[string]string{
		"on/.claude-plugin/plugin.json":  `{"name":"on","version":"1","author":{"name":"a"}}`,
		"off/.claude-plugin/plugin.json": `{"name":"off","version":"1","author":{"name":"a"}}`,
	})
	a := LoadAll([]SearchRoot{{Path: root}}, []string{"off"})
	if len(a.Plugins) != 2 {
		t.Fatalf("both plugins should still load: %d", len(a.Plugins))
	}
	for _, lp := range a.Plugins {
		want := lp.Manifest.Name == "on"
		if lp.Enabled != want {
			t.Errorf("%s: Enabled=%v, want %v", lp.Manifest.Name, lp.Enabled, want)
		}
	}
}

// ─── AttachAgents ────────────────────────────────────────────

func TestAttachAgents_loads(t *testing.T) {
	root := mkRoot(t, map[string]string{
		"p/.claude-plugin/plugin.json": `{"name":"p","version":"1","author":{"name":"a"}}`,
		"p/agents/explore.md": `---
name: explore
description: poke around
tools: Read, Grep
---
You explore.`,
	})
	a := LoadAll([]SearchRoot{{Path: root}}, nil)
	if len(a.Plugins) != 1 {
		t.Fatalf("setup: want 1 plugin, got %d", len(a.Plugins))
	}

	reg := agents.NewRegistry()
	a.AttachAgents(reg)
	if _, ok := reg.Lookup("explore"); !ok {
		t.Error("plugin agent not registered")
	}
}

func TestAttachAgents_disabledSkipped(t *testing.T) {
	root := mkRoot(t, map[string]string{
		"p/.claude-plugin/plugin.json": `{"name":"p","version":"1","author":{"name":"a"}}`,
		"p/agents/foo.md": `---
name: foo
description: x
---
body`,
	})
	a := LoadAll([]SearchRoot{{Path: root}}, []string{"p"})
	reg := agents.NewRegistry()
	a.AttachAgents(reg)
	if _, ok := reg.Lookup("foo"); ok {
		t.Error("disabled plugin's agent should NOT be registered")
	}
}

// ─── AttachCommands ──────────────────────────────────────────

func TestAttachCommands_loadsAndTagsSource(t *testing.T) {
	root := mkRoot(t, map[string]string{
		"p/.claude-plugin/plugin.json": `{"name":"p","version":"1","author":{"name":"a"}}`,
		"p/commands/review.md": `---
description: review pr
---
Body $ARGUMENTS`,
	})
	a := LoadAll([]SearchRoot{{Path: root}}, nil)

	reg, err := commands.Load("")
	if err != nil {
		t.Fatal(err)
	}
	a.AttachCommands(reg)

	cmd, ok := reg.Lookup("review")
	if !ok {
		t.Fatal("review command not loaded")
	}
	if string(cmd.Source) != "plugin:p" {
		t.Errorf("Source = %q, want %q", cmd.Source, "plugin:p")
	}
}

// ─── AttachSkills ────────────────────────────────────────────

func TestAttachSkills_loads(t *testing.T) {
	root := mkRoot(t, map[string]string{
		"p/.claude-plugin/plugin.json": `{"name":"p","version":"1","author":{"name":"a"}}`,
		"p/skills/foo/SKILL.md": `---
name: foo
description: example
---
body`,
	})
	a := LoadAll([]SearchRoot{{Path: root}}, nil)

	reg := skills.NewRegistry()
	a.AttachSkills(reg)
	if _, ok := reg.Lookup("foo"); !ok {
		t.Error("skill not registered")
	}
}

// ─── AttachOutputStyles ──────────────────────────────────────

func TestAttachOutputStyles_loads(t *testing.T) {
	root := mkRoot(t, map[string]string{
		"p/.claude-plugin/plugin.json": `{"name":"p","version":"1","author":{"name":"a"}}`,
		"p/output-styles/concise.md": `---
name: concise
description: terse
---
Reply briefly.`,
	})
	a := LoadAll([]SearchRoot{{Path: root}}, nil)

	reg := output.NewRegistry()
	a.AttachOutputStyles(reg)
	got := reg.Get("concise")
	if got.Name != "concise" {
		t.Errorf("style not loaded, got %+v", got)
	}
}

// ─── AttachHooks ─────────────────────────────────────────────

func TestAttachHooks_inlineMerged(t *testing.T) {
	root := mkRoot(t, map[string]string{
		"p/.claude-plugin/plugin.json": `{
			"name":"p","version":"1","author":{"name":"a"},
			"hooks": {
				"PreToolUse": [{"matcher":"Bash","hooks":[{"type":"command","command":"echo hi"}]}]
			}
		}`,
	})
	a := LoadAll([]SearchRoot{{Path: root}}, nil)

	reg := hooks.NewRegistry()
	a.AttachHooks(reg)
	got := reg.For(hooks.EventPreToolUse, "Bash")
	if len(got) == 0 {
		t.Error("plugin hook not registered")
	}
}

func TestAttachHooks_externalMergedAndExpanded(t *testing.T) {
	root := mkRoot(t, map[string]string{
		"p/.claude-plugin/plugin.json": `{"name":"p","version":"1","author":{"name":"a"}}`,
		"p/hooks/hooks.json": `{
			"description": "test",
			"hooks": {
				"Stop": [{"hooks":[{"type":"command","command":"${CLAUDE_PLUGIN_ROOT}/hooks/done.sh"}]}]
			}
		}`,
	})
	a := LoadAll([]SearchRoot{{Path: root}}, nil)
	reg := hooks.NewRegistry()
	a.AttachHooks(reg)

	got := reg.For(hooks.EventStop, "")
	if len(got) == 0 {
		t.Fatal("Stop hook not registered")
	}
	wantPath := filepath.Join(root, "p", "hooks", "done.sh")
	if !strings.Contains(got[0].Command.Command, wantPath) {
		t.Errorf("plugin-root not expanded; got %q want substring %q",
			got[0].Command.Command, wantPath)
	}
	if got[0].Source != "plugin:p" {
		t.Errorf("hook Source = %q, want plugin:p", got[0].Source)
	}
}

func TestAttachHooks_disabledSkipped(t *testing.T) {
	root := mkRoot(t, map[string]string{
		"p/.claude-plugin/plugin.json": `{
			"name":"p","version":"1","author":{"name":"a"},
			"hooks": { "Stop": [{"hooks":[{"type":"command","command":"x"}]}] }
		}`,
	})
	a := LoadAll([]SearchRoot{{Path: root}}, []string{"p"})

	reg := hooks.NewRegistry()
	a.AttachHooks(reg)
	if got := reg.For(hooks.EventStop, ""); len(got) != 0 {
		t.Errorf("disabled plugin's hooks should not register, got %d", len(got))
	}
}

// ─── McpServerConfigs ────────────────────────────────────────

func TestMcpServerConfigs_namespacedAndExpanded(t *testing.T) {
	root := mkRoot(t, map[string]string{
		"alpha/.claude-plugin/plugin.json": `{
			"name":"alpha","version":"1","author":{"name":"a"},
			"mcpServers": {
				"github": { "command": "/usr/bin/gh-mcp", "args": ["--root","${CLAUDE_PLUGIN_ROOT}"] }
			}
		}`,
		"beta/.claude-plugin/plugin.json": `{
			"name":"beta","version":"1","author":{"name":"a"},
			"mcpServers": {
				"github": { "command": "/usr/bin/other-gh-mcp" }
			}
		}`,
	})
	a := LoadAll([]SearchRoot{{Path: root}}, nil)
	cfgs := a.McpServerConfigs()
	if len(cfgs) != 2 {
		t.Fatalf("want 2 mcp configs, got %d", len(cfgs))
	}
	// Names must be namespaced so two plugins' "github" servers don't collide.
	wantNames := map[string]bool{"alpha__github": false, "beta__github": false}
	for _, c := range cfgs {
		if _, ok := wantNames[c.Name]; !ok {
			t.Errorf("unexpected namespaced name %q", c.Name)
		} else {
			wantNames[c.Name] = true
		}
		if c.PluginName == "" {
			t.Errorf("PluginName empty for %s", c.Name)
		}
	}
	for n, found := range wantNames {
		if !found {
			t.Errorf("missing config %s", n)
		}
	}
	// Verify expansion ran inside the alpha config.
	for _, c := range cfgs {
		if c.PluginName == "alpha" {
			expanded := false
			for _, arg := range c.Spec.Args {
				if strings.HasSuffix(arg, "/alpha") {
					expanded = true
				}
			}
			if !expanded {
				t.Errorf("alpha args not expanded: %v", c.Spec.Args)
			}
		}
	}
}

func TestMcpServerConfigs_disabledSkipped(t *testing.T) {
	root := mkRoot(t, map[string]string{
		"p/.claude-plugin/plugin.json": `{
			"name":"p","version":"1","author":{"name":"a"},
			"mcpServers": { "x": { "command": "/x" } }
		}`,
	})
	a := LoadAll([]SearchRoot{{Path: root}}, []string{"p"})
	if got := a.McpServerConfigs(); len(got) != 0 {
		t.Errorf("want 0 (disabled), got %d", len(got))
	}
}

// ─── AttachAll smoke ─────────────────────────────────────────

func TestAttachAll_smoke(t *testing.T) {
	root := mkRoot(t, map[string]string{
		"p/.claude-plugin/plugin.json": `{
			"name":"p","version":"1","author":{"name":"a"},
			"hooks": { "Stop": [{"hooks":[{"type":"command","command":"x"}]}] }
		}`,
		"p/agents/x.md": `---
name: x
description: y
---
body`,
		"p/commands/c.md": `---
description: cmd
---
body`,
		"p/skills/s/SKILL.md": `---
name: s
description: ok
---
body`,
		"p/output-styles/style.md": `---
name: style
description: ok
---
body`,
	})
	a := LoadAll([]SearchRoot{{Path: root}}, nil)

	agentsReg := agents.NewRegistry()
	cmdsReg, _ := commands.Load("")
	skillsReg := skills.NewRegistry()
	outReg := output.NewRegistry()
	hooksReg := hooks.NewRegistry()

	a.AttachAll(agentsReg, cmdsReg, skillsReg, outReg, hooksReg)

	if _, ok := agentsReg.Lookup("x"); !ok {
		t.Error("agent missing")
	}
	if _, ok := cmdsReg.Lookup("c"); !ok {
		t.Error("command missing")
	}
	if _, ok := skillsReg.Lookup("s"); !ok {
		t.Error("skill missing")
	}
	if outReg.Get("style").Name != "style" {
		t.Error("output style missing")
	}
	if got := hooksReg.For(hooks.EventStop, ""); len(got) != 1 {
		t.Errorf("hook count = %d, want 1", len(got))
	}
}

// ─── helpers ────────────────────────────────────────────────

// mkRoot builds a temporary plugin search root with the supplied
// file tree relative to the root. Returns the absolute root path.
func mkRoot(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, body := range files {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}
