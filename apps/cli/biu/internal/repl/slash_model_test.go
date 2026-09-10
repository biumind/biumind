package repl

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/biumind/biumind/apps/cli/biu/internal/client"
	"github.com/biumind/biumind/apps/cli/biu/internal/config"
	"github.com/biumind/biumind/apps/cli/biu/internal/modelcatalog"
)

// stubProvider satisfies client.Provider without network.
type stubProvider struct{}

func (stubProvider) Name() string { return "stub" }
func (stubProvider) ChatStream(_ context.Context, _ client.ChatRequest) (<-chan client.Frame, error) {
	ch := make(chan client.Frame)
	close(ch)
	return ch, nil
}

// /model <code> 切会话模型并持久化到 config（本地偏好）。
func TestSlashModel_switchPersists(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := model{}
	mm, _ := m.handleModel([]string{"/model", "kimi-k3"})
	got := mm.(model)
	if got.modelID != "kimi-k3" {
		t.Errorf("modelID=%q", got.modelID)
	}
	cfg, _, err := config.Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Default.Model != "kimi-k3" {
		t.Errorf("persisted=%q", cfg.Default.Model)
	}
}

// /model platform 清本地偏好。
func TestSlashModel_platformClearsPreference(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m := model{modelID: "kimi-k3"}
	mm, _ := m.handleModel([]string{"/model", "platform"})
	got := mm.(model)
	if got.modelID != "" {
		t.Errorf("modelID=%q want empty", got.modelID)
	}
	cfg, _, err := config.Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Default.Model != "" {
		t.Errorf("persisted=%q want empty", cfg.Default.Model)
	}
}

// 裸 /model 走注入的目录源, 渲染编号列表 + 平台默认标。
func TestSlashModel_bareFetchesCatalog(t *testing.T) {
	m := model{
		listModels: func(context.Context) ([]modelcatalog.Model, error) {
			return []modelcatalog.Model{
				{Code: "kimi-k3", DisplayName: "Kimi K3", Mode: "chat"},
				{Code: "claude-sonnet-4-6", DisplayName: "Sonnet", Mode: "chat", IsDefault: true},
				{Code: "text-embed-3", Mode: "embedding"}, // 非 chat 排除
			}, nil
		},
	}
	mm, cmd := m.handleModel([]string{"/model"})
	if cmd == nil {
		t.Fatal("bare /model should return a fetch cmd")
	}
	got := mm.(model)
	got.handleModelList(cmd().(modelListMsg))

	note := got.history[len(got.history)-1].Content
	for _, want := range []string{
		"available models",
		"claude-sonnet-4-6",
		"(platform default)",
		"pick with: /model <code>",
	} {
		if !strings.Contains(note, want) {
			t.Errorf("picker note missing %q\nnote=%s", want, note)
		}
	}
	if strings.Contains(note, "text-embed-3") {
		t.Errorf("embedding model leaked into picker\nnote=%s", note)
	}
	// 默认模型排最前
	if !strings.HasPrefix(strings.SplitN(note, "\n", 3)[1], "  1) claude-sonnet-4-6") {
		t.Errorf("default model should be first\nnote=%s", note)
	}
}

// 目录拉取失败 → 手动 usage 引导, 不炸。
func TestSlashModel_catalogErrorFallsBack(t *testing.T) {
	m := model{
		listModels: func(context.Context) ([]modelcatalog.Model, error) {
			return nil, errors.New("boom")
		},
	}
	mm, cmd := m.handleModel([]string{"/model"})
	got := mm.(model)
	got.handleModelList(cmd().(modelListMsg))
	note := got.history[len(got.history)-1].Content
	if !strings.Contains(note, "boom") || !strings.Contains(note, "/model <id>") {
		t.Errorf("unexpected fallback note: %s", note)
	}
}

// notReadyHint 三态。
func TestNotReadyHint(t *testing.T) {
	if h := (model{}).notReadyHint(); !strings.Contains(h, "/login") {
		t.Errorf("no provider should hint /login, got %q", h)
	}
	if h := (model{modelID: "x"}).notReadyHint(); !strings.Contains(h, "/login") {
		t.Errorf("provider nil should hint /login, got %q", h)
	}
	if h := (model{provider: stubProvider{}}).notReadyHint(); !strings.Contains(h, "/model") {
		t.Errorf("empty model should hint /model, got %q", h)
	}
	if h := (model{modelID: "x", provider: stubProvider{}}).notReadyHint(); h != "" {
		t.Errorf("ready model should return empty hint, got %q", h)
	}
	if h := (model{engine: nil, modelID: "x", provider: stubProvider{}}).notReadyHint(); h != "" {
		t.Errorf("ready legacy path should return empty hint, got %q", h)
	}
}

// 未就绪时 Enter 不发请求，转成引导 note（kimi 式懒引导门）。
func TestEnterNotReadyGates(t *testing.T) {
	ta := textarea.New()
	ta.SetValue("hello world")
	m := model{textarea: ta} // provider nil → not signed in
	got, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	out := got.(model)
	if out.state != stateIdle {
		t.Errorf("state=%v want idle (no send attempted)", out.state)
	}
	if len(out.history) != 1 || out.history[0].Role != "system" {
		t.Fatalf("expected one system note, got %+v", out.history)
	}
	if !strings.Contains(out.history[0].Content, "/login") {
		t.Errorf("note should guide /login: %q", out.history[0].Content)
	}
}

// /login 后 rewire: provider + 平台默认模型接回。
func TestHandleRewired(t *testing.T) {
	m := model{}
	m.handleRewired(rewiredMsg{provider: stubProvider{}, model: "claude-sonnet-4-6"})
	if m.provider == nil {
		t.Error("provider not wired")
	}
	if m.modelID != "claude-sonnet-4-6" {
		t.Errorf("modelID=%q", m.modelID)
	}
	if h := m.notReadyHint(); h != "" {
		t.Errorf("should be ready after rewire, hint=%q", h)
	}

	// 已有模型偏好时 rewire 不覆盖用户选择。
	m2 := model{modelID: "kimi-k3"}
	m2.handleRewired(rewiredMsg{provider: stubProvider{}, model: "claude-sonnet-4-6"})
	if m2.modelID != "kimi-k3" {
		t.Errorf("user preference overwritten: %q", m2.modelID)
	}

	// rewire 失败 → 重启引导, 不 panic。
	m3 := model{}
	m3.handleRewired(rewiredMsg{err: errors.New("still not logged in")})
	if m3.provider != nil {
		t.Error("failed rewire should not set provider")
	}
	note := m3.history[len(m3.history)-1].Content
	if !strings.Contains(note, "restart") {
		t.Errorf("failed rewire should hint restart, got %q", note)
	}
}
