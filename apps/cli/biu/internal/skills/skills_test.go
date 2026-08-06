package skills

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixturesDir resolves tools/skills-fixtures from this package's
// location. We don't symlink into a `testdata/` dir because the
// fixtures are shared with services/runtime install tests (PS2.3)
// and biu skill subcommand smoke tests; canonicalising the location
// in one place avoids drift.
func fixturesDir(t *testing.T) string {
	t.Helper()
	// apps/cli/biu/internal/skills → tools/skills-fixtures (5 levels up).
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(wd, "..", "..", "..", "..", "..")
	dir := filepath.Join(root, "tools", "skills-fixtures")
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("fixtures dir %q missing: %v", dir, err)
	}
	return dir
}

// copyDir mirrors src into dst preserving relative paths. Used to
// stage fixtures into a t.TempDir() so tests don't race against the
// shared fixture tree (and so the "Setenv HOME" trick still gets a
// full filesystem root rather than leaking onto the dev machine).
func copyDir(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		out := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		body, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		return os.WriteFile(out, body, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func write(t *testing.T, dir, rel, body string) {
	t.Helper()
	full := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadProjectAndUserAndOverride(t *testing.T) {
	cwd := t.TempDir()
	t.Setenv("HOME", cwd)
	// User-level skill.
	write(t, cwd, ".biumind/skills/hello/SKILL.md", `---
name: hello
description: greet
when-to-use: when user says hi
user-invocable: true
---
Hello $ARGS
`)
	// Project-level skill that overrides "hello".
	write(t, cwd, "proj/.biumind/skills/hello/SKILL.md", `---
name: hello
description: project greet
---
PROJECT $ARGS
`)
	// Project-only skill.
	write(t, cwd, "proj/.biumind/skills/refactor/SKILL.md", `---
name: refactor
description: refactor helper
---
Plan a refactor for: $1
`)

	reg, err := Load(filepath.Join(cwd, "proj"))
	if err != nil {
		t.Fatal(err)
	}
	nonBundled := nonBundledSkills(reg)
	if len(nonBundled) != 2 {
		t.Fatalf("expected 2 non-bundled skills, got %d: %+v", len(nonBundled), nonBundled)
	}
	hello, ok := reg.Lookup("hello")
	if !ok {
		t.Fatal("hello not loaded")
	}
	// Project layer should win.
	if hello.Source != "project" {
		t.Errorf("project should override user; source=%q", hello.Source)
	}
	out, err := hello.Run(context.Background(), "world")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "PROJECT world") {
		t.Errorf("substitution failed: %q", out)
	}

	refactor, _ := reg.Lookup("refactor")
	out, _ = refactor.Run(context.Background(), "auth flow")
	if !strings.Contains(out, "auth flow") {
		t.Errorf("$1 substitution failed: %q", out)
	}
}

func TestLoadIgnoresMissingDir(t *testing.T) {
	cwd := t.TempDir()
	t.Setenv("HOME", cwd)
	reg, err := Load(filepath.Join(cwd, "no-such-dir"))
	if err != nil {
		t.Fatal(err)
	}
	if len(nonBundledSkills(reg)) != 0 {
		t.Errorf("expected empty user/project registry (bundled is allowed)")
	}
}

func TestLoadFallsBackOnDirNameWhenFrontmatterEmpty(t *testing.T) {
	cwd := t.TempDir()
	t.Setenv("HOME", cwd)
	write(t, cwd, ".biumind/skills/myhelper/SKILL.md", `Plain body, no frontmatter.`)
	reg, _ := Load("")
	if _, ok := reg.Lookup("myhelper"); !ok {
		t.Errorf("expected dir-name fallback")
	}
}

func TestParsePathsStripsTrailingSlashStar(t *testing.T) {
	got := parsePathsFrontmatter("apps/cli/biu/**, src/**, **")
	want := []string{"apps/cli/biu", "src"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("parsePaths: %v", got)
	}
}

func TestParsePathsAllMatchAllReturnsNil(t *testing.T) {
	if got := parsePathsFrontmatter("**"); got != nil {
		t.Errorf("** should yield nil; got %v", got)
	}
	if got := parsePathsFrontmatter("**, *"); got != nil {
		t.Errorf("**+* should yield nil; got %v", got)
	}
}

func TestParsePathsEmpty(t *testing.T) {
	if got := parsePathsFrontmatter(""); got != nil {
		t.Errorf("empty should yield nil; got %v", got)
	}
}

func TestAutoAttachLiteralPath(t *testing.T) {
	cwd := t.TempDir()
	t.Setenv("HOME", cwd)
	write(t, cwd, ".biumind/skills/go-style/SKILL.md", `---
name: go-style
description: Go conventions
paths: apps/cli/biu, internal
---
ALWAYS use snake_case for env vars in Go files.
`)
	r, _ := Load(cwd)
	hits := r.AutoAttach("/repo/apps/cli/biu/cmd")
	if len(hits) != 1 || hits[0].Name != "go-style" {
		t.Errorf("expected go-style hit; got %v", hits)
	}

	out := r.AutoAttachPrompt("/repo/apps/cli/biu/cmd")
	if !strings.Contains(out, "go-style") || !strings.Contains(out, "snake_case") {
		t.Errorf("AutoAttachPrompt missing body: %s", out)
	}
}

func TestAutoAttachGlobPattern(t *testing.T) {
	cwd := t.TempDir()
	t.Setenv("HOME", cwd)
	write(t, cwd, ".biumind/skills/ts-style/SKILL.md", `---
name: ts-style
description: TS only
paths: src/**/*.ts
---
TS body
`)
	r, _ := Load(cwd)
	if len(r.AutoAttach("/repo/src/auth/login.ts")) != 1 {
		t.Errorf("glob should match nested .ts file")
	}
	if len(r.AutoAttach("/repo/cmd/main.go")) != 0 {
		t.Errorf("glob must not match unrelated path")
	}
}

func TestAutoAttachSkillsWithoutPathsAreSkipped(t *testing.T) {
	cwd := t.TempDir()
	t.Setenv("HOME", cwd)
	write(t, cwd, ".biumind/skills/no-paths/SKILL.md", `---
name: no-paths
description: invocable only
---
body
`)
	r, _ := Load(cwd)
	if len(r.AutoAttach("/anywhere")) != 0 {
		t.Errorf("skill without paths must NOT auto-attach")
	}
	// But it's still loadable explicitly.
	if _, ok := r.Lookup("no-paths"); !ok {
		t.Errorf("explicit lookup should still find it")
	}
}

func TestAutoAttachMatchAllPatternIsIgnored(t *testing.T) {
	cwd := t.TempDir()
	t.Setenv("HOME", cwd)
	write(t, cwd, ".biumind/skills/star/SKILL.md", `---
name: star
description: would attach everywhere
paths: "**"
---
body
`)
	r, _ := Load(cwd)
	if len(r.AutoAttach("/anywhere")) != 0 {
		t.Errorf("** should not auto-attach (would pollute every project)")
	}
}

func TestSplitFrontmatter(t *testing.T) {
	front, body := splitFrontmatter(`---
name: x
description: y
---
body
`)
	if front["name"] != "x" || front["description"] != "y" {
		t.Errorf("front: %+v", front)
	}
	if strings.TrimSpace(body) != "body" {
		t.Errorf("body: %q", body)
	}
}

// TestLoadFixtures runs Load over the canonical fixture set in
// tools/skills-fixtures/ — staged into a TempDir as a project layer.
//
// All six fixture folders should produce a Skill row: the loader is
// deliberately tolerant of malformed SKILL.md so a single typo
// doesn't blank a user's whole skill catalogue. broken-frontmatter
// arrives without a parsed `name` and the loader falls back to its
// directory name; the body is whatever survived the missing fence.
// This test pins that contract so a future "stricter" loader change
// is forced to update the expectation explicitly.
func TestLoadFixtures(t *testing.T) {
	src := fixturesDir(t)
	tmp := t.TempDir()
	t.Setenv("HOME", tmp) // empty user layer

	projectSkills := filepath.Join(tmp, ".biumind", "skills")
	if err := os.MkdirAll(projectSkills, 0o755); err != nil {
		t.Fatal(err)
	}
	copyDir(t, src, projectSkills)

	r, err := Load(tmp)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := r.All()
	names := make(map[string]bool, len(got))
	for _, s := range got {
		names[s.Name] = true
	}

	want := []string{
		"minimal", "with-paths", "with-resources",
		"with-permissions", "update-of-target",
		// Loader fallback: when frontmatter `name:` is missing or
		// unparsed, the directory name is used as the identifier.
		"broken-frontmatter",
	}
	for _, w := range want {
		if !names[w] {
			t.Errorf("missing skill %q in load result; got %v", w, names)
		}
	}
	if got, want := len(nonBundledSkills(r)), len(want); got != want {
		t.Errorf("loaded %d non-bundled skills, want %d", got, want)
	}

	// Negative-shape check on the broken one: it MUST NOT carry the
	// full description string (which contained an unclosed quote);
	// fallback path means description is empty.
	broken, ok := r.Lookup("broken-frontmatter")
	if !ok {
		t.Fatal("broken-frontmatter not loadable")
	}
	if broken.Description != "" {
		t.Errorf("broken-frontmatter description should be empty under "+
			"fallback parse; got %q", broken.Description)
	}
}

// TestFixtureWithPathsAutoAttaches verifies the fixture's `paths:`
// frontmatter actually drives auto-attach in a Go-source cwd.
func TestFixtureWithPathsAutoAttaches(t *testing.T) {
	src := fixturesDir(t)
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	projectSkills := filepath.Join(tmp, ".biumind", "skills")
	if err := os.MkdirAll(projectSkills, 0o755); err != nil {
		t.Fatal(err)
	}
	copyDir(t, src, projectSkills)

	r, err := Load(tmp)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	hits := r.AutoAttach(filepath.Join(tmp, "services/foo/main.go"))
	var found bool
	for _, h := range hits {
		if h.Name == "with-paths" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("with-paths skill should auto-attach for a .go cwd; got %v", hits)
	}
}

func TestLoadWithExtraDirs(t *testing.T) {
	cwd := t.TempDir()
	extra := t.TempDir()
	t.Setenv("HOME", t.TempDir()) // isolate ~/.biumind/skills

	// Skill in extra dir
	write(t, extra, ".biumind/skills/extra-skill/SKILL.md",
		"---\nname: extra-skill\ndescription: from extra\n---\nbody\n")
	// Skill in cwd that overrides "shared"
	write(t, cwd, ".biumind/skills/shared/SKILL.md",
		"---\nname: shared\ndescription: project version\n---\nproject\n")
	write(t, extra, ".biumind/skills/shared/SKILL.md",
		"---\nname: shared\ndescription: extra version\n---\nextra\n")

	r, err := LoadWithExtraDirs(cwd, []string{extra})
	if err != nil {
		t.Fatal(err)
	}
	names := r.sortedNames()
	if !contains(names, "extra-skill") {
		t.Errorf("missing extra-skill; have %+v", names)
	}
	if !contains(names, "shared") {
		t.Errorf("missing shared; have %+v", names)
	}
	// Project should win over extra
	if rs, ok := r.Lookup("shared"); !ok || !strings.Contains(rs.Skill.Body, "project") {
		body := ""
		if ok {
			body = rs.Skill.Body
		}
		t.Errorf("project should win over extra; got body=%q", body)
	}
}

func contains(slice []string, s string) bool {
	for _, x := range slice {
		if x == s {
			return true
		}
	}
	return false
}
