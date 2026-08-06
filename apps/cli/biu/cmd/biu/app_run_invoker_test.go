package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDevInvoker_ServesFixtureByActionName(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "fetch.json"),
		[]byte(`{"items":[{"id":"1"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	d := &devInvoker{opts: devOpts{MockDir: dir}}
	out, err := d.Invoke(t.Context(), "rss", "fetch", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	m := out.(map[string]any)
	items, ok := m["items"].([]any)
	if !ok || len(items) != 1 {
		t.Errorf("unexpected fixture: %+v", out)
	}
}

func TestDevInvoker_ServesFixtureByQualifiedName(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "rss.fetch.json"),
		[]byte(`{"qualified":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	d := &devInvoker{opts: devOpts{MockDir: dir}}
	out, err := d.Invoke(t.Context(), "rss", "fetch", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	m := out.(map[string]any)
	if m["qualified"] != true {
		t.Errorf("expected qualified fixture, got %+v", out)
	}
}

func TestDevInvoker_NoFixtureReturnsClearError(t *testing.T) {
	d := &devInvoker{opts: devOpts{MockDir: t.TempDir()}}
	_, err := d.Invoke(t.Context(), "rss", "fetch", json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "no fixture") {
		t.Errorf("expected no-fixture error, got %v", err)
	}
}

func TestDevInvoker_BadJSONInFixtureFails(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "fetch.json"), []byte("not json"), 0o644)
	d := &devInvoker{opts: devOpts{MockDir: dir}}
	_, err := d.Invoke(t.Context(), "rss", "fetch", json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "invalid JSON") {
		t.Errorf("expected invalid JSON error, got %v", err)
	}
}

func TestDevInvoker_NoMockMode_ReturnsHelpfulMessage(t *testing.T) {
	d := &devInvoker{opts: devOpts{}}
	_, err := d.Invoke(t.Context(), "rss", "fetch", json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "subproc invoke not yet wired") {
		t.Errorf("expected subproc-not-wired hint, got %v", err)
	}
}
