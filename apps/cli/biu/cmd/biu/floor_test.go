package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/pkg/biumindkit"
)

func TestNormalizePreset(t *testing.T) {
	cases := map[string]string{
		"readonly":        "readonly",
		"workspace-write": "workspace-write",
		"full":            "full",
		"FULL":            "full",
		"":                "workspace-write", // 安全默认
		"garbage":         "workspace-write",
	}
	for in, want := range cases {
		if got := normalizePreset(in); got != want {
			t.Errorf("normalizePreset(%q)=%q want %q", in, got, want)
		}
	}
}

func TestIntersectPreset(t *testing.T) {
	cases := []struct{ daemon, brain, want string }{
		{"full", "readonly", "readonly"},          // brain 收窄
		{"readonly", "full", "readonly"},          // daemon 上限胜
		{"workspace-write", "full", "workspace-write"},
		{"workspace-write", "", "workspace-write"}, // brain 空 → daemon
		{"full", "", "full"},
		{"full", "workspace-write", "workspace-write"},
	}
	for _, c := range cases {
		if got := intersectPreset(c.daemon, c.brain); got != c.want {
			t.Errorf("intersect(%q,%q)=%q want %q", c.daemon, c.brain, got, c.want)
		}
	}
}

func TestResolveToolFloor(t *testing.T) {
	if resolveToolFloor("full") != nil {
		t.Error("full preset should yield nil floor (no capability restriction)")
	}
	// readonly：所有危险工具都被拒
	ro := resolveToolFloor("readonly")
	for _, tool := range []string{"Bash", "Edit", "Write", "Agent", "MultiEdit"} {
		if ro.Allows(tool) {
			t.Errorf("readonly should deny %q", tool)
		}
	}
	// workspace-write：文件写允许，shell/子agent 拒
	ww := resolveToolFloor("workspace-write")
	for _, tool := range []string{"Edit", "edit", "Write", "MultiEdit", "NotebookEdit"} {
		if !ww.Allows(tool) {
			t.Errorf("workspace-write should allow %q", tool)
		}
	}
	for _, tool := range []string{"Bash", "BashOutput", "Agent", "AgentBackground"} {
		if ww.Allows(tool) {
			t.Errorf("workspace-write should deny %q", tool)
		}
	}
}

func TestFloorPolicy(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "a.txt")
	outside := filepath.Join(t.TempDir(), "secret")
	roots := []string{root}
	ww := resolveToolFloor("workspace-write")

	allowDelegate := func(context.Context, biumindkit.PermissionRequest) biumindkit.PermissionDecision {
		return biumindkit.PermAllow
	}
	p := floorPolicy(roots, ww, allowDelegate)

	cases := []struct {
		name  string
		tool  string
		input map[string]any
		want  biumindkit.PermissionDecision
	}{
		{"bash denied by capability", "Bash", map[string]any{"command": "ls"}, biumindkit.PermDeny},
		{"agent denied by capability", "Agent", map[string]any{"prompt": "x"}, biumindkit.PermDeny},
		{"write inside roots → delegate allow", "Write", map[string]any{"file_path": inside}, biumindkit.PermAllow},
		{"write outside roots → deny", "Write", map[string]any{"file_path": outside}, biumindkit.PermDeny},
		{"read inside roots → delegate allow", "Read", map[string]any{"file_path": inside}, biumindkit.PermAllow},
		{"read outside roots → deny", "read", map[string]any{"file_path": outside}, biumindkit.PermDeny},
		{"no-path tool passes to delegate", "WebSearch", map[string]any{"query": "x"}, biumindkit.PermAllow},
	}
	for _, c := range cases {
		got := p(context.Background(), biumindkit.PermissionRequest{ToolName: c.tool, Input: c.input})
		if got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestFloorPolicy_NilDelegate(t *testing.T) {
	p := floorPolicy([]string{t.TempDir()}, nil, nil)
	got := p(context.Background(), biumindkit.PermissionRequest{ToolName: "WebSearch"})
	if got != biumindkit.PermDeny {
		t.Errorf("nil delegate should deny, got %v", got)
	}
}

func TestWithinRoots_SymlinkEscape(t *testing.T) {
	root := t.TempDir()
	// roots/../<sibling> 必须被判越界
	escape := filepath.Join(root, "..", "elsewhere")
	if withinRoots(escape, []string{root}) {
		t.Errorf("path traversal %q should be outside %q", escape, root)
	}
	if !withinRoots(filepath.Join(root, "sub", "f.txt"), []string{root}) {
		t.Error("nested path under root should be inside")
	}
	if withinRoots("/etc/passwd", nil) {
		t.Error("empty roots should reject everything")
	}
}

// 真符号链接逃逸：workspace 内部的软链指向 root 之外，经它访问的路径必须判
// 越界。TestWithinRoots_SymlinkEscape 只测了 `..` 字面遍历；这里测 EvalSymlinks
// 实际解析——这是 resolveSymlinks 存在的理由（防"看似在 root 内、解析后逃出去"）。
func TestWithinRoots_RealSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir() // root 之外的真实目录

	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	// 经 root 内软链访问外部已存在文件 → 解析到 outside → 越界拒。
	target := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(target, []byte("x"), 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}
	if withinRoots(filepath.Join(link, "secret.txt"), []string{root}) {
		t.Error("path through workspace symlink to external dir must be outside root")
	}
	// 经同一软链访问外部**尚未创建**的文件 → longest-ancestor 解析仍判越界。
	if withinRoots(filepath.Join(link, "not-yet.txt"), []string{root}) {
		t.Error("not-yet-created path through escaping symlink must still be outside root")
	}

	// 对照：root 内软链指向 root 内的另一处 → 解析后仍 inside → 放行。
	innerDir := filepath.Join(root, "real")
	if err := os.Mkdir(innerDir, 0o755); err != nil {
		t.Fatalf("mkdir inner: %v", err)
	}
	innerLink := filepath.Join(root, "alias")
	if err := os.Symlink(innerDir, innerLink); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if !withinRoots(filepath.Join(innerLink, "f.txt"), []string{root}) {
		t.Error("symlink resolving to a path inside root should be allowed")
	}
}

func TestResolveAllowedRoots_DefaultsToCwd(t *testing.T) {
	got := resolveAllowedRoots(nil)
	cwd, _ := os.Getwd()
	if len(got) != 1 || got[0] != cwd {
		t.Errorf("empty flag should default to [cwd]=%q, got %v", cwd, got)
	}
	got2 := resolveAllowedRoots([]string{"  ", ""})
	if len(got2) != 1 || got2[0] != cwd {
		t.Errorf("blank-only flag should default to [cwd], got %v", got2)
	}
}

func TestToolPath(t *testing.T) {
	cases := []struct {
		tool string
		in   map[string]any
		want string
	}{
		{"Read", map[string]any{"file_path": "/a"}, "/a"},
		{"Edit", map[string]any{"file_path": "/b"}, "/b"},
		{"Glob", map[string]any{"path": "/c"}, "/c"},
		{"Grep", map[string]any{"path": "/d"}, "/d"},
		{"NotebookEdit", map[string]any{"notebook_path": "/e"}, "/e"},
		{"Bash", map[string]any{"command": "ls"}, ""}, // 无路径语义
		{"WebSearch", map[string]any{"query": "x"}, ""},
	}
	for _, c := range cases {
		if got := toolPath(c.tool, c.in); got != c.want {
			t.Errorf("toolPath(%q)=%q want %q", c.tool, got, c.want)
		}
	}
}
