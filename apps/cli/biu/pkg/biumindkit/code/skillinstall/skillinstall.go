// Package skillinstall 把 biumind 的全局 skill(~/.biumind/skills/<name>)以 symlink
// 安装进项目的 .claude/skills / .codex/skills,让外部 Claude Code / Codex 也能发现复用。
// hub 复用 biumind 既有 skill 存储,不另造第二套。
//
// 设计取舍:
//   - hub = ~/.biumind/skills(与「技能管理」同一存储)。仅磁盘上的 skill 可 symlink;
//     内置(embed)skill 不在此(无磁盘路径)。
//   - 安装态不另存 registry:直接扫项目 .claude/skills + .codex/skills 里指向 hub 的
//     symlink 反推(health = ok/broken/diverged),避免和文件系统真相漂移。
//   - symlink:macOS/Linux 直用 os.Symlink;Windows 需提权,失败透传错误(与 agent/pty
//     的 Windows 暂缓注记一致)。
package skillinstall

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/biumind/biumind/apps/cli/biu/internal/skills"
)

// agentSkillSubdir 返回某 agent 的项目内 skills 子目录(相对项目根)。
func agentSkillSubdir(agent string) (string, error) {
	switch agent {
	case "claude":
		return filepath.Join(".claude", "skills"), nil
	case "codex":
		return filepath.Join(".codex", "skills"), nil
	default:
		return "", fmt.Errorf("skillinstall: unknown agent %q (want claude|codex)", agent)
	}
}

// HubDir 返回全局 skill hub 目录 ~/.biumind/skills。
func HubDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".biumind", "skills"), nil
}

// HubSkill 是 hub 里一个可安装的 skill。
type HubSkill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Dir         string `json:"dir"` // hub 内 skill 目录绝对路径(symlink 源)
}

// ListHub 列出 hub 里全部 skill(扫 ~/.biumind/skills/*/SKILL.md,复用 internal/skills
// 的 frontmatter 解析)。hub 不存在 → 空列表(非错误)。
func ListHub() ([]HubSkill, error) {
	hub, err := HubDir()
	if err != nil {
		return nil, err
	}
	if _, serr := os.Stat(hub); os.IsNotExist(serr) {
		return nil, nil
	}
	reg := skills.NewRegistry()
	if lerr := reg.LoadDir(hub, "user"); lerr != nil {
		return nil, lerr
	}
	var out []HubSkill
	for _, s := range reg.All() {
		out = append(out, HubSkill{
			Name:        s.Name,
			Description: s.Description,
			Dir:         filepath.Dir(s.Path), // SKILL.md → 其所在 skill 目录
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// hubSkillDir 返回 hub 里某 skill 的目录,并校验其存在(防注入不存在的名字)。
func hubSkillDir(name string) (string, error) {
	if name == "" || filepath.Base(name) != name {
		return "", fmt.Errorf("skillinstall: invalid skill name %q", name)
	}
	hub, err := HubDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(hub, name)
	info, serr := os.Stat(dir)
	if serr != nil || !info.IsDir() {
		return "", fmt.Errorf("skillinstall: skill %q not found in hub", name)
	}
	return dir, nil
}

// Install 把 hub 里的 skill symlink 进项目的 <agent>/skills/<name>。已是指向同一 hub 目录
// 的 symlink → 幂等成功;目标已存在且非该 symlink → 报冲突(不覆盖用户文件)。
func Install(projectRoot, name, agent string) error {
	src, err := hubSkillDir(name)
	if err != nil {
		return err
	}
	sub, err := agentSkillSubdir(agent)
	if err != nil {
		return err
	}
	dstDir := filepath.Join(projectRoot, sub)
	if mkErr := os.MkdirAll(dstDir, 0o755); mkErr != nil {
		return mkErr
	}
	link := filepath.Join(dstDir, name)
	if existing, lerr := os.Readlink(link); lerr == nil {
		if existing == src {
			return nil // 幂等:已指向同一 hub 目录
		}
		// 指向别处的旧 symlink → 重指(我们管理的 symlink,可替换)。
		if rmErr := os.Remove(link); rmErr != nil {
			return rmErr
		}
	} else if _, statErr := os.Lstat(link); statErr == nil {
		// 存在但不是 symlink(真实文件/目录)→ 不覆盖,报冲突。
		return fmt.Errorf("skillinstall: %q already exists at target and is not a managed symlink", name)
	}
	return os.Symlink(src, link)
}

// Uninstall 移除项目内某 agent 的 skill symlink。仅删 symlink(不删真实目录,防误删用户文件);
// 不存在 → 幂等成功。
func Uninstall(projectRoot, name, agent string) error {
	if name == "" || filepath.Base(name) != name {
		return fmt.Errorf("skillinstall: invalid skill name %q", name)
	}
	sub, err := agentSkillSubdir(agent)
	if err != nil {
		return err
	}
	link := filepath.Join(projectRoot, sub, name)
	info, lerr := os.Lstat(link)
	if lerr != nil {
		if os.IsNotExist(lerr) {
			return nil
		}
		return lerr
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return fmt.Errorf("skillinstall: %q is a real path, not a managed symlink; refusing to delete", name)
	}
	return os.Remove(link)
}

// Installation 是项目内一条 skill 安装记录(扫 symlink 反推)。
type Installation struct {
	Name   string `json:"name"`
	Agent  string `json:"agent"`  // claude | codex
	Health string `json:"health"` // ok | broken | diverged
}

// Installations 扫项目的 .claude/skills + .codex/skills,反推已安装的 skill 及健康度:
//
//	ok       —— symlink 指向 hub 里仍存在的 skill 目录
//	broken   —— symlink 指向已不存在的目标
//	diverged —— 同名项是真实目录而非我们管理的 symlink(用户自建,不归我们管)
func Installations(projectRoot string) ([]Installation, error) {
	hub, err := HubDir()
	if err != nil {
		return nil, err
	}
	var out []Installation
	for _, agent := range []string{"claude", "codex"} {
		sub, _ := agentSkillSubdir(agent)
		dir := filepath.Join(projectRoot, sub)
		entries, rerr := os.ReadDir(dir)
		if rerr != nil {
			continue // 目录不存在 → 该 agent 无安装
		}
		for _, e := range entries {
			name := e.Name()
			link := filepath.Join(dir, name)
			target, lerr := os.Readlink(link)
			if lerr != nil {
				// 非 symlink(真实目录)且落在 hub 同名 → diverged;否则不归我们管,跳过。
				if e.IsDir() {
					out = append(out, Installation{Name: name, Agent: agent, Health: "diverged"})
				}
				continue
			}
			// 只认指向 hub 的 symlink(别处的不归我们管)。
			if filepath.Dir(target) != filepath.Clean(hub) {
				continue
			}
			health := "ok"
			if _, serr := os.Stat(target); serr != nil {
				health = "broken"
			}
			out = append(out, Installation{Name: name, Agent: agent, Health: health})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Agent < out[j].Agent
	})
	return out, nil
}
