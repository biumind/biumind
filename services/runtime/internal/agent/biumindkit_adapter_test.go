// S11-3 BiumindkitAdapter + PermissionPolicy + AsBiumindkitTools 单测。
// 不需要真 Anthropic upstream —— 只验类型转换 + 决策逻辑。

package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/pkg/biumindkit"
)

func TestBiumindkitAdapter_NilSafe(t *testing.T) {
	if got := BiumindkitAdapter(nil); got != nil {
		t.Errorf("BiumindkitAdapter(nil) = %v, want nil", got)
	}
	if got := BiumindkitAdapter(&Tool{Name: "no-invoke"}); got != nil {
		t.Errorf("Tool with nil Invoke should return nil adapter")
	}
}

func TestBiumindkitAdapter_InvokeRoundtrip(t *testing.T) {
	tt := &Tool{
		Name:        "echo",
		Description: "echo arg.x",
		Risk:        RiskLow,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"x": map[string]any{"type": "string"},
			},
		},
		Invoke: func(_ context.Context, args map[string]any) (string, error) {
			x, _ := args["x"].(string)
			return "got:" + x, nil
		},
	}
	bk := BiumindkitAdapter(tt)
	if bk == nil {
		t.Fatal("adapter nil")
	}
	if bk.Name() != "echo" {
		t.Errorf("name=%q", bk.Name())
	}
	if !bk.IsReadOnly() {
		t.Errorf("RiskLow → expected IsReadOnly=true")
	}
	if bk.IsDestructive() {
		t.Errorf("RiskLow → expected IsDestructive=false")
	}
	if !bk.IsConcurrencySafe() {
		t.Errorf("RiskLow → expected IsConcurrencySafe=true")
	}
	out, err := bk.Run(context.Background(), map[string]any{"x": "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if out != "got:hi" {
		t.Errorf("Run output=%q want got:hi", out)
	}
}

func TestBiumindkitAdapter_RiskMapping(t *testing.T) {
	cases := []struct {
		risk            Risk
		readOnly        bool
		destructive     bool
		concurrencySafe bool
	}{
		{RiskLow, true, false, true},
		{RiskMedium, false, false, false},
		{RiskHigh, false, true, false},
	}
	for _, c := range cases {
		bk := BiumindkitAdapter(&Tool{
			Name: "x", Risk: c.risk,
			Invoke: func(_ context.Context, _ map[string]any) (string, error) { return "", nil },
		})
		if bk.IsReadOnly() != c.readOnly {
			t.Errorf("risk=%v IsReadOnly=%v want %v", c.risk, bk.IsReadOnly(), c.readOnly)
		}
		if bk.IsDestructive() != c.destructive {
			t.Errorf("risk=%v IsDestructive=%v want %v", c.risk, bk.IsDestructive(), c.destructive)
		}
		if bk.IsConcurrencySafe() != c.concurrencySafe {
			t.Errorf("risk=%v IsConcurrencySafe=%v want %v",
				c.risk, bk.IsConcurrencySafe(), c.concurrencySafe)
		}
	}
}

func TestRegistry_AsBiumindkitTools(t *testing.T) {
	reg := DefaultRegistry()
	bks := reg.AsBiumindkitTools()
	// Default registry 包 read / write / edit / grep / glob / bash 共 6 个
	if len(bks) != 6 {
		t.Errorf("AsBiumindkitTools len=%d want 6", len(bks))
	}
	// 每个工具都有 Name + Run 不为 nil
	for _, b := range bks {
		if b.Name() == "" {
			t.Errorf("tool with empty name in adapter output")
		}
	}
}

func TestPermissionPolicy_PermAuto(t *testing.T) {
	reg := DefaultRegistry()
	policy := PermissionPolicy(reg, PermAuto)
	for _, name := range []string{"read", "write", "bash"} {
		if got := policy(context.Background(), biumindkit.PermissionRequest{ToolName: name}); got != biumindkit.PermAllow {
			t.Errorf("PermAuto for %q: got %v want PermAllow", name, got)
		}
	}
}

func TestPermissionPolicy_PermReadOnly(t *testing.T) {
	reg := DefaultRegistry()
	policy := PermissionPolicy(reg, PermReadOnly)
	cases := []struct {
		tool string
		want biumindkit.PermissionDecision
	}{
		{"read", biumindkit.PermAllow},  // RiskLow
		{"grep", biumindkit.PermAllow},  // RiskLow
		{"glob", biumindkit.PermAllow},  // RiskLow
		{"edit", biumindkit.PermDeny},   // RiskMedium
		{"write", biumindkit.PermDeny},  // RiskHigh
		{"bash", biumindkit.PermDeny},   // RiskHigh
	}
	for _, c := range cases {
		got := policy(context.Background(), biumindkit.PermissionRequest{ToolName: c.tool})
		if got != c.want {
			t.Errorf("PermReadOnly for %q: got %v want %v", c.tool, got, c.want)
		}
	}
}

func TestPermissionPolicy_PermSafe(t *testing.T) {
	reg := DefaultRegistry()
	policy := PermissionPolicy(reg, PermSafe)
	cases := []struct {
		tool string
		want biumindkit.PermissionDecision
	}{
		{"read", biumindkit.PermAllow},  // Low
		{"edit", biumindkit.PermAllow},  // Medium
		{"bash", biumindkit.PermDeny},   // High
		{"write", biumindkit.PermDeny},  // High
	}
	for _, c := range cases {
		got := policy(context.Background(), biumindkit.PermissionRequest{ToolName: c.tool})
		if got != c.want {
			t.Errorf("PermSafe for %q: got %v want %v", c.tool, got, c.want)
		}
	}
}

func TestPermissionPolicy_UnknownToolDenied(t *testing.T) {
	reg := DefaultRegistry()
	policy := PermissionPolicy(reg, PermSafe)
	got := policy(context.Background(), biumindkit.PermissionRequest{ToolName: "nonexistent"})
	if got != biumindkit.PermDeny {
		t.Errorf("unknown tool: got %v want PermDeny (defensive)", got)
	}
}

func TestPermissionPolicy_NilRegistryDeniesAll(t *testing.T) {
	policy := PermissionPolicy(nil, PermSafe)
	got := policy(context.Background(), biumindkit.PermissionRequest{ToolName: "read"})
	if got != biumindkit.PermDeny {
		t.Errorf("nil registry: got %v want PermDeny", got)
	}
}

func TestPermissionPolicy_EmptyModeDefaultsToSafe(t *testing.T) {
	reg := DefaultRegistry()
	policy := PermissionPolicy(reg, "")
	// edit (Medium) should pass since "" defaults to PermSafe
	got := policy(context.Background(), biumindkit.PermissionRequest{ToolName: "edit"})
	if got != biumindkit.PermAllow {
		t.Errorf("empty mode: edit got %v want PermAllow (default = PermSafe)", got)
	}
	// bash (High) should still deny
	got = policy(context.Background(), biumindkit.PermissionRequest{ToolName: "bash"})
	if got != biumindkit.PermDeny {
		t.Errorf("empty mode: bash got %v want PermDeny", got)
	}
}

// 防御性 smoke：BuildBiumindkitAgent 缺 APIKey → 返回错误
func TestBuildBiumindkitAgent_RequiresAPIKey(t *testing.T) {
	logger := nopLogger{}
	_, err := BuildBiumindkitAgent(context.Background(), logger,
		BuildBiumindkitAgentInput{
			Model: "claude-haiku-4-5",
			Tools: DefaultRegistry(),
		})
	if err == nil {
		t.Fatal("expected APIKey-missing error")
	}
	if !strings.Contains(err.Error(), "AnthropicAPIKey") {
		t.Errorf("err msg=%q", err.Error())
	}
	_ = errors.Is // anti-unused
}

func TestBuildBiumindkitAgent_BasicConstruction(t *testing.T) {
	// 只验构造成功；不真发请求（biumindkit.New 不打网络）。Submit 时
	// 会打 fake APIKey 失败，不在本测试范围内
	logger := nopLogger{}
	ag, err := BuildBiumindkitAgent(context.Background(), logger,
		BuildBiumindkitAgentInput{
			AnthropicAPIKey: "sk-fake",
			Model:           "claude-haiku-4-5",
			System:          "you are a tester",
			Tools:           DefaultRegistry(),
			PermissionMode:  PermSafe,
		})
	if err != nil {
		t.Fatalf("BuildBiumindkitAgent: %v", err)
	}
	if ag == nil {
		t.Fatal("returned nil agent without error")
	}
	defer ag.Close()
	cost := ag.Cost()
	if cost.Model == "" {
		t.Errorf("model not set on cost snapshot")
	}
}

type nopLogger struct{}

func (nopLogger) Warn(_ string, _ ...any)  {}
func (nopLogger) Debug(_ string, _ ...any) {}
