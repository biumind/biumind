package files

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
	"github.com/biumind/biumind/apps/cli/biu/internal/state"
)

func newEnv(t *testing.T, cwd string) *engine.ToolEnv {
	t.Helper()
	return &engine.ToolEnv{AppState: state.New(), Cwd: cwd}
}

func writeFile(t *testing.T, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func flatten(p *engine.ToolResultPayload) string {
	out := ""
	for _, b := range p.Content {
		out += b.Text
	}
	return out
}

func TestReadAndCachesFreshness(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "x.go"), "package main\nfunc main(){}\n")
	env := newEnv(t, dir)

	out, _ := ReadTool{}.Call(context.Background(), map[string]any{
		"file_path": "x.go",
	}, env)
	if out.IsError {
		t.Fatalf("read failed: %+v", out)
	}
	body := flatten(out)
	if !strings.Contains(body, "package main") || !strings.Contains(body, "     1\t") {
		t.Errorf("read output missing line numbers: %s", body)
	}
	cached, ok := env.AppState.FileSnapshot(filepath.Join(dir, "x.go"))
	if !ok || cached.Sha256 == "" {
		t.Errorf("freshness cache not populated: %+v", cached)
	}
}

func TestEditRequiresPriorRead(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "y.go")
	writeFile(t, p, "alpha\nbeta\n")
	env := newEnv(t, dir)

	out, _ := EditTool{}.Call(context.Background(), map[string]any{
		"file_path":  "y.go",
		"old_string": "alpha",
		"new_string": "ALPHA",
	}, env)
	if !out.IsError || !strings.Contains(out.SoftError, "not been read") {
		t.Errorf("edit without read should soft-error; got %+v", out)
	}
}

func TestEditAfterReadHappyPath(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "z.go")
	writeFile(t, p, "alpha\nbeta\n")
	env := newEnv(t, dir)

	_, _ = ReadTool{}.Call(context.Background(), map[string]any{"file_path": "z.go"}, env)
	out, _ := EditTool{}.Call(context.Background(), map[string]any{
		"file_path":  "z.go",
		"old_string": "alpha",
		"new_string": "ALPHA",
	}, env)
	if out.IsError {
		t.Fatalf("edit failed: %+v", out)
	}
	final, _ := os.ReadFile(p)
	if !strings.Contains(string(final), "ALPHA") {
		t.Errorf("edit didn't apply: %q", final)
	}
}

func TestEditRejectsAmbiguous(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.go")
	writeFile(t, p, "x\nx\nx\n")
	env := newEnv(t, dir)
	_, _ = ReadTool{}.Call(context.Background(), map[string]any{"file_path": "a.go"}, env)
	out, _ := EditTool{}.Call(context.Background(), map[string]any{
		"file_path":  "a.go",
		"old_string": "x",
		"new_string": "Y",
	}, env)
	if !out.IsError || !strings.Contains(out.SoftError, "ambiguous") &&
		!strings.Contains(out.SoftError, "occurs") {
		t.Errorf("ambiguous edit must soft-error; got %+v", out)
	}
}

func TestEditReplaceAll(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "b.go")
	writeFile(t, p, "x\nx\nx\n")
	env := newEnv(t, dir)
	_, _ = ReadTool{}.Call(context.Background(), map[string]any{"file_path": "b.go"}, env)
	out, _ := EditTool{}.Call(context.Background(), map[string]any{
		"file_path": "b.go", "old_string": "x", "new_string": "Y",
		"replace_all": true,
	}, env)
	if out.IsError {
		t.Fatalf("replace_all failed: %+v", out)
	}
	final, _ := os.ReadFile(p)
	if string(final) != "Y\nY\nY\n" {
		t.Errorf("replace_all wrong: %q", final)
	}
}

func TestEditDetectsConcurrentMod(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "c.go")
	writeFile(t, p, "alpha\n")
	env := newEnv(t, dir)
	_, _ = ReadTool{}.Call(context.Background(), map[string]any{"file_path": "c.go"}, env)

	// External mutation between Read and Edit.
	writeFile(t, p, "betagamma\n")

	out, _ := EditTool{}.Call(context.Background(), map[string]any{
		"file_path": "c.go", "old_string": "alpha", "new_string": "X",
	}, env)
	if !out.IsError {
		t.Errorf("concurrent mod should be detected: %+v", out)
	}
}

func TestMultiEditAllOrNothing(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "d.go")
	writeFile(t, p, "one\ntwo\nthree\n")
	env := newEnv(t, dir)
	_, _ = ReadTool{}.Call(context.Background(), map[string]any{"file_path": "d.go"}, env)
	// Second edit's old_string won't match.
	out, _ := MultiEditTool{}.Call(context.Background(), map[string]any{
		"file_path": "d.go",
		"edits": []any{
			map[string]any{"old_string": "one", "new_string": "ONE"},
			map[string]any{"old_string": "MISSING", "new_string": "X"},
		},
	}, env)
	if !out.IsError {
		t.Fatalf("partial-failure should reject: %+v", out)
	}
	final, _ := os.ReadFile(p)
	if !strings.Contains(string(final), "one\n") {
		t.Errorf("file should be untouched on rollback: %q", final)
	}
}

func TestWriteNewFileSkipsReadCheck(t *testing.T) {
	dir := t.TempDir()
	env := newEnv(t, dir)
	out, _ := WriteTool{}.Call(context.Background(), map[string]any{
		"file_path": "fresh.txt", "content": "hello",
	}, env)
	if out.IsError {
		t.Fatalf("new-file write failed: %+v", out)
	}
	final, _ := os.ReadFile(filepath.Join(dir, "fresh.txt"))
	if string(final) != "hello" {
		t.Errorf("content wrong: %q", final)
	}
}

func TestGlobMatchesRecursive(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "src", "a.go"), "")
	writeFile(t, filepath.Join(dir, "src", "sub", "b.go"), "")
	writeFile(t, filepath.Join(dir, "src", "c.txt"), "")
	env := newEnv(t, dir)
	out, _ := GlobTool{}.Call(context.Background(), map[string]any{
		"pattern": "**/*.go",
	}, env)
	body := flatten(out)
	if !strings.Contains(body, "a.go") || !strings.Contains(body, "b.go") {
		t.Errorf("missing matches: %s", body)
	}
	if strings.Contains(body, "c.txt") {
		t.Errorf("txt should not match: %s", body)
	}
}

func TestGrepFindsMatches(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.go"), "package main\nfunc Hello() {}\n")
	writeFile(t, filepath.Join(dir, "b.go"), "package other\nfunc World() {}\n")
	env := newEnv(t, dir)
	out, _ := GrepTool{}.Call(context.Background(), map[string]any{
		"pattern": "Hello",
		"path":    dir,
	}, env)
	body := flatten(out)
	if !strings.Contains(body, "Hello") || !strings.Contains(body, "a.go") {
		t.Errorf("grep missed match: %s", body)
	}
}

func TestGrepNoMatches(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.go"), "package main\n")
	env := newEnv(t, dir)
	out, _ := GrepTool{}.Call(context.Background(), map[string]any{
		"pattern": "Nonexistent",
		"path":    dir,
	}, env)
	if !strings.Contains(flatten(out), "no matches") {
		t.Errorf("expected no-match note: %s", flatten(out))
	}
}
