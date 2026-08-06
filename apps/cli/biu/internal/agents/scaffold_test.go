package agents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ─── name validation ────────────────────────────────────

func TestScaffoldRejectsEmptyName(t *testing.T) {
	_, err := Scaffold(ScaffoldOptions{Name: "", Home: t.TempDir()})
	if err == nil {
		t.Errorf("empty name should fail")
	}
}

func TestScaffoldRejectsInvalidCharacters(t *testing.T) {
	for _, bad := range []string{
		"has space", "starts/with/slash", "has.dot",
		"😀emoji", "1starts-with-digit",
	} {
		_, err := Scaffold(ScaffoldOptions{Name: bad, Home: t.TempDir()})
		if err == nil {
			t.Errorf("name %q should fail validation", bad)
		}
	}
}

func TestScaffoldAcceptsCanonicalNames(t *testing.T) {
	home := t.TempDir()
	for _, ok := range []string{"explore-deep", "MyAgent", "agent_v2", "x"} {
		_, err := Scaffold(ScaffoldOptions{
			Name: ok, Home: home, Force: true, // share a home so collisions don't matter
		})
		if err != nil {
			t.Errorf("name %q should pass; got %v", ok, err)
		}
	}
}

// ─── reserved-name collision ─────────────────────────────

func TestScaffoldRefusesReservedNames(t *testing.T) {
	for _, reserved := range []string{
		"Plan", "Explore", "CodeReview", "Verification", "general-purpose",
	} {
		_, err := Scaffold(ScaffoldOptions{
			Name: reserved, Home: t.TempDir(),
		})
		if err == nil {
			t.Errorf("reserved %q should fail", reserved)
		}
	}
}

// ─── scope routing ───────────────────────────────────────

func TestScaffoldUserScopeWritesUnderHome(t *testing.T) {
	home := t.TempDir()
	res, err := Scaffold(ScaffoldOptions{
		Name: "myagent", Home: home,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".biumind", "agents", "myagent.md")
	if res.Path != want {
		t.Errorf("Path: got %q, want %q", res.Path, want)
	}
	if res.Overwritten {
		t.Errorf("first write should not be Overwritten")
	}
	if _, err := os.Stat(want); err != nil {
		t.Errorf("file not written: %v", err)
	}
}

func TestScaffoldProjectScopeWritesUnderCwd(t *testing.T) {
	cwd := t.TempDir()
	res, err := Scaffold(ScaffoldOptions{
		Name:  "team-agent",
		Scope: ScopeProject,
		Cwd:   cwd,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(cwd, ".biumind", "agents", "team-agent.md")
	if res.Path != want {
		t.Errorf("Path: got %q, want %q", res.Path, want)
	}
}

func TestScaffoldProjectScopeRequiresCwd(t *testing.T) {
	_, err := Scaffold(ScaffoldOptions{
		Name:  "a", Scope: ScopeProject,
	})
	if err == nil {
		t.Errorf("project scope without cwd should fail")
	}
}

func TestScaffoldRejectsUnknownScope(t *testing.T) {
	_, err := Scaffold(ScaffoldOptions{
		Name: "a", Scope: "global",
	})
	if err == nil {
		t.Errorf("unknown scope should fail")
	}
}

// ─── overwrite behaviour ─────────────────────────────────

func TestScaffoldRefusesToOverwriteWithoutForce(t *testing.T) {
	home := t.TempDir()
	if _, err := Scaffold(ScaffoldOptions{Name: "x", Home: home}); err != nil {
		t.Fatal(err)
	}
	_, err := Scaffold(ScaffoldOptions{Name: "x", Home: home})
	if err == nil {
		t.Errorf("second create without --force should fail")
	}
	if !strings.Contains(err.Error(), "Re-run with --force") {
		t.Errorf("error should advertise --force; got %v", err)
	}
}

func TestScaffoldForceOverwrites(t *testing.T) {
	home := t.TempDir()
	if _, err := Scaffold(ScaffoldOptions{Name: "x", Home: home}); err != nil {
		t.Fatal(err)
	}
	res, err := Scaffold(ScaffoldOptions{
		Name: "x", Home: home, Force: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Overwritten {
		t.Errorf("Overwritten should be true on --force")
	}
}

// ─── file shape: parses cleanly via Load ────────────────

// Critical contract: the scaffold output must round-trip through
// the agents.Load() parser. Otherwise users who run /agents create
// would get a "definition skipped: missing description" log on the
// next session.
func TestScaffoldOutputIsLoadableByLoad(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	res, err := Scaffold(ScaffoldOptions{Name: "round-trip", Home: home})
	if err != nil {
		t.Fatal(err)
	}
	r, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	d, ok := r.Lookup("round-trip")
	if !ok {
		t.Fatalf("Load did not pick up scaffolded agent at %s", res.Path)
	}
	if d.Description == "" {
		t.Errorf("scaffolded agent has empty description (would be silently dropped by Load)")
	}
	// Default preset uses the read-only allow-list.
	if len(d.Tools) == 0 {
		t.Errorf("default preset should have a tools allow-list")
	}
}

// ─── presets ────────────────────────────────────────────

// Each named preset must produce frontmatter that parses + matches
// the documented shape (tools / model / permissionMode).
func TestScaffoldPresetExploreShape(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if _, err := Scaffold(ScaffoldOptions{
		Name: "my-explore", Home: home, Preset: "explore",
	}); err != nil {
		t.Fatal(err)
	}
	r, _ := Load(t.TempDir())
	d, ok := r.Lookup("my-explore")
	if !ok {
		t.Fatal("explore preset did not load")
	}
	if d.Model != "claude-haiku-4-5" {
		t.Errorf("model: got %q, want haiku", d.Model)
	}
	for _, banned := range []string{"Edit", "Write", "Agent", "ExitPlanMode"} {
		found := false
		for _, t2 := range d.DisallowedTools {
			if t2 == banned {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("explore preset missing deny %q; got %v", banned, d.DisallowedTools)
		}
	}
}

func TestScaffoldPresetReviewShape(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if _, err := Scaffold(ScaffoldOptions{
		Name: "my-review", Home: home, Preset: "review",
	}); err != nil {
		t.Fatal(err)
	}
	r, _ := Load(t.TempDir())
	d, _ := r.Lookup("my-review")
	if d.Model != "claude-sonnet-4-6" {
		t.Errorf("review preset should use sonnet; got %q", d.Model)
	}
	if !strings.Contains(d.SystemPrompt, "BLOCKER") {
		t.Errorf("review preset prompt missing severity vocab")
	}
}

func TestScaffoldPresetVerifyShape(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if _, err := Scaffold(ScaffoldOptions{
		Name: "my-verify", Home: home, Preset: "verify",
	}); err != nil {
		t.Fatal(err)
	}
	r, _ := Load(t.TempDir())
	d, _ := r.Lookup("my-verify")
	// Verify preset must include bg-task partner tools so users get
	// the documented "long-running probe" workflow out of the box.
	for _, want := range []string{"BashOutput", "KillBash"} {
		found := false
		for _, t2 := range d.Tools {
			if t2 == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("verify preset missing %q in allow-list", want)
		}
	}
	if !strings.Contains(d.SystemPrompt, "VERDICT: PASS") {
		t.Errorf("verify prompt missing verdict line")
	}
}

func TestScaffoldPresetPlanShape(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if _, err := Scaffold(ScaffoldOptions{
		Name: "my-plan", Home: home, Preset: "plan",
	}); err != nil {
		t.Fatal(err)
	}
	r, _ := Load(t.TempDir())
	d, _ := r.Lookup("my-plan")
	if d.PermissionMode != "plan" {
		t.Errorf("plan preset must set permissionMode=plan; got %q", d.PermissionMode)
	}
	if !strings.Contains(d.SystemPrompt, "ExitPlanMode") {
		t.Errorf("plan preset prompt should mention ExitPlanMode")
	}
}

// Unknown preset names fall through to the safe default (read-only)
// rather than erroring — typos shouldn't block the user.
func TestScaffoldUnknownPresetFallsBackToDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	res, err := Scaffold(ScaffoldOptions{
		Name: "weird-preset", Home: home, Preset: "exactly-not-a-real-preset",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.UsedPreset != "default" {
		t.Errorf("unknown preset should record `default`; got %q", res.UsedPreset)
	}
	body, _ := os.ReadFile(res.Path)
	if !strings.Contains(string(body), "TODO: rewrite") {
		t.Errorf("default body marker missing")
	}
}
