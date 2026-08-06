package projcfg

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadMissing_ReturnsDefault(t *testing.T) {
	cfg, err := Read(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Agent.Default != "biu" || cfg.Agent.DefaultPermissionMode != "ask" {
		t.Fatalf("missing config should default: %+v", cfg)
	}
}

func TestWriteThenRead_RoundTrip(t *testing.T) {
	root := t.TempDir()
	in := Config{Agent: Agent{
		Default:               "claude",
		DefaultPermissionMode: "auto_edit",
		PromptPrefix:          "遵循 STYLE.md",
	}}
	if err := Write(root, in); err != nil {
		t.Fatal(err)
	}
	// 落在 .biu/config.toml。
	if _, err := os.Stat(filepath.Join(root, ".biu", "config.toml")); err != nil {
		t.Fatalf("config.toml not written: %v", err)
	}
	got, err := Read(root)
	if err != nil {
		t.Fatal(err)
	}
	if got.Agent.Default != "claude" || got.Agent.DefaultPermissionMode != "auto_edit" || got.Agent.PromptPrefix != "遵循 STYLE.md" {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
}

func TestReadGarbage_ReturnsDefault(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".biu"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".biu", "config.toml"),
		[]byte("this is not = valid toml ]["), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Read(root)
	if err != nil {
		t.Fatalf("garbage should not error: %v", err)
	}
	if cfg.Agent.Default != "biu" {
		t.Fatalf("garbage config should fall back to default: %+v", cfg)
	}
}

func TestReadPartial_NormalizesBlanks(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".biu"), 0o755); err != nil {
		t.Fatal(err)
	}
	// 只设 prompt_prefix,其余空 → normalize 补默认。
	if err := os.WriteFile(filepath.Join(root, ".biu", "config.toml"),
		[]byte("[agent]\nprompt_prefix = \"hi\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, _ := Read(root)
	if cfg.Agent.Default != "biu" || cfg.Agent.DefaultPermissionMode != "ask" || cfg.Agent.PromptPrefix != "hi" {
		t.Fatalf("partial normalize wrong: %+v", cfg)
	}
}
