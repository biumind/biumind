package files

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
	"github.com/biumind/biumind/apps/cli/biu/internal/state"
)

const sampleNotebook = `{
 "cells": [
  {"cell_type": "code", "source": "print('hello')", "id": "c1", "metadata": {}, "outputs": []},
  {"cell_type": "markdown", "source": "# Title", "id": "c2", "metadata": {}}
 ],
 "metadata": {"kernelspec": {"name": "python3"}},
 "nbformat": 4,
 "nbformat_minor": 5
}
`

func newNotebookEnv(t *testing.T) (*engine.ToolEnv, string) {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "demo.ipynb")
	if err := os.WriteFile(p, []byte(sampleNotebook), 0o644); err != nil {
		t.Fatal(err)
	}
	return &engine.ToolEnv{AppState: state.New(), Cwd: dir}, p
}

func TestNotebookReadListsCells(t *testing.T) {
	env, _ := newNotebookEnv(t)
	out, _ := NotebookReadTool{}.Call(context.Background(), map[string]any{
		"file_path": "demo.ipynb",
	}, env)
	if out.IsError {
		t.Fatalf("read failed: %+v", out)
	}
	body := flatten(out)
	if !strings.Contains(body, "cell 0 (code)") || !strings.Contains(body, "print('hello')") {
		t.Errorf("missing cell 0: %s", body)
	}
	if !strings.Contains(body, "cell 1 (markdown)") || !strings.Contains(body, "# Title") {
		t.Errorf("missing cell 1: %s", body)
	}
}

func TestNotebookEditReplaceWithoutReadFails(t *testing.T) {
	env, _ := newNotebookEnv(t)
	out, _ := NotebookEditTool{}.Call(context.Background(), map[string]any{
		"notebook_path": "demo.ipynb",
		"cell_number":   float64(0),
		"new_source":    "print('replaced')",
	}, env)
	if !out.IsError || !strings.Contains(out.SoftError, "not been read") {
		t.Errorf("unread edit must soft-error: %+v", out)
	}
}

func TestNotebookEditReplaceCell(t *testing.T) {
	env, p := newNotebookEnv(t)
	_, _ = NotebookReadTool{}.Call(context.Background(), map[string]any{
		"file_path": "demo.ipynb",
	}, env)
	out, _ := NotebookEditTool{}.Call(context.Background(), map[string]any{
		"notebook_path": "demo.ipynb",
		"cell_number":   float64(0),
		"new_source":    "print('replaced')",
	}, env)
	if out.IsError {
		t.Fatalf("replace failed: %+v", out)
	}
	body, _ := os.ReadFile(p)
	var nb map[string]any
	if err := json.Unmarshal(body, &nb); err != nil {
		t.Fatal(err)
	}
	cells := nb["cells"].([]any)
	first := cells[0].(map[string]any)
	if first["source"].(string) != "print('replaced')" {
		t.Errorf("source not replaced: %+v", first)
	}
	// Metadata + nbformat preserved.
	if _, ok := nb["metadata"]; !ok {
		t.Errorf("metadata lost on round-trip")
	}
	if nb["nbformat"].(float64) != 4 {
		t.Errorf("nbformat lost: %+v", nb["nbformat"])
	}
}

func TestNotebookEditInsertCell(t *testing.T) {
	env, p := newNotebookEnv(t)
	_, _ = NotebookReadTool{}.Call(context.Background(), map[string]any{"file_path": "demo.ipynb"}, env)
	out, _ := NotebookEditTool{}.Call(context.Background(), map[string]any{
		"notebook_path": "demo.ipynb",
		"cell_number":   float64(0),
		"edit_mode":     "insert",
		"new_source":    "print('NEW')",
	}, env)
	if out.IsError {
		t.Fatal(out)
	}
	body, _ := os.ReadFile(p)
	var nb map[string]any
	_ = json.Unmarshal(body, &nb)
	cells := nb["cells"].([]any)
	if len(cells) != 3 {
		t.Errorf("expected 3 cells after insert, got %d", len(cells))
	}
	if cells[1].(map[string]any)["source"].(string) != "print('NEW')" {
		t.Errorf("inserted cell missing")
	}
}

func TestNotebookEditDeleteCell(t *testing.T) {
	env, p := newNotebookEnv(t)
	_, _ = NotebookReadTool{}.Call(context.Background(), map[string]any{"file_path": "demo.ipynb"}, env)
	out, _ := NotebookEditTool{}.Call(context.Background(), map[string]any{
		"notebook_path": "demo.ipynb",
		"cell_number":   float64(1),
		"edit_mode":     "delete",
	}, env)
	if out.IsError {
		t.Fatal(out)
	}
	body, _ := os.ReadFile(p)
	var nb map[string]any
	_ = json.Unmarshal(body, &nb)
	cells := nb["cells"].([]any)
	if len(cells) != 1 {
		t.Errorf("expected 1 cell after delete, got %d", len(cells))
	}
}

func TestNotebookEditFindByCellID(t *testing.T) {
	env, _ := newNotebookEnv(t)
	_, _ = NotebookReadTool{}.Call(context.Background(), map[string]any{"file_path": "demo.ipynb"}, env)
	out, _ := NotebookEditTool{}.Call(context.Background(), map[string]any{
		"notebook_path": "demo.ipynb",
		"cell_id":       "c2",
		"new_source":    "# Replaced via id",
	}, env)
	if out.IsError {
		t.Fatalf("cell_id edit failed: %+v", out)
	}
	if !strings.Contains(flatten(out), "cell 1") {
		t.Errorf("expected idx 1 in result: %s", flatten(out))
	}
}
