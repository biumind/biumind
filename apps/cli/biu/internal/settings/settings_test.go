package settings

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/permissions"
)

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	full := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return full
}

func TestLoadAndMerge(t *testing.T) {
	cwd := t.TempDir()
	// Pretend $HOME is also under cwd so we don't pollute the
	// user's real ~/.biumind.
	t.Setenv("HOME", cwd)

	writeFile(t, cwd, ".biumind/settings.json", `{
		"permissions": {
			"allow": ["Bash(npm install)"],
			"defaultMode": "acceptEdits"
		}
	}`)
	writeFile(t, cwd, ".biumind/settings.local.json", `{
		"permissions": {
			"deny": ["Bash(rm -rf /)"],
			"defaultMode": "default"
		},
		"model": "claude-haiku-4-5-20251001"
	}`)

	l, err := Load(cwd)
	if err != nil {
		t.Fatal(err)
	}
	if l.Project == nil || l.Local == nil {
		t.Fatalf("expected project + local present; got %+v", l)
	}
	if got := l.PreferredModel(); got != "claude-haiku-4-5-20251001" {
		t.Errorf("model=%q", got)
	}

	ctx := permissions.NewContext()
	mode := l.ApplyToContext(ctx)
	if mode != permissions.ModeDefault {
		t.Errorf("local should win mode; got %v", mode)
	}

	// Allow rule from project visible.
	d, r := permissions.Decide(ctx, permissions.Request{
		Tool: "Bash", Args: map[string]any{"command": "npm install"},
	})
	if d != permissions.DecideAllow || r.Source != permissions.SrcProjectSettings {
		t.Errorf("allow from project: got %v %+v", d, r)
	}

	// Deny rule from local beats nothing else; rm -rf must deny.
	d, r = permissions.Decide(ctx, permissions.Request{
		Tool: "Bash", Args: map[string]any{"command": "rm -rf /"},
	})
	if d != permissions.DecideDeny || r.Source != permissions.SrcLocalSettings {
		t.Errorf("deny from local: got %v %+v", d, r)
	}
}

func TestLoadMissingDirOK(t *testing.T) {
	cwd := t.TempDir()
	t.Setenv("HOME", cwd)
	l, err := Load(cwd)
	if err != nil {
		t.Fatal(err)
	}
	if l.Project != nil || l.Local != nil || l.User != nil {
		t.Errorf("nothing should be loaded; got %+v", l)
	}
}

func TestLoadInvalidJSONReportsPath(t *testing.T) {
	cwd := t.TempDir()
	t.Setenv("HOME", cwd)
	writeFile(t, cwd, ".biumind/settings.json", `{not json`)
	if _, err := Load(cwd); err == nil {
		t.Errorf("expected parse error")
	}
}

func TestMergedClaudeMdExcludesDedupes(t *testing.T) {
	cwd := t.TempDir()
	t.Setenv("HOME", cwd)
	writeFile(t, cwd, ".biumind/settings.json",
		`{"claudeMdExcludes":["vendor","node_modules"]}`)
	writeFile(t, cwd, ".biumind/settings.local.json",
		`{"claudeMdExcludes":["vendor","build"]}`)
	l, _ := Load(cwd)
	got := l.MergedClaudeMdExcludes()
	if len(got) != 3 {
		t.Errorf("expected 3 deduped patterns; got %v", got)
	}
	for _, want := range []string{"vendor", "node_modules", "build"} {
		found := false
		for _, p := range got {
			if p == want {
				found = true
			}
		}
		if !found {
			t.Errorf("missing pattern %q in %v", want, got)
		}
	}
}

func TestMergedClaudeMdExcludesNil(t *testing.T) {
	if got := (&Layered{}).MergedClaudeMdExcludes(); got != nil {
		t.Errorf("empty layered should yield nil; got %v", got)
	}
}

func TestMergedEnvLocalWins(t *testing.T) {
	cwd := t.TempDir()
	t.Setenv("HOME", cwd)
	writeFile(t, cwd, ".biumind/settings.json", `{"env":{"X":"project","Y":"p"}}`)
	writeFile(t, cwd, ".biumind/settings.local.json", `{"env":{"X":"local"}}`)
	l, _ := Load(cwd)
	env := l.MergedEnv()
	if env["X"] != "local" || env["Y"] != "p" {
		t.Errorf("merged env wrong: %+v", env)
	}
}

func TestPreferredStatusLineNilWhenAbsent(t *testing.T) {
	cwd := t.TempDir()
	t.Setenv("HOME", cwd)
	l, _ := Load(cwd)
	if got := l.PreferredStatusLine(); got != nil {
		t.Errorf("no settings → nil; got %+v", got)
	}
}

func TestPreferredStatusLineUserLayer(t *testing.T) {
	cwd := t.TempDir()
	t.Setenv("HOME", cwd)
	writeFile(t, cwd, ".biumind/settings.json", `{
		"statusLine": {
			"type": "command",
			"command": "git rev-parse --abbrev-ref HEAD",
			"timeoutMs": 1000
		}
	}`)
	l, _ := Load(cwd)
	got := l.PreferredStatusLine()
	if got == nil {
		t.Fatal("user statusLine should be picked up")
	}
	if got.Command != "git rev-parse --abbrev-ref HEAD" {
		t.Errorf("command: got %q", got.Command)
	}
	if got.TimeoutMs != 1000 {
		t.Errorf("timeoutMs: got %d", got.TimeoutMs)
	}
}

func TestPreferredStatusLineLocalOverridesUser(t *testing.T) {
	cwd := t.TempDir()
	t.Setenv("HOME", cwd)
	writeFile(t, cwd, ".biumind/settings.json", `{
		"statusLine": {"type":"command","command":"echo user"}
	}`)
	writeFile(t, cwd, ".biumind/settings.local.json", `{
		"statusLine": {"type":"command","command":"echo local"}
	}`)
	l, _ := Load(cwd)
	got := l.PreferredStatusLine()
	if got == nil || got.Command != "echo local" {
		t.Errorf("local should win; got %+v", got)
	}
}

// Empty command at any precedence level disables — lets a project
// silence a user-level status line without deleting the home file.
func TestPreferredStatusLineEmptyCommandDisables(t *testing.T) {
	cwd := t.TempDir()
	t.Setenv("HOME", cwd)
	writeFile(t, cwd, ".biumind/settings.json", `{
		"statusLine": {"type":"command","command":"echo user"}
	}`)
	writeFile(t, cwd, ".biumind/settings.local.json", `{
		"statusLine": {"type":"command","command":""}
	}`)
	l, _ := Load(cwd)
	if got := l.PreferredStatusLine(); got != nil {
		t.Errorf("empty local command should disable; got %+v", got)
	}
}

func TestApplyToContext_AdditionalDirectoriesFromSettings(t *testing.T) {
	cwd := t.TempDir()
	t.Setenv("HOME", cwd)

	for _, d := range []string{"proj-dir", "local-dir"} {
		if err := os.MkdirAll(filepath.Join(cwd, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	writeFile(t, cwd, ".biumind/settings.json", `{
		"permissions": {
			"additionalDirectories": ["${PROJECT_ROOT}/proj-dir"]
		}
	}`)
	writeFile(t, cwd, ".biumind/settings.local.json", `{
		"permissions": {
			"additionalDirectories": ["local-dir"]
		}
	}`)

	l, err := Load(cwd)
	if err != nil {
		t.Fatal(err)
	}
	ctx := permissions.NewContext()
	l.ApplyToContext(ctx)

	got := ctx.AdditionalDirectoryPaths()
	if len(got) < 1 {
		t.Fatalf("expected at least 1 dir from settings; got %+v", got)
	}
	for _, p := range got {
		if !filepath.IsAbs(p) {
			t.Errorf("dir %q must be absolute", p)
		}
	}
	// proj-dir must appear (use ${PROJECT_ROOT} substitution)
	wantSuffix := filepath.Join(cwd, "proj-dir")
	found := false
	for _, p := range got {
		if p == wantSuffix {
			found = true
		}
	}
	if !found {
		t.Errorf("expected %q in dirs; got %+v", wantSuffix, got)
	}
}

func TestApplyToContext_RelativeDirsBecomeAbsolute(t *testing.T) {
	cwd := t.TempDir()
	t.Setenv("HOME", cwd)
	if err := os.MkdirAll(filepath.Join(cwd, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}

	writeFile(t, cwd, ".biumind/settings.local.json", `{
		"permissions": {"additionalDirectories": ["${PROJECT_ROOT}/subdir"]}
	}`)

	l, err := Load(cwd)
	if err != nil {
		t.Fatal(err)
	}
	ctx := permissions.NewContext()
	l.ApplyToContext(ctx)

	dirs := ctx.AdditionalDirectoryPaths()
	if len(dirs) != 1 {
		t.Fatalf("got %+v want 1 dir", dirs)
	}
	if !filepath.IsAbs(dirs[0]) {
		t.Errorf("dir should be absolute; got %q", dirs[0])
	}
}
