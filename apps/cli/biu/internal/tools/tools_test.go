package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadTool(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.txt")
	_ = os.WriteFile(path, []byte("line1\nline2\nline3\n"), 0o644)
	r := Defaults()
	tool, _ := r.Get("read")
	out, err := tool.Invoke(context.Background(), map[string]any{"path": path})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "line1") || !strings.Contains(out, "line3") {
		t.Errorf("output missing lines: %s", out)
	}
}

func TestWriteAndEdit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "y.txt")
	r := Defaults()
	w, _ := r.Get("write")
	if _, err := w.Invoke(context.Background(), map[string]any{"path": path, "content": "hello there"}); err != nil {
		t.Fatal(err)
	}
	e, _ := r.Get("edit")
	if _, err := e.Invoke(context.Background(), map[string]any{"path": path, "old_string": "there", "new_string": "world"}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "hello world" {
		t.Errorf("got %q", got)
	}
}

func TestEditUniqueRequired(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "y.txt")
	_ = os.WriteFile(path, []byte("foo bar foo"), 0o644)
	r := Defaults()
	e, _ := r.Get("edit")
	if _, err := e.Invoke(context.Background(), map[string]any{"path": path, "old_string": "foo", "new_string": "x"}); err == nil {
		t.Fatal("expected error on non-unique match")
	}
}

func TestGlobTool(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"a.go", "b.go", "c.txt"} {
		_ = os.WriteFile(filepath.Join(dir, n), []byte(""), 0o644)
	}
	r := Defaults()
	g, _ := r.Get("glob")
	out, err := g.Invoke(context.Background(), map[string]any{"pattern": filepath.Join(dir, "*.go")})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "a.go") || !strings.Contains(out, "b.go") || strings.Contains(out, "c.txt") {
		t.Errorf("got %q", out)
	}
}

func TestBashSimple(t *testing.T) {
	r := Defaults()
	b, _ := r.Get("bash")
	out, err := b.Invoke(context.Background(), map[string]any{"cmd": "echo hello-from-biu"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "hello-from-biu") {
		t.Errorf("got %q", out)
	}
}
