package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// 写一个最小 ~/.claude.json 到临时 HOME,返回 cfg 路径。
func writeClaudeCfg(t *testing.T, home string, content map[string]any) string {
	t.Helper()
	raw, err := json.MarshalIndent(content, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(home, ".claude.json")
	if err := os.WriteFile(p, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func readClaudeCfg(t *testing.T, p string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestEnsureClaudeTrust_SetsTrustAndPreservesOtherFields(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cwd := "/Users/me/proj"

	cfg := writeClaudeCfg(t, home, map[string]any{
		"numStartups":   7.0, // 顶层无关字段须保留
		"installMethod": "brew",
		"projects": map[string]any{
			cwd: map[string]any{
				"hasTrustDialogAccepted": false,
				"allowedTools":           []any{"Bash"}, // 项目内其它字段须保留
			},
			"/Users/me/other": map[string]any{
				"hasTrustDialogAccepted": false, // 其它项目不应被动
			},
		},
	})

	if err := EnsureClaudeTrust(cwd); err != nil {
		t.Fatalf("EnsureClaudeTrust: %v", err)
	}

	got := readClaudeCfg(t, cfg)
	if got["numStartups"] != 7.0 || got["installMethod"] != "brew" {
		t.Errorf("顶层字段被破坏: %v", got)
	}
	projects := got["projects"].(map[string]any)
	proj := projects[cwd].(map[string]any)
	if proj["hasTrustDialogAccepted"] != true {
		t.Errorf("目标 cwd 未标记信任: %v", proj)
	}
	if tools, ok := proj["allowedTools"].([]any); !ok || len(tools) != 1 || tools[0] != "Bash" {
		t.Errorf("项目内其它字段未保留: %v", proj)
	}
	other := projects["/Users/me/other"].(map[string]any)
	if other["hasTrustDialogAccepted"] != false {
		t.Errorf("误改了其它项目的信任态: %v", other)
	}
}

func TestEnsureClaudeTrust_CreatesProjectEntryWhenMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cwd := "/Users/me/fresh"
	writeClaudeCfg(t, home, map[string]any{"projects": map[string]any{}})

	if err := EnsureClaudeTrust(cwd); err != nil {
		t.Fatalf("EnsureClaudeTrust: %v", err)
	}
	got := readClaudeCfg(t, filepath.Join(home, ".claude.json"))
	proj := got["projects"].(map[string]any)[cwd].(map[string]any)
	if proj["hasTrustDialogAccepted"] != true {
		t.Errorf("未为缺失的 cwd 建条目并信任: %v", proj)
	}
}

func TestEnsureClaudeTrust_IdempotentNoRewriteWhenAlreadyTrusted(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cwd := "/Users/me/proj"
	cfg := writeClaudeCfg(t, home, map[string]any{
		"projects": map[string]any{
			cwd: map[string]any{"hasTrustDialogAccepted": true},
		},
	})
	before, _ := os.Stat(cfg)

	if err := EnsureClaudeTrust(cwd); err != nil {
		t.Fatalf("EnsureClaudeTrust: %v", err)
	}
	after, _ := os.Stat(cfg)
	// 幂等:已信任则不重写,mtime 不变。
	if !before.ModTime().Equal(after.ModTime()) {
		t.Errorf("已信任时不应重写文件 (mtime 变了)")
	}
}

func TestEnsureClaudeTrust_MissingConfigReturnsError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// 不创建 ~/.claude.json
	if err := EnsureClaudeTrust("/Users/me/proj"); err == nil {
		t.Errorf("配置不存在时应返回 err(调用方据此记日志、不阻塞任务)")
	}
}
