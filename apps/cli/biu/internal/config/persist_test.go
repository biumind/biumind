package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// SetDefaultModel 落盘必须保住未知 section（map round-trip）并能读回。
func TestSetDefaultModel(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := SetDefaultModel("claude-sonnet-4-6"); err != nil {
		t.Fatalf("SetDefaultModel: %v", err)
	}
	cfg, _, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Default.Model != "claude-sonnet-4-6" {
		t.Errorf("model=%q", cfg.Default.Model)
	}
	// 新建文件要带默认 endpoint，cloud 模式离线也能定位 relay
	if cfg.Relay.Endpoint != "https://biumind.xxlab.tech" {
		t.Errorf("endpoint=%q", cfg.Relay.Endpoint)
	}
}

func TestSetDefaultModelPreservesUnknownSections(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".biu")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.toml")
	body := `[default]
mode = "cloud"

[model-relay]
endpoint = "https://relay.example.com"
virtual_key = "vk-1"

[[mcp_servers]]
name = "fs"
command = "npx"

[future_section]
custom_field = "keep-me"
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := SetDefaultModel("kimi-k3"); err != nil {
		t.Fatalf("SetDefaultModel: %v", err)
	}

	raw, _ := os.ReadFile(path)
	s := string(raw)
	// go-toml/v2 Marshal 用单引号 literal string 序列化
	for _, want := range []string{
		`model = 'kimi-k3'`,
		`endpoint = 'https://relay.example.com'`,
		`virtual_key = 'vk-1'`,
		`name = 'fs'`,
		`custom_field = 'keep-me'`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("file lost %q\nfile=%s", want, s)
		}
	}

	// 清除: model="" 删键 → Load 回到空 model
	if err := SetDefaultModel(""); err != nil {
		t.Fatalf("clear: %v", err)
	}
	cfg, _, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Default.Model != "" {
		t.Errorf("model=%q want empty", cfg.Default.Model)
	}
	if cfg.Relay.Endpoint != "https://relay.example.com" {
		t.Errorf("endpoint lost: %q", cfg.Relay.Endpoint)
	}
}
