// Unit tests for the lookupSkill / skillSlashItems helpers — the
// slash-dispatch surface for the file-based SKILL.md registry.
//
// runSkill is exercised end-to-end via the bubbletea integration
// tests under test/integration/repl (Layer C in the test plan); the
// pure-logic helpers below pin the matching + dropdown contract that
// integration can't easily isolate.

package repl

import (
	"os"
	"strings"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/skills"
)

// loadSkills stages a single SKILL.md into a temp HOME so we can
// poke the registry without dragging in copyDir from the skills
// package's tests.
func loadSkillsForTest(t *testing.T, body string) *skills.Registry {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	dir := tmp + "/.biumind/skills/example"
	if err := mkdirAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(dir+"/SKILL.md", body); err != nil {
		t.Fatal(err)
	}
	r, err := skills.Load("")
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestLookupSkill_Hit(t *testing.T) {
	r := loadSkillsForTest(t, `---
name: example
description: greet
---
Hello $ARGS
`)
	m := model{skills: r}
	rs, args, ok := m.lookupSkill("/example", "/example world tour")
	if !ok {
		t.Fatal("lookupSkill should hit")
	}
	if rs.Name() != "example" {
		t.Errorf("name = %q", rs.Name())
	}
	if args != "world tour" {
		t.Errorf("args = %q, want %q", args, "world tour")
	}
}

func TestLookupSkill_MissReturnsFalse(t *testing.T) {
	r := loadSkillsForTest(t, `---
name: only-this
description: x
---
body
`)
	m := model{skills: r}
	if _, _, ok := m.lookupSkill("/missing", "/missing"); ok {
		t.Error("lookupSkill should miss for unknown name")
	}
}

func TestLookupSkill_NilRegistry(t *testing.T) {
	m := model{skills: nil}
	if _, _, ok := m.lookupSkill("/anything", "/anything"); ok {
		t.Error("nil registry should always miss")
	}
}

func TestLookupSkill_RequiresLeadingSlash(t *testing.T) {
	r := loadSkillsForTest(t, `---
name: example
description: x
---
body
`)
	m := model{skills: r}
	if _, _, ok := m.lookupSkill("example", "example"); ok {
		t.Error("missing slash should not match")
	}
}

func TestSkillSlashItems_NilRegistry(t *testing.T) {
	m := model{skills: nil}
	if got := m.skillSlashItems(); got != nil {
		t.Errorf("nil registry should yield nil; got %v", got)
	}
}

func TestSkillSlashItems_EmitsSourceTag(t *testing.T) {
	r := loadSkillsForTest(t, `---
name: tagged
description: tagme
---
body
`)
	m := model{skills: r}
	items := m.skillSlashItems()
	// Bundled skills also surface in the dropdown — find ours.
	var found bool
	for _, it := range items {
		if it.Name == "/tagged" {
			found = true
			if !strings.Contains(it.Description, "[skill:") {
				t.Errorf("description should carry [skill:...] tag; got %q",
					it.Description)
			}
			break
		}
	}
	if !found {
		t.Fatalf("/tagged missing from dropdown items: %d entries", len(items))
	}
}

// ─── tiny fs helpers (avoid pulling in the skills test package) ───

func mkdirAll(p string) error {
	return os.MkdirAll(p, 0o755)
}

func writeFile(p, body string) error {
	return os.WriteFile(p, []byte(body), 0o644)
}
