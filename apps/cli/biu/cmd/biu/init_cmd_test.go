package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/config"
)

func TestWriteConfigRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	cfg := config.Defaults()
	cfg.Default.Mode = "direct"
	cfg.Default.Model = "claude-haiku-4-5"
	cfg.Providers["anthropic"] = config.ProviderSection{APIKey: "sk-test"}

	if err := writeConfig(path, cfg); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(body)
	if !strings.Contains(got, `mode = "direct"`) {
		t.Errorf("mode missing: %s", got)
	}
	if !strings.Contains(got, `model = "claude-haiku-4-5"`) {
		t.Errorf("model missing: %s", got)
	}
	if !strings.Contains(got, `api_key = "sk-test"`) {
		t.Errorf("api key missing: %s", got)
	}

	// File mode should be 0600 — secret material.
	st, _ := os.Stat(path)
	if mode := st.Mode().Perm(); mode != 0o600 {
		t.Errorf("config mode = %v, want 0600", mode)
	}

	// Round-trip via config.Load to ensure it parses cleanly.
	cfg2, _, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg2.Default.Mode != "direct" || cfg2.Providers["anthropic"].APIKey != "sk-test" {
		t.Errorf("roundtrip lost values: %+v", cfg2)
	}
}

func TestSelectModeFlagWins(t *testing.T) {
	// Flag set ⇒ no prompt.
	got := selectMode("direct", false)
	if got != "direct" {
		t.Errorf("flag should win: got %s", got)
	}
	// Empty flag + non-interactive ⇒ default cloud.
	got = selectMode("", true)
	if got != "cloud" {
		t.Errorf("non-interactive default: got %s", got)
	}
}

func TestScaffoldMemoryRefusesExisting(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile("BIUMIND.md", []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := scaffoldMemory(); err == nil {
		t.Errorf("expected refusal when BIUMIND.md exists")
	}
}

func TestScaffoldMemoryCreatesNew(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := scaffoldMemory(); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile("BIUMIND.md")
	if !strings.Contains(string(body), "Project notes") {
		t.Errorf("scaffold body missing: %s", body)
	}
}
