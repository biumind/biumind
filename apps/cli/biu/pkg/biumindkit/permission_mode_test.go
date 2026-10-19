package biumindkit

import (
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/pkg/biumindkit/permissions"
)

func TestApplyPermissionMode_NilContext(t *testing.T) {
	if got := ApplyPermissionMode(nil, "auto"); got != permissions.ModeDefault {
		t.Errorf("nil ctx should be a no-op returning default; got %q", got)
	}
}

func TestApplyPermissionMode_UnknownFallsBackToDefault(t *testing.T) {
	ctx := permissions.NewContext()
	ctx.SetMode(permissions.ModeAuto)
	if got := ApplyPermissionMode(ctx, "bogus"); got != permissions.ModeDefault {
		t.Errorf("unknown mode should resolve to default; got %q", got)
	}
}

func TestApplyPermissionMode_PlanBookkeeping(t *testing.T) {
	ctx := permissions.NewContext()
	ctx.SetMode(permissions.ModeAuto)

	if got := ApplyPermissionMode(ctx, "plan"); got != permissions.ModePlan {
		t.Fatalf("enter plan: got %q", got)
	}
	if pre := ctx.PrePlanMode(); pre != permissions.ModeAuto {
		t.Fatalf("prePlanMode = %q, want auto", pre)
	}
	// 离开 plan 落到显式目标（不是恢复到 prePlan）——与 REPL /mode
	// 语义一致：ExitPlanMode 清簿记后 SetMode(newMode)。
	if got := ApplyPermissionMode(ctx, "manual"); got != permissions.ModeDefault {
		t.Errorf("plan → manual: got %q, want default", got)
	}
	if pre := ctx.PrePlanMode(); pre != "" {
		t.Errorf("prePlanMode should be cleared; got %q", pre)
	}
}
