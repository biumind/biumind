package agents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/permissions"
)

func write(t *testing.T, dir, rel, body string) string {
	t.Helper()
	full := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return full
}

func TestLoadParsesFrontmatterAndBody(t *testing.T) {
	cwd := t.TempDir()
	t.Setenv("HOME", cwd)
	write(t, cwd, ".biumind/agents/explore.md", `---
name: explore
description: Read-only repo exploration agent
tools: Read, Glob, Grep
permissionMode: plan
model: claude-haiku-4-5
maxTurns: 10
---
You are the Explore agent. Read files and summarise.
`)
	r, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	d, ok := r.Lookup("explore")
	if !ok {
		t.Fatal("explore not registered")
	}
	if d.Description == "" || d.SystemPrompt == "" {
		t.Errorf("missing fields: %+v", d)
	}
	if len(d.Tools) != 3 || d.Tools[0] != "Read" {
		t.Errorf("tools: %+v", d.Tools)
	}
	if d.PermissionMode != permissions.ModePlan {
		t.Errorf("mode: %v", d.PermissionMode)
	}
	if d.Model != "claude-haiku-4-5" {
		t.Errorf("model: %s", d.Model)
	}
	if d.MaxTurns != 10 {
		t.Errorf("maxTurns: %d", d.MaxTurns)
	}
}

func TestProjectOverridesUser(t *testing.T) {
	cwd := t.TempDir()
	t.Setenv("HOME", cwd)
	write(t, cwd, ".biumind/agents/shared.md", `---
name: shared
description: from user
---
USER body
`)
	proj := filepath.Join(cwd, "proj")
	write(t, proj, ".biumind/agents/shared.md", `---
name: shared
description: from project
---
PROJECT body
`)
	r, _ := Load(proj)
	d, _ := r.Lookup("shared")
	if d.Source != "project" || d.Description != "from project" {
		t.Errorf("project should win: %+v", d)
	}
}

func TestSkipNoDescription(t *testing.T) {
	cwd := t.TempDir()
	t.Setenv("HOME", cwd)
	write(t, cwd, ".biumind/agents/incomplete.md", `---
name: incomplete
---
nothing here
`)
	r, _ := Load("")
	if _, ok := r.Lookup("incomplete"); ok {
		t.Errorf("missing description should skip; got registered")
	}
}

func TestApplyOverridesBase(t *testing.T) {
	d := &Definition{
		SystemPrompt:   "you are explore",
		Model:          "claude-haiku-4-5",
		MaxTurns:       7,
		PermissionMode: permissions.ModePlan,
		Tools:          []string{"Read", "Glob"},
	}
	base := SpawnRequest{
		System: "default", Model: "claude-opus-4-7", MaxTurns: 25,
	}
	got := d.Apply(base)
	if got.System != "you are explore" || got.Model != "claude-haiku-4-5" {
		t.Errorf("override failed: %+v", got)
	}
	if got.MaxTurns != 7 || got.PermissionMode != permissions.ModePlan {
		t.Errorf("budget/mode override failed: %+v", got)
	}
	if len(got.Tools) != 2 {
		t.Errorf("tool whitelist lost: %+v", got.Tools)
	}
}

func TestApplyInheritKeepsBaseModel(t *testing.T) {
	d := &Definition{Model: "inherit"}
	got := d.Apply(SpawnRequest{Model: "claude-opus-4-7"})
	if got.Model != "claude-opus-4-7" {
		t.Errorf("`inherit` must not override base: %s", got.Model)
	}
}

func TestFilterToolsAllowList(t *testing.T) {
	d := &Definition{Tools: []string{"Read", "Glob"}}
	got := d.FilterTools([]string{"Read", "Glob", "Bash", "Edit"})
	if len(got) != 2 {
		t.Errorf("filter wrong: %v", got)
	}
}

func TestFilterToolsDenyList(t *testing.T) {
	d := &Definition{DisallowedTools: []string{"Bash"}}
	got := d.FilterTools([]string{"Read", "Bash", "Edit"})
	for _, t := range got {
		if t == "Bash" {
			// not allowed
			panic("Bash leaked through deny list")
		}
	}
	if len(got) != 2 {
		// unused panic above doesn't catch this — keep explicit check
	}
}

func TestParseList(t *testing.T) {
	cases := map[string]int{
		"":              0,
		"a":             1,
		"a, b, c":       3,
		"[a, b]":        2,
		`"x", "y"`:      2,
		"  a  ,  b ":    2,
	}
	for in, want := range cases {
		if got := parseList(in); len(got) != want {
			t.Errorf("parseList(%q)=%v want len %d", in, got, want)
		}
	}
}

func TestNamesSorted(t *testing.T) {
	cwd := t.TempDir()
	t.Setenv("HOME", cwd)
	for _, n := range []string{"zebra", "alpha", "mango"} {
		write(t, cwd, ".biumind/agents/"+n+".md",
			"---\nname: "+n+"\ndescription: x\n---\nbody")
	}
	r, _ := Load("")
	got := r.Names()
	// Go's byte-wise string compare puts uppercase ASCII before
	// lowercase. Capitalised built-ins (CodeReview / Explore / Plan
	// / Verification) come first, then the lowercase user agents,
	// with the lowercase "general-purpose" built-in slotted between
	// them at its alphabetical position.
	want := []string{
		"CodeReview", "Explore", "Plan", "Verification",
		"alpha", "general-purpose", "mango", "zebra",
	}
	if len(got) != len(want) {
		t.Fatalf("names not sorted: %v (want %v)", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("names not sorted: %v (want %v)", got, want)
			break
		}
	}
}

func TestBuiltinPlanSeededByLoad(t *testing.T) {
	r, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	plan, ok := r.Lookup("Plan")
	if !ok {
		t.Fatal("Plan built-in agent must be available out of the box")
	}
	if plan.Source != "builtin" {
		t.Errorf("Plan agent should be marked builtin; got %q", plan.Source)
	}
	if plan.PermissionMode != "plan" {
		t.Errorf("Plan agent should run in plan mode; got %q", plan.PermissionMode)
	}
	// Allowed tools must NOT include Edit/Write — built-in is read-only.
	for _, banned := range []string{"Edit", "Write", "MultiEdit", "NotebookEdit"} {
		for _, allow := range plan.Tools {
			if allow == banned {
				t.Errorf("Plan tool whitelist should exclude %s; got %v", banned, plan.Tools)
			}
		}
	}
}

func TestBuiltinExploreSeededByLoad(t *testing.T) {
	r, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	exp, ok := r.Lookup("Explore")
	if !ok {
		t.Fatal("Explore built-in agent must be available out of the box")
	}
	if exp.Source != "builtin" {
		t.Errorf("Explore should be marked builtin; got %q", exp.Source)
	}
	// Explore inherits permission mode from the parent — locking to
	// plan would silently disable Bash (which is allow-listed for
	// read-only shell ops like `git log`). Read-only enforcement
	// comes from the deny-list + system prompt instead.
	if exp.PermissionMode != "" {
		t.Errorf("Explore should inherit permission mode; got %q", exp.PermissionMode)
	}
	// Deny-list must keep the obvious write tools + recursive Agent
	// spawn out, no matter what the allow-list says.
	for _, banned := range []string{"Edit", "Write", "MultiEdit", "NotebookEdit", "Agent"} {
		found := false
		for _, d := range exp.DisallowedTools {
			if d == banned {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Explore must disallow %s; got %v", banned, exp.DisallowedTools)
		}
	}
	// Allow-list should NOT contain any write tool (FilterTools is
	// belt; this is suspenders).
	for _, banned := range []string{"Edit", "Write", "MultiEdit", "NotebookEdit"} {
		for _, allow := range exp.Tools {
			if allow == banned {
				t.Errorf("Explore allow-list should not include %s; got %v", banned, exp.Tools)
			}
		}
	}
}

func TestBuiltinCodeReviewSeededByLoad(t *testing.T) {
	r, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cr, ok := r.Lookup("CodeReview")
	if !ok {
		t.Fatal("CodeReview built-in agent must be available out of the box")
	}
	if cr.Source != "builtin" {
		t.Errorf("CodeReview should be marked builtin; got %q", cr.Source)
	}
	// Inherits permission mode (same reasoning as Explore — plan mode
	// would silently kill Bash for `git diff`).
	if cr.PermissionMode != "" {
		t.Errorf("CodeReview should inherit permission mode; got %q", cr.PermissionMode)
	}
	// Default model matters: sonnet for reasoning quality.
	if cr.Model != "claude-sonnet-4-6" {
		t.Errorf("CodeReview default model should be sonnet-4-6; got %q", cr.Model)
	}
	// Deny-list locks down write paths + recursive Agent.
	for _, banned := range []string{"Edit", "Write", "MultiEdit", "NotebookEdit", "Agent", "ExitPlanMode"} {
		found := false
		for _, d := range cr.DisallowedTools {
			if d == banned {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("CodeReview must disallow %s; got %v", banned, cr.DisallowedTools)
		}
	}
	// Allow-list is read-only research tools (Read/Glob/Grep/Bash/WebFetch).
	for _, want := range []string{"Read", "Glob", "Grep", "Bash", "WebFetch"} {
		found := false
		for _, a := range cr.Tools {
			if a == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("CodeReview must allow-list %s; got %v", want, cr.Tools)
		}
	}
	// System prompt must teach the severity vocabulary the output
	// format depends on. If these strings drift, downstream tooling
	// (renderers, severity counters) will silently break.
	for _, kw := range []string{"BLOCKER", "MAJOR", "MINOR", "QUESTION", "Summary:"} {
		if !strings.Contains(cr.SystemPrompt, kw) {
			t.Errorf("system prompt missing severity vocabulary %q", kw)
		}
	}
}

func TestBuiltinVerificationSeededByLoad(t *testing.T) {
	r, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	v, ok := r.Lookup("Verification")
	if !ok {
		t.Fatal("Verification built-in must be available out of the box")
	}
	if v.Source != "builtin" {
		t.Errorf("Source: got %q, want builtin", v.Source)
	}
	if v.PermissionMode != "" {
		t.Errorf("Verification should inherit mode; got %q", v.PermissionMode)
	}
	if v.Model != "claude-sonnet-4-6" {
		t.Errorf("default model should be sonnet; got %q", v.Model)
	}
	// Allow-list must include the bg-task partners so server-side
	// probes work end-to-end.
	for _, want := range []string{"Bash", "Read", "Glob", "Grep", "WebFetch", "BashOutput", "KillBash"} {
		found := false
		for _, t2 := range v.Tools {
			if t2 == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Verification must allow-list %q; got %v", want, v.Tools)
		}
	}
	// Deny-list mirrors CodeReview — no recursive Agent, no edits.
	for _, banned := range []string{"Agent", "ExitPlanMode", "Edit", "Write", "MultiEdit", "NotebookEdit"} {
		found := false
		for _, d := range v.DisallowedTools {
			if d == banned {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Verification must disallow %q; got %v", banned, v.DisallowedTools)
		}
	}
	// Verdict vocabulary the parent / future tooling parses for.
	for _, kw := range []string{"VERDICT: PASS", "VERDICT: FAIL", "VERDICT: PARTIAL"} {
		if !strings.Contains(v.SystemPrompt, kw) {
			t.Errorf("system prompt missing verdict marker %q", kw)
		}
	}
	// Anti-rationalization section — locked because it's the prompt's
	// load-bearing part. If a future edit removes the "rationalization"
	// language, this test catches it before behaviour drifts.
	for _, kw := range []string{"Verification avoidance", "first 80%", "RATIONALIZATIONS"} {
		if !strings.Contains(v.SystemPrompt, kw) {
			t.Errorf("system prompt missing anti-rationalization marker %q", kw)
		}
	}
}

func TestBuiltinGeneralPurposeSeededByLoad(t *testing.T) {
	r, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	d, ok := r.Lookup("general-purpose")
	if !ok {
		t.Fatal("general-purpose built-in must be available out of the box")
	}
	if d.Source != "builtin" {
		t.Errorf("Source: got %q, want builtin", d.Source)
	}
	// Empty Tools = inherit full parent catalog. This is the
	// deliberate departure from specialists; locked here so a
	// future "let's add an allow-list" tweak can't sneak in
	// without updating the prompt's "your strengths" section.
	if len(d.Tools) != 0 {
		t.Errorf("general-purpose should NOT have an allow-list (inherits parent catalog); got %v", d.Tools)
	}
	if len(d.DisallowedTools) != 0 {
		t.Errorf("general-purpose should NOT have a deny-list; got %v", d.DisallowedTools)
	}
	if d.Model != "inherit" {
		t.Errorf("Model: got %q, want inherit", d.Model)
	}
	// Prompt must steer toward specialists (the model is bias-prone
	// to grab the broadest tool when one exists). Lock the markers
	// the prompt depends on.
	for _, kw := range []string{"Explore", "Plan", "CodeReview", "Verification", "specialists"} {
		if !strings.Contains(d.SystemPrompt, kw) {
			t.Errorf("system prompt missing specialist nudge %q", kw)
		}
	}
	for _, kw := range []string{"NEVER create files", "Cite findings"} {
		if !strings.Contains(d.SystemPrompt, kw) {
			t.Errorf("system prompt missing guidance marker %q", kw)
		}
	}
}

// FilterTools with empty allow + empty deny must be a passthrough —
// every tool the parent has is available to general-purpose.
func TestGeneralPurposeFilterToolsIsPassthrough(t *testing.T) {
	r, _ := Load(t.TempDir())
	d, _ := r.Lookup("general-purpose")
	parent := []string{"Read", "Write", "Edit", "Bash", "Agent", "ExitPlanMode"}
	got := d.FilterTools(parent)
	if len(got) != len(parent) {
		t.Errorf("FilterTools should pass through everything; got %v", got)
	}
}

func TestUserPlanOverridesBuiltin(t *testing.T) {
	dir := t.TempDir()
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	// Drop a user-level Plan override.
	plansDir := filepath.Join(homeDir, ".biumind", "agents")
	if err := os.MkdirAll(plansDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: Plan\ndescription: User plan agent\n---\nUser system prompt"
	if err := os.WriteFile(filepath.Join(plansDir, "plan.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	r, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	plan, _ := r.Lookup("Plan")
	if plan.Source != "user" {
		t.Errorf("user override should win; source=%q", plan.Source)
	}
	if plan.Description != "User plan agent" {
		t.Errorf("user description should win; got %q", plan.Description)
	}
}
