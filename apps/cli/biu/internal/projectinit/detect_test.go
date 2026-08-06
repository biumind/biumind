package projectinit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

// Empty directory yields zero languages — the renderer falls back
// to a "didn't recognise" template.
func TestDetectEmptyDirectory(t *testing.T) {
	cwd := t.TempDir()
	d := Detect(cwd)
	if len(d.Languages) != 0 {
		t.Errorf("empty dir should yield no languages; got %v", d.Languages)
	}
	rendered := d.Render()
	if !strings.Contains(rendered, "didn't recognise a manifest") {
		t.Errorf("empty render should explain missing detection; got %q", rendered[:200])
	}
}

func TestDetectGo(t *testing.T) {
	cwd := t.TempDir()
	write(t, cwd, "go.mod", "module x\n\ngo 1.22\n")
	d := Detect(cwd)
	if len(d.Languages) != 1 || d.Languages[0].Language != LangGo {
		t.Fatalf("want Go; got %+v", d.Languages)
	}
	if d.Languages[0].Manager != PMGoMod {
		t.Errorf("manager: got %q", d.Languages[0].Manager)
	}
	// gofmt may be `gofmt -l -w .` or similar — substring match is
	// stable as long as the binary name is in the command.
	flat := flattenValues(d.Languages[0].Commands)
	for _, must := range []string{"go test ./...", "go build ./...", "go vet ./...", "gofmt"} {
		if !strings.Contains(flat, must) {
			t.Errorf("Go commands missing %q; got %v", must, d.Languages[0].Commands)
		}
	}
}

// flattenValues joins every command value with a separator so
// substring assertions are stable regardless of map iteration order.
func flattenValues(m map[string]string) string {
	var b strings.Builder
	for _, v := range m {
		b.WriteString(v)
		b.WriteByte('\n')
	}
	return b.String()
}

func TestDetectNodeWithLockfilePicksPM(t *testing.T) {
	cwd := t.TempDir()
	write(t, cwd, "package.json", `{"scripts":{"test":"jest"}}`)
	write(t, cwd, "pnpm-lock.yaml", "lockfileVersion: 6.0\n")
	d := Detect(cwd)
	if d.Languages[0].Manager != PMPnpm {
		t.Errorf("pnpm-lock.yaml should win; got %q", d.Languages[0].Manager)
	}
	if d.Languages[0].Commands["test"] != "pnpm run test" {
		t.Errorf("test cmd: got %q", d.Languages[0].Commands["test"])
	}
}

// Lockfile precedence — bun > pnpm > yarn > npm. Test the
// most-specific wins layout.
func TestDetectNodeLockfilePrecedence(t *testing.T) {
	cwd := t.TempDir()
	write(t, cwd, "package.json", `{}`)
	// Multiple lockfiles co-existing happens during migrations.
	write(t, cwd, "package-lock.json", "")
	write(t, cwd, "yarn.lock", "")
	write(t, cwd, "pnpm-lock.yaml", "")
	d := Detect(cwd)
	if d.Languages[0].Manager != PMPnpm {
		t.Errorf("expected pnpm to win precedence; got %q", d.Languages[0].Manager)
	}
}

// Corepack-style `packageManager` field overrides lockfile.
func TestDetectNodePackageManagerFieldWins(t *testing.T) {
	cwd := t.TempDir()
	write(t, cwd, "package.json", `{"packageManager":"yarn@4.0.0"}`)
	write(t, cwd, "pnpm-lock.yaml", "")
	d := Detect(cwd)
	if d.Languages[0].Manager != PMYarn {
		t.Errorf("packageManager field should win; got %q", d.Languages[0].Manager)
	}
}

// Scripts present in package.json populate command intents using
// the `<pm> run <name>` form.
func TestDetectNodeSurfacesScripts(t *testing.T) {
	cwd := t.TempDir()
	write(t, cwd, "package.json", `{"scripts":{"build":"tsup","lint":"eslint .","format":"prettier -w ."}}`)
	d := Detect(cwd)
	cmds := d.Languages[0].Commands
	for _, intent := range []string{"build", "lint", "format"} {
		want := "npm run " + intent
		if cmds[intent] != want {
			t.Errorf("intent %q: got %q, want %q", intent, cmds[intent], want)
		}
	}
}

func TestDetectRust(t *testing.T) {
	cwd := t.TempDir()
	write(t, cwd, "Cargo.toml", `[package]
name = "x"
version = "0.1"
`)
	d := Detect(cwd)
	if len(d.Languages) != 1 || d.Languages[0].Language != LangRust {
		t.Fatalf("expected Rust; got %+v", d.Languages)
	}
	if !contains(d.Languages[0].Commands, "cargo clippy --all-targets") {
		t.Errorf("clippy missing: %v", d.Languages[0].Commands)
	}
}

func TestDetectPythonByPyprojectAndUvLock(t *testing.T) {
	cwd := t.TempDir()
	write(t, cwd, "pyproject.toml", "[project]\nname = 'x'\n")
	write(t, cwd, "uv.lock", "")
	d := Detect(cwd)
	if d.Languages[0].Manager != PMUv {
		t.Errorf("uv.lock should pick uv; got %q", d.Languages[0].Manager)
	}
	if d.Languages[0].Commands["install"] != "uv sync" {
		t.Errorf("install cmd: got %q", d.Languages[0].Commands["install"])
	}
}

func TestDetectPythonRequirementsTxtFallsBackToPip(t *testing.T) {
	cwd := t.TempDir()
	write(t, cwd, "requirements.txt", "")
	d := Detect(cwd)
	if d.Languages[0].Manager != PMPip {
		t.Errorf("requirements.txt should pick pip; got %q", d.Languages[0].Manager)
	}
}

// Mixed projects (Go + Node) should produce both sections in
// detection order.
func TestDetectMixedProject(t *testing.T) {
	cwd := t.TempDir()
	write(t, cwd, "go.mod", "module x\n")
	write(t, cwd, "package.json", `{}`)
	d := Detect(cwd)
	if len(d.Languages) != 2 {
		t.Fatalf("want 2 sections; got %d", len(d.Languages))
	}
	if d.Languages[0].Language != LangGo || d.Languages[1].Language != LangNode {
		t.Errorf("section order off: %+v", d.Languages)
	}
}

// Notes surface ad-hoc context the renderer mentions.
func TestDetectSurfacesNotesForCommonFiles(t *testing.T) {
	cwd := t.TempDir()
	write(t, cwd, "Makefile", "all:\n\techo hi\n")
	write(t, cwd, "README.md", "# X\n")
	write(t, cwd, "docker-compose.yml", "")
	write(t, cwd, ".env.example", "DB_URL=")
	d := Detect(cwd)
	for _, fragment := range []string{"Makefile", "README.md", "docker-compose", ".env.example"} {
		found := false
		for _, n := range d.Notes {
			if strings.Contains(n, fragment) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("notes missing fragment %q; got %v", fragment, d.Notes)
		}
	}
}

// Render shape: stable headings + Architecture / Conventions /
// Gotchas placeholders so the user always knows what to fill in.
func TestRenderHasStableHeadings(t *testing.T) {
	cwd := t.TempDir()
	write(t, cwd, "go.mod", "module x\n")
	d := Detect(cwd)
	rendered := d.Render()
	for _, h := range []string{
		"# BIUMIND.md",
		"## Project type",
		"## Commands",
		"### Go",
		"## Architecture",
		"## Conventions",
		"## Gotchas",
	} {
		if !strings.Contains(rendered, h) {
			t.Errorf("rendered missing heading %q;\nfull:\n%s", h, rendered)
		}
	}
}

// Per-section command listings are sorted by intent for stable
// diffs across runs.
func TestRenderCommandsAreSorted(t *testing.T) {
	cwd := t.TempDir()
	write(t, cwd, "Cargo.toml", "")
	rendered := Detect(cwd).Render()
	// Rust intents alphabetically: build, clippy, format, run, test
	wantOrder := []string{"build", "clippy", "format", "run", "test"}
	last := -1
	for _, k := range wantOrder {
		idx := strings.Index(rendered, "**"+k+"**")
		if idx <= last {
			t.Errorf("intent %q out of order or missing (idx=%d, last=%d)", k, idx, last)
			break
		}
		last = idx
	}
}

func contains(m map[string]string, needle string) bool {
	for _, v := range m {
		if v == needle {
			return true
		}
	}
	return false
}
