package output

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuiltinsLoaded(t *testing.T) {
	r := NewRegistry()
	all := r.All()
	if len(all) < 4 {
		t.Errorf("expected ≥4 builtin styles, got %d", len(all))
	}
	if r.Get("default").Name != "default" {
		t.Errorf("default missing")
	}
	if r.Get("missing-x").Name != "default" {
		t.Errorf("unknown name should fall back to default")
	}
}

func TestApplyConciseAppendsPrompt(t *testing.T) {
	r := NewRegistry()
	concise := r.Get("concise")
	got := concise.Apply("base")
	if !strings.Contains(got, "base") || !strings.Contains(got, "preamble") {
		t.Errorf("apply: %q", got)
	}
	if def := r.Get("default"); def.Apply("base") != "base" {
		t.Errorf("default style should not change system")
	}
}

func TestLoadFileBasedStyleOverridesBuiltin(t *testing.T) {
	cwd := t.TempDir()
	t.Setenv("HOME", cwd)
	dir := filepath.Join(cwd, ".biumind", "output-styles")
	_ = os.MkdirAll(dir, 0o755)
	body := `---
name: concise
description: my override
---
custom concise prompt.`
	_ = os.WriteFile(filepath.Join(dir, "concise.md"), []byte(body), 0o644)

	r := NewRegistry()
	if err := r.Load(""); err != nil {
		t.Fatal(err)
	}
	got := r.Get("concise")
	if got.Source != "user" || !strings.Contains(got.Prompt, "custom concise prompt") {
		t.Errorf("user override not applied: %+v", got)
	}
}

func TestLoadIgnoresMissingDir(t *testing.T) {
	r := NewRegistry()
	if err := r.Load("/no/such/path"); err != nil {
		t.Fatal(err)
	}
}
