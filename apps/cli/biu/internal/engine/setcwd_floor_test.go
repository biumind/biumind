package engine

import (
	"path/filepath"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/permissions"
)

// R6.4 / D7：SetCwd 在 floor（permission Context 有 OriginalCwd 锚点）下拒绝
// 越界目标，普通运行（无锚点）放行任意路径。

func newEngineWithPerms(p *permissions.Context, cwd string) *QueryEngine {
	return &QueryEngine{cwd: cwd, perms: p}
}

func TestSetCwd_FloorRejectsOutsideRoots(t *testing.T) {
	root := t.TempDir()
	p := permissions.NewContext()
	p.SetOriginalCwd(root)
	p.AddDirectories(permissions.SrcCLIArg, []string{root})
	e := newEngineWithPerms(p, root)

	// roots 内子目录 → 允许。
	sub := filepath.Join(root, "sub")
	if err := e.SetCwd(sub); err != nil {
		t.Fatalf("in-root SetCwd should succeed, got %v", err)
	}
	if e.Cwd() != sub {
		t.Fatalf("cwd=%q want %q", e.Cwd(), sub)
	}

	// roots 外 → 拒绝且 cwd 不变。
	outside := t.TempDir() // 另一个临时目录，不在 root 下
	if err := e.SetCwd(outside); err == nil {
		t.Fatal("out-of-root SetCwd should error")
	}
	if e.Cwd() != sub {
		t.Fatalf("cwd should be unchanged after rejected switch, got %q want %q", e.Cwd(), sub)
	}
}

func TestSetCwd_NoFloorAllowsAnything(t *testing.T) {
	// 无 OriginalCwd 锚点（普通 REPL/chat）→ 任意切换放行（回归保护）。
	p := permissions.NewContext()
	e := newEngineWithPerms(p, "/start")

	for _, dir := range []string{"/tmp/whatever", "/etc", t.TempDir()} {
		if err := e.SetCwd(dir); err != nil {
			t.Errorf("no-floor SetCwd(%q) should succeed, got %v", dir, err)
		}
		if e.Cwd() != dir {
			t.Errorf("cwd=%q want %q", e.Cwd(), dir)
		}
	}
}

func TestSetCwd_NilPermsAllows(t *testing.T) {
	e := newEngineWithPerms(nil, "/start")
	if err := e.SetCwd("/anywhere"); err != nil {
		t.Errorf("nil perms SetCwd should succeed, got %v", err)
	}
}
