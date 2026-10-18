// resolvePermissionWiring 单测 —— payload 用户面权限模式 →
// (内核模式, PermissionPolicy) 映射（auto 权限模式落地，设计文档
// BiuMind-Auto-Permission-Mode-Design §4.3）。

package agent

import (
	"context"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/pkg/biumindkit"
)

func TestResolvePermissionWiring_KernelModes(t *testing.T) {
	cases := []struct {
		payloadMode      string
		wantKernelMode   string
		wantDenyFallback bool
	}{
		{"", "", false},        // 旧 payload → legacy PermSafe 路径
		{"auto", "auto", true}, // 内核 ModeAuto 全放行 + deny 兜底
		{"plan", "plan", true}, // 内核 ModePlan 只读 + deny 兜底
		{"manual", "default", true},
		{"default", "default", true},
		{"bogus", "default", true}, // 未知值按 ModeFromString 语义回落 default
	}
	for _, c := range cases {
		kernelMode, policy := resolvePermissionWiring(DefaultRegistry(), c.payloadMode)
		if kernelMode != c.wantKernelMode {
			t.Errorf("payload %q: kernelMode=%q want %q", c.payloadMode, kernelMode, c.wantKernelMode)
		}
		if policy == nil {
			t.Errorf("payload %q: policy nil", c.payloadMode)
		}
		// deny 兜底路径：policy 对一切 ask 都回 PermDeny（fail-closed）
		if c.wantDenyFallback {
			got := policy(context.Background(), biumindkit.PermissionRequest{ToolName: "read"})
			if got != biumindkit.PermDeny {
				t.Errorf("payload %q: deny fallback got %v want PermDeny", c.payloadMode, got)
			}
		}
	}
}

// 空串路径必须保持改造前行为一字不差：PermissionPolicy(reg, PermSafe)
// 的 Risk 分级 —— Low/Medium 放行、High 拒、未知工具拒。
func TestResolvePermissionWiring_EmptyModeKeepsLegacyPermSafe(t *testing.T) {
	kernelMode, policy := resolvePermissionWiring(DefaultRegistry(), "")
	if kernelMode != "" {
		t.Errorf("empty payload: kernelMode=%q want empty (内核不动 mode)", kernelMode)
	}
	cases := []struct {
		tool string
		want biumindkit.PermissionDecision
	}{
		{"read", biumindkit.PermAllow},   // RiskLow
		{"edit", biumindkit.PermAllow},   // RiskMedium
		{"bash", biumindkit.PermDeny},    // RiskHigh
		{"write", biumindkit.PermDeny},   // RiskHigh
		{"unknown", biumindkit.PermDeny}, // 未知工具防御性 deny
	}
	for _, c := range cases {
		got := policy(context.Background(), biumindkit.PermissionRequest{ToolName: c.tool})
		if got != c.want {
			t.Errorf("legacy path for %q: got %v want %v", c.tool, got, c.want)
		}
	}
}
