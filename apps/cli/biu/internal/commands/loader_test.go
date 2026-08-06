package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeCmd(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadEmpty(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	r, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got := r.All(); len(got) != 0 {
		t.Errorf("empty registry should yield no commands; got %v", got)
	}
}

func TestLoadUserCommand(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeCmd(t, filepath.Join(home, ".biumind", "commands"), "refactor.md",
		"---\ndescription: refactor a function\n---\n"+
			"Refactor the target.\n\nTarget: $ARGUMENTS\n")

	r, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c, ok := r.Lookup("refactor")
	if !ok {
		t.Fatal("refactor not registered")
	}
	if c.Source != SrcUser {
		t.Errorf("Source: got %q, want user", c.Source)
	}
	if c.Description != "refactor a function" {
		t.Errorf("Description: got %q", c.Description)
	}
	if !strings.Contains(c.Body, "Target: $ARGUMENTS") {
		t.Errorf("Body lost: %q", c.Body)
	}
}

// Project commands shadow user commands of the same name. Project
// is closer to the work, so it wins — same precedence as
// agents/memory.
func TestLoadProjectShadowsUser(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeCmd(t, filepath.Join(home, ".biumind", "commands"), "lint.md",
		"---\ndescription: from user\n---\nUSER body\n")

	cwd := t.TempDir()
	writeCmd(t, filepath.Join(cwd, ".biumind", "commands"), "lint.md",
		"---\ndescription: from project\n---\nPROJECT body\n")

	r, _ := Load(cwd)
	c, _ := r.Lookup("lint")
	if c.Source != SrcProject {
		t.Errorf("project should win; got source=%q", c.Source)
	}
	if !strings.Contains(c.Body, "PROJECT body") {
		t.Errorf("project body should win; got %q", c.Body)
	}
}

// Loader reads .biumind/commands/ only. A stray .claude/ directory
// (left over from a former Claude Code install) must be ignored —
// loading it would let an old config silently leak into biu.
func TestLoadIgnoresClaudeDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeCmd(t, filepath.Join(home, ".claude", "commands"), "stale.md",
		"stale body\n")
	r, _ := Load(t.TempDir())
	if _, ok := r.Lookup("stale"); ok {
		t.Errorf(".claude command must NOT be loaded; got %v", r.All())
	}
}

// First non-empty line becomes the description when no frontmatter.
func TestDescriptionFallsBackToFirstLine(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeCmd(t, filepath.Join(home, ".biumind", "commands"), "noheader.md",
		"# Just a heading\n\nBody\n")
	r, _ := Load(t.TempDir())
	c, _ := r.Lookup("noheader")
	if c.Description != "Just a heading" {
		t.Errorf("expected first-line fallback; got %q", c.Description)
	}
}

// Subdirectories are ignored — keeps the slash namespace flat.
func TestLoadIgnoresSubdirectories(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeCmd(t, filepath.Join(home, ".biumind", "commands", "nested"), "deep.md", "x")
	r, _ := Load(t.TempDir())
	if got := len(r.All()); got != 0 {
		t.Errorf("subdir commands should be ignored; got %d", got)
	}
}

// Invalid filenames (spaces, dots, leading digits) are silently
// skipped — protects the slash dispatcher from "/foo bar" style
// input.
func TestLoadSkipsInvalidFilenames(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".biumind", "commands")
	for _, badName := range []string{"has space.md", "1leading.md", "weird.dot.name.md"} {
		writeCmd(t, dir, badName, "x")
	}
	writeCmd(t, dir, "good.md", "ok")
	r, _ := Load(t.TempDir())
	if _, ok := r.Lookup("good"); !ok {
		t.Errorf("valid name should register")
	}
	for _, n := range []string{"has space", "1leading", "weird.dot.name"} {
		if _, ok := r.Lookup(n); ok {
			t.Errorf("invalid name %q should be skipped", n)
		}
	}
}

// $ARGUMENTS / $CWD / $DATE substitution.
func TestRenderSubstitutes(t *testing.T) {
	c := &Command{
		Body: "Args: $ARGUMENTS\nCwd: $CWD\nDate: $DATE\n",
	}
	got := c.Render("refactor pkg/auth")
	if !strings.Contains(got, "Args: refactor pkg/auth") {
		t.Errorf("$ARGUMENTS not substituted: %q", got)
	}
	if !strings.Contains(got, "Cwd: ") {
		t.Errorf("$CWD not substituted: %q", got)
	}
	// Date is YYYY-MM-DD; just check the shape.
	if !strings.Contains(got, "Date: 20") {
		t.Errorf("$DATE not substituted: %q", got)
	}
}

// Unknown placeholders are passed through verbatim — caller's typo,
// not biu's job to silently drop.
func TestRenderLeavesUnknownPlaceholders(t *testing.T) {
	c := &Command{Body: "Hello $UNKNOWN there\n"}
	got := c.Render("ignored")
	if !strings.Contains(got, "$UNKNOWN") {
		t.Errorf("unknown placeholder should pass through; got %q", got)
	}
}

// Empty $ARGUMENTS doesn't leave a stray space in the rendered body.
// We rely on the user's body design — biu just substitutes "" — but
// the test pins the behaviour so future "smart" trimming doesn't
// silently drop content the user intended.
func TestRenderEmptyArgsSubstitutesEmpty(t *testing.T) {
	c := &Command{Body: "Args: [$ARGUMENTS]\n"}
	got := c.Render("")
	if !strings.Contains(got, "Args: []") {
		t.Errorf("empty args should substitute as empty; got %q", got)
	}
}

// Frontmatter parser handles common edge cases.
func TestSplitFrontmatterCases(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		wantDesc string
		wantBody string
	}{
		{
			name:     "basic",
			in:       "---\ndescription: hi\n---\nbody\n",
			wantDesc: "hi", wantBody: "body\n",
		},
		{
			name:     "quoted value",
			in:       "---\ndescription: \"quoted value\"\n---\nbody\n",
			wantDesc: "quoted value", wantBody: "body\n",
		},
		{
			name: "no frontmatter",
			in:   "just body\n", wantDesc: "", wantBody: "just body\n",
		},
		{
			name:     "comment line ignored",
			in:       "---\n# this is a comment\ndescription: real\n---\nbody\n",
			wantDesc: "real", wantBody: "body\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fm, body := splitFrontmatter(c.in)
			if fm["description"] != c.wantDesc {
				t.Errorf("desc: got %q, want %q", fm["description"], c.wantDesc)
			}
			if body != c.wantBody {
				t.Errorf("body: got %q, want %q", body, c.wantBody)
			}
		})
	}
}

// validCommandName covers every code path so future name additions
// don't regress.
func TestValidCommandName(t *testing.T) {
	cases := map[string]bool{
		"":                      false,
		"refactor":              true,
		"my-command":            true,
		"my_command":            true,
		"with space":            false,
		"1starts-num":           false,
		"-leading":              false,
		"has.dot":               false,
		strings.Repeat("x", 33): false,
		strings.Repeat("x", 32): true,
	}
	for in, want := range cases {
		if got := validCommandName(in); got != want {
			t.Errorf("validCommandName(%q) = %v, want %v", in, got, want)
		}
	}
}

// All() returns commands sorted by name — REPL menu must be
// deterministic across sessions.
func TestAllSortedByName(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".biumind", "commands")
	for _, n := range []string{"zebra", "alpha", "mango"} {
		writeCmd(t, dir, n+".md", "x")
	}
	r, _ := Load(t.TempDir())
	got := r.All()
	want := []string{"alpha", "mango", "zebra"}
	if len(got) != len(want) {
		t.Fatalf("count: got %d, want %d", len(got), len(want))
	}
	for i, c := range got {
		if c.Name != want[i] {
			t.Errorf("position %d: got %q, want %q", i, c.Name, want[i])
		}
	}
}
