package permissions

import (
	"path/filepath"
	"testing"
)

// R6.3 / D7 回归测试：证明 R6.3 地板依赖的两条机制成立——
//   1. 设了 OriginalCwd + AddDirectories 后，越界只读路径不再 fail-open 自动放行
//      （而是落到 Ask；daemon floorPolicy 再把它升级成 Deny）。
//   2. deny 规则（biumindkit New 从 ToolFloor 注入）在 Decide 里赢过 acceptEdits /
//      read-only 自动放行——即便 mode=acceptEdits，被 deny 的 Bash/Edit 仍被拒。

func TestFloorGate_ReadOutsideRoots_NotAutoAllowed(t *testing.T) {
	root := t.TempDir()
	c := NewContext()
	c.SetOriginalCwd(root)
	c.AddDirectories(SrcCLIArg, []string{root})

	// 越界只读 Read → 不再自动放行，落到 Ask（工作目录门）。
	out := filepath.Join(t.TempDir(), "secret")
	d, reason := Decide(c, Request{Tool: "Read", IsReadOnly: true, Args: map[string]any{"file_path": out}})
	if d != DecideAsk {
		t.Fatalf("read outside roots: got %v (%s), want DecideAsk", d, reason.Kind)
	}
	if reason.Kind != "workingDir" {
		t.Errorf("reason=%q want workingDir", reason.Kind)
	}

	// 根内只读 Read → 仍自动放行。
	in := filepath.Join(root, "ok.txt")
	d, _ = Decide(c, Request{Tool: "Read", IsReadOnly: true, Args: map[string]any{"file_path": in}})
	if d != DecideAllow {
		t.Errorf("read inside roots: got %v, want DecideAllow", d)
	}
}

func TestFloorGate_FailOpenWithoutAnchor(t *testing.T) {
	// 没有 OriginalCwd / dirs（daemon 旧行为）→ 越界只读自动放行（即 R6.3 要修的
	// fail-open）。这条固化"为什么必须设 AllowedRoots"。
	c := NewContext()
	d, _ := Decide(c, Request{Tool: "Read", IsReadOnly: true, Args: map[string]any{"file_path": "/etc/passwd"}})
	if d != DecideAllow {
		t.Fatalf("without anchor read should fail-open allow (got %v) — proves AllowedRoots is required", d)
	}
}

func TestFloorGate_DenyRuleBeatsAcceptEdits(t *testing.T) {
	c := NewContext()
	c.SetMode(ModeAcceptEdits)
	// biumindkit New 注入的能力地板等价：deny Bash + Edit。
	c.AddRules(SrcCLIArg, BehaviorDeny, []string{"Bash", "Edit"})

	// acceptEdits 本会自动放行 Edit；deny 规则（step 4）须先赢。
	d, reason := Decide(c, Request{Tool: "Edit", Args: map[string]any{"file_path": "/tmp/x"}})
	if d != DecideDeny {
		t.Errorf("Edit under deny rule: got %v (%s), want DecideDeny", d, reason.Kind)
	}
	// Bash（非只读）同样被 deny 规则挡住。
	d, _ = Decide(c, Request{Tool: "bash", Args: map[string]any{"command": "ls"}})
	if d != DecideDeny {
		t.Errorf("bash under deny rule (case-folded): got %v, want DecideDeny", d)
	}
}
