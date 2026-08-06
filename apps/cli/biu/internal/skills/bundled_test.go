package skills

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// nonBundledSkills returns the loaded skills excluding the bundled
// layer. Used by user/project layer tests so the count assertions
// stay tight instead of having to track how many skills ship with
// biu at any given moment.
func nonBundledSkills(r *Registry) []Skill {
	out := make([]Skill, 0, len(r.byName))
	for _, s := range r.byName {
		if s.Source == "bundled" {
			continue
		}
		out = append(out, s)
	}
	return out
}

// TestBundledSkillsLoad confirms the embed.FS layer registers each
// SKILL.md shipped under bundled/. The set is the contract surfaced
// to first-time users — if a skill drops off the binary the user
// loses muscle memory silently.
func TestBundledSkillsLoad(t *testing.T) {
	r := NewRegistry()
	r.loadBundled()

	want := []string{"loop", "debug", "stuck", "verify", "simplify", "remember"}
	for _, name := range want {
		s, ok := r.Lookup(name)
		if !ok {
			t.Errorf("bundled skill %q not loaded", name)
			continue
		}
		if s.Source != "bundled" {
			t.Errorf("skill %q source=%q, want bundled", name, s.Source)
		}
		if !strings.HasPrefix(s.Path, "embed:bundled/") {
			t.Errorf("skill %q path=%q, want embed: prefix", name, s.Path)
		}
		if s.Description == "" {
			t.Errorf("skill %q has empty description (frontmatter parse failure?)", name)
		}
		if s.Body == "" {
			t.Errorf("skill %q has empty body", name)
		}
		if !s.UserInvocable {
			t.Errorf("skill %q should be user-invocable", name)
		}
	}
}

// TestBundledRunSubstitutesArgs verifies the bundled SKILL.md bodies
// flow through the same $ARGS substitution path as on-disk skills.
func TestBundledRunSubstitutesArgs(t *testing.T) {
	r := NewRegistry()
	r.loadBundled()
	loop, ok := r.Lookup("loop")
	if !ok {
		t.Fatal("loop bundled skill missing")
	}
	out, err := loop.Run(context.Background(), "")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out, "CronCreate") {
		t.Errorf("loop body lost CronCreate reference: %q", out)
	}
}

// TestProjectSkillShadowsBundled — the override layering contract:
// a project-level SKILL.md with the same name as a bundled one wins.
// This is how a team customises `/debug` for their own ops handbook
// without forking biu.
func TestProjectSkillShadowsBundled(t *testing.T) {
	cwd := t.TempDir()
	t.Setenv("HOME", cwd)
	write(t, cwd, ".biumind/skills/debug/SKILL.md", `---
name: debug
description: project-specific debug
---
PROJECT DEBUG BODY
`)
	r, err := Load(cwd)
	if err != nil {
		t.Fatal(err)
	}
	dbg, ok := r.Lookup("debug")
	if !ok {
		t.Fatal("debug not loaded")
	}
	if dbg.Source != "project" {
		t.Errorf("project should shadow bundled; got source=%q", dbg.Source)
	}
	if !strings.Contains(dbg.Body, "PROJECT DEBUG BODY") {
		t.Errorf("project body lost: %q", dbg.Body)
	}
}

// TestUserSkillShadowsBundled — same override contract at the user
// (~/.biumind) layer.
func TestUserSkillShadowsBundled(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	write(t, home, ".biumind/skills/loop/SKILL.md", `---
name: loop
description: my personal loop
---
USER LOOP BODY
`)
	r, err := Load(filepath.Join(home, "no-project"))
	if err != nil {
		t.Fatal(err)
	}
	loop, ok := r.Lookup("loop")
	if !ok {
		t.Fatal("loop not loaded")
	}
	if loop.Source != "user" {
		t.Errorf("user should shadow bundled; got source=%q", loop.Source)
	}
	if !strings.Contains(loop.Body, "USER LOOP BODY") {
		t.Errorf("user body lost: %q", loop.Body)
	}
}
