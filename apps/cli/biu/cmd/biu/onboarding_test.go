package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/config"
	"github.com/biumind/biumind/apps/cli/biu/internal/modelcatalog"
)

// resolveModelWithCatalog: 平台默认标记存在 → 直接返回（不交互）。
func TestResolveModelWithCatalog_PlatformDefault(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[
			{"code":"kimi-k3","mode":"chat"},
			{"code":"claude-sonnet-4-6","mode":"chat","is_default_chat":true}
		]}`))
	}))
	defer srv.Close()

	cfg := config.Defaults()
	cfg.Relay.Endpoint = srv.URL
	cfg.Relay.VirtualKey = "vk-test"

	got := resolveModelWithCatalog(context.Background(), cfg)
	if got != "claude-sonnet-4-6" {
		t.Errorf("got %q want platform default claude-sonnet-4-6", got)
	}
}

// 目录拉取失败（网络错）→ 空串 + 不 panic（REPL 懒引导兜底）。
func TestResolveModelWithCatalog_NetworkFailure(t *testing.T) {
	cfg := config.Defaults()
	cfg.Relay.Endpoint = "http://127.0.0.1:1" // 不可达
	cfg.Relay.VirtualKey = "vk"
	if got := resolveModelWithCatalog(context.Background(), cfg); got != "" {
		t.Errorf("network failure should return empty, got %q", got)
	}
}

// filterPickable: 默认排最前，其余按 code 字典序。
func TestFilterPickable(t *testing.T) {
	got := filterPickable([]modelcatalog.Model{
		{Code: "zeta"},
		{Code: "alpha"},
		{Code: "default-one", IsDefault: true},
		{Code: "mid"},
	})
	want := []string{"default-one", "alpha", "mid", "zeta"}
	for i, m := range got {
		if m.Code != want[i] {
			t.Fatalf("order[%d]=%q want %q (full: %v)", i, m.Code, want[i], got)
		}
	}
}

// config 已存在 → onboarding 不触发（幂等，不打扰老用户）。
func TestRunFirstLaunchOnboarding_ExistingConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".biu")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("[default]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if runFirstLaunchOnboarding(context.Background()) {
		t.Error("onboarding should not run when config exists")
	}
}
