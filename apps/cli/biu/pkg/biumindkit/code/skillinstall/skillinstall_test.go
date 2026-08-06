package skillinstall

import (
	"os"
	"path/filepath"
	"testing"
)

// mkHubSkill 在 ~/.biumind/skills/<name> 建一个带合法 frontmatter 的 SKILL.md。
func mkHubSkill(t *testing.T, name string) {
	t.Helper()
	hub, err := HubDir()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(hub, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	md := "---\nname: " + name + "\ndescription: skill " + name + ".\n---\n# " + name + "\nbody\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestInstallUninstall_Symlink(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	mkHubSkill(t, "foo")

	hub, _ := ListHub()
	if len(hub) != 1 || hub[0].Name != "foo" {
		t.Fatalf("ListHub = %+v", hub)
	}

	proj := t.TempDir()
	if err := Install(proj, "foo", "claude"); err != nil {
		t.Fatal(err)
	}
	// 幂等:再装一次不报错。
	if err := Install(proj, "foo", "claude"); err != nil {
		t.Fatalf("re-install should be idempotent: %v", err)
	}
	link := filepath.Join(proj, ".claude", "skills", "foo")
	target, lerr := os.Readlink(link)
	if lerr != nil {
		t.Fatalf("symlink not created: %v", lerr)
	}
	hubDir, _ := HubDir()
	if target != filepath.Join(hubDir, "foo") {
		t.Fatalf("symlink target = %q", target)
	}

	ins, _ := Installations(proj)
	if len(ins) != 1 || ins[0].Name != "foo" || ins[0].Agent != "claude" || ins[0].Health != "ok" {
		t.Fatalf("Installations = %+v", ins)
	}

	if err := Uninstall(proj, "foo", "claude"); err != nil {
		t.Fatal(err)
	}
	if ins, _ := Installations(proj); len(ins) != 0 {
		t.Fatalf("after uninstall expected empty, got %+v", ins)
	}
}

func TestInstall_UnknownSkillRejected(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := Install(t.TempDir(), "ghost", "claude"); err == nil {
		t.Fatal("installing a skill not in hub should error")
	}
}

func TestInstall_ConflictRealPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	mkHubSkill(t, "baz")

	proj := t.TempDir()
	dst := filepath.Join(proj, ".claude", "skills")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	// 目标处已有真实文件 → 安装应报冲突,不覆盖。
	if err := os.WriteFile(filepath.Join(dst, "baz"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Install(proj, "baz", "claude"); err == nil {
		t.Fatal("conflict with real file should error")
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "baz")); string(b) != "mine" {
		t.Fatal("conflicting real file must not be overwritten")
	}
}

func TestInstallations_BrokenAndDiverged(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	mkHubSkill(t, "foo")
	proj := t.TempDir()
	if err := Install(proj, "foo", "claude"); err != nil {
		t.Fatal(err)
	}
	// 删 hub 里的目标 → broken。
	hubDir, _ := HubDir()
	if err := os.RemoveAll(filepath.Join(hubDir, "foo")); err != nil {
		t.Fatal(err)
	}
	// codex 下放一个真实目录(非 symlink)→ diverged。
	div := filepath.Join(proj, ".codex", "skills", "bar")
	if err := os.MkdirAll(div, 0o755); err != nil {
		t.Fatal(err)
	}

	ins, _ := Installations(proj)
	var brokenOK, divergedOK bool
	for _, i := range ins {
		if i.Name == "foo" && i.Agent == "claude" && i.Health == "broken" {
			brokenOK = true
		}
		if i.Name == "bar" && i.Agent == "codex" && i.Health == "diverged" {
			divergedOK = true
		}
	}
	if !brokenOK || !divergedOK {
		t.Fatalf("expected broken+diverged, got %+v", ins)
	}
}

func TestUninstall_RefusesRealDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	proj := t.TempDir()
	real := filepath.Join(proj, ".claude", "skills", "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Uninstall(proj, "real", "claude"); err == nil {
		t.Fatal("uninstall should refuse to delete a real dir")
	}
	if _, err := os.Stat(real); err != nil {
		t.Fatal("real dir must survive refused uninstall")
	}
}
