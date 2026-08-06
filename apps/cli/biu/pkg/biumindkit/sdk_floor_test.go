package biumindkit

import "testing"

func TestToolFloor_Allows(t *testing.T) {
	var nilFloor *ToolFloor
	if !nilFloor.Allows("Bash") {
		t.Error("nil floor should allow everything")
	}
	f := &ToolFloor{AllowedTools: map[string]struct{}{"Edit": {}, "Write": {}}}
	if !f.Allows("edit") { // 大小写折叠
		t.Error("Allows should be case-insensitive (edit vs Edit)")
	}
	if f.Allows("Bash") {
		t.Error("Bash not in set → not allowed")
	}
}

func TestToolFloor_DeniedDangerous(t *testing.T) {
	// readonly：空允许集 → 全部危险工具被拒。
	ro := &ToolFloor{AllowedTools: map[string]struct{}{}}
	denied := ro.deniedDangerous()
	if len(denied) != len(floorDangerousTools) {
		t.Errorf("readonly should deny all %d dangerous tools, got %d", len(floorDangerousTools), len(denied))
	}
	// workspace-write：允许文件写 → 只剩 shell/子agent 被拒。
	ww := &ToolFloor{AllowedTools: map[string]struct{}{
		"Edit": {}, "edit": {}, "Write": {}, "write": {}, "MultiEdit": {}, "NotebookEdit": {},
	}}
	for _, d := range ww.deniedDangerous() {
		switch d {
		case "Edit", "edit", "Write", "write", "MultiEdit", "NotebookEdit":
			t.Errorf("workspace-write should NOT deny file tool %q", d)
		}
	}
	// 必须仍拒 Bash / Agent。
	denset := map[string]bool{}
	for _, d := range ww.deniedDangerous() {
		denset[d] = true
	}
	for _, must := range []string{"Bash", "BashOutput", "KillBash", "Agent", "AgentBackground"} {
		if !denset[must] {
			t.Errorf("workspace-write must deny %q", must)
		}
	}
}

func TestIsFloorDangerousTool(t *testing.T) {
	for _, d := range []string{"Bash", "bash", "Edit", "Write", "Agent", "AgentBackground", "MultiEdit"} {
		if !IsFloorDangerousTool(d) {
			t.Errorf("%q should be dangerous", d)
		}
	}
	for _, safe := range []string{"Read", "Glob", "Grep", "WebSearch", "WebFetch", "TodoWrite", "EnterPlanMode"} {
		if IsFloorDangerousTool(safe) {
			t.Errorf("%q should NOT be dangerous (path/capability-safe)", safe)
		}
	}
}
