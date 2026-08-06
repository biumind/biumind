// EnterWorktree / ExitWorktree — minimal git worktree wrappers.
//
// Behaviour:
//
//   * EnterWorktree creates a new git worktree under
//     `.biumind/worktrees/<name>` on a fresh branch off HEAD, then
//     switches the engine's Cwd to the new path.
//   * ExitWorktree removes the worktree (or just leaves it on disk
//     when action="keep") and restores the original Cwd.
//
// We deliberately don't emulate a hook-driven worktree lifecycle for
// non-git VCSes — biu assumes git for P0. Outside a git repo the tool
// soft-errors so the model can fall back to a normal cd.

package interactive

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
)

// CwdSwitcher lets the worktree tools change the engine's working
// directory at runtime. The QueryEngine implementation can satisfy
// this with a simple mutex-protected setter.
type CwdSwitcher interface {
	Cwd() string
	// SetCwd switches the engine cwd. Returns an error when a remote-device
	// floor (R6.4) rejects the target as outside the allowed roots — the
	// cwd is left unchanged then. Normal (no-floor) runs never error.
	SetCwd(string) error
}

// EnterWorktreeTool creates and enters a new git worktree.
type EnterWorktreeTool struct {
	Cwd CwdSwitcher

	// State persists the (previous, current, branch) triple to a
	// sidecar JSON so `biu --resume <id>` can land back in the
	// worktree on a subsequent invocation. nil = persistence
	// disabled (worktree only survives the current process).
	State WorktreeStore

	mu       sync.Mutex
	previous string // the cwd we restore on ExitWorktree
	branch   string // the branch we created (only valid when previous != "")
}

// WorktreeStore is the persistence contract — kept as an interface
// so the worktree package can satisfy it without an import cycle.
// Methods mirror internal/worktree.Store.
type WorktreeStore interface {
	Save(state WorktreeState) error
	Delete(sessionID string) error
}

// WorktreeState mirrors internal/worktree.State on the call site.
// The interactive package can't import the worktree package because
// of an architectural rule we keep — this is a tiny copy of the
// fields to break the cycle.
type WorktreeState struct {
	SessionID string
	Previous  string
	Current   string
	Branch    string
}

func (*EnterWorktreeTool) Name() string { return "EnterWorktree" }

func (*EnterWorktreeTool) Description(_ map[string]any) string {
	return "Create a git worktree (new branch off HEAD) under " +
		".biumind/worktrees/<name>, then switch the session into it."
}

func (*EnterWorktreeTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "Optional worktree name. Defaults to a timestamped slug.",
			},
		},
	}
}

func (*EnterWorktreeTool) IsReadOnly(_ map[string]any) bool        { return false }
func (*EnterWorktreeTool) IsDestructive(_ map[string]any) bool     { return false }
func (*EnterWorktreeTool) IsConcurrencySafe(_ map[string]any) bool { return false }
func (*EnterWorktreeTool) InterruptBehavior() string               { return "block" }

func (e *EnterWorktreeTool) Call(ctx context.Context, input map[string]any, env *engine.ToolEnv) (*engine.ToolResultPayload, error) {
	if e.Cwd == nil {
		return softErr("EnterWorktree", "no cwd switcher wired"), nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	repo := e.Cwd.Cwd()
	if repo == "" {
		return softErr("EnterWorktree", "engine has no cwd"), nil
	}
	if !isGitRepo(ctx, repo) {
		return softErr("EnterWorktree", "not inside a git repository"), nil
	}
	name, _ := input["name"].(string)
	name = sanitizeName(name)
	branch := "biu/" + name
	target := filepath.Join(repo, ".biumind", "worktrees", name)

	out, err := runGit(ctx, repo, "worktree", "add", "-b", branch, target)
	if err != nil {
		return softErr("EnterWorktree", fmt.Sprintf("worktree add: %v\n%s", err, out)), nil
	}
	if err := e.Cwd.SetCwd(target); err != nil {
		// R6.4：远程设备 floor 拒了越界 worktree。worktree 已在磁盘建好，但不
		// 切入（fail-safe）。透明回模型，让它知道是策略所致。
		return softErr("EnterWorktree", fmt.Sprintf("cannot enter worktree: %v", err)), nil
	}
	e.previous = repo
	e.branch = branch

	// Persist the state so `biu --resume <session>` can re-enter on
	// the next run. Failures here are non-fatal — the worktree itself
	// is on disk, only the convenience auto-resume is lost.
	if e.State != nil && env != nil && env.AppState != nil {
		_ = e.State.Save(WorktreeState{
			SessionID: env.AppState.SessionID,
			Previous:  repo,
			Current:   target,
			Branch:    branch,
		})
	}

	return text(fmt.Sprintf("Worktree created at %s on branch %s. Engine cwd switched.",
		target, branch)), nil
}

// ExitWorktreeTool unwinds a previously-created worktree.
type ExitWorktreeTool struct {
	Cwd CwdSwitcher
	// Enter is the matching EnterWorktreeTool — used to find the
	// previous Cwd to restore. Exposed as a field so a test or
	// non-default wiring can inject an alternative.
	Enter *EnterWorktreeTool
}

func (ExitWorktreeTool) Name() string { return "ExitWorktree" }

func (ExitWorktreeTool) Description(_ map[string]any) string {
	return "Leave the active worktree. action='keep' preserves the " +
		"directory + branch on disk; action='remove' deletes both " +
		"(rejected when there are uncommitted changes)."
}

func (ExitWorktreeTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type": "string", "enum": []string{"keep", "remove"},
			},
			"discard_changes": map[string]any{"type": "boolean"},
		},
		"required": []string{"action"},
	}
}

func (ExitWorktreeTool) IsReadOnly(_ map[string]any) bool { return false }
func (ExitWorktreeTool) IsDestructive(input map[string]any) bool {
	a, _ := input["action"].(string)
	return a == "remove"
}
func (ExitWorktreeTool) IsConcurrencySafe(_ map[string]any) bool { return false }
func (ExitWorktreeTool) InterruptBehavior() string               { return "block" }

func (x ExitWorktreeTool) Call(ctx context.Context, input map[string]any, env *engine.ToolEnv) (*engine.ToolResultPayload, error) {
	if x.Cwd == nil || x.Enter == nil {
		return softErr("ExitWorktree", "no worktree session active"), nil
	}
	x.Enter.mu.Lock()
	defer x.Enter.mu.Unlock()

	prev := x.Enter.previous
	if prev == "" {
		return softErr("ExitWorktree", "not in a biu-managed worktree"), nil
	}
	current := x.Cwd.Cwd()
	action, _ := input["action"].(string)
	discard, _ := input["discard_changes"].(bool)

	if action == "remove" {
		if !discard && hasUncommitted(ctx, current) {
			return softErr("ExitWorktree",
				"refusing to remove: uncommitted changes (set discard_changes=true to override)"), nil
		}
		out, err := runGit(ctx, prev, "worktree", "remove", "--force", current)
		if err != nil {
			return softErr("ExitWorktree", fmt.Sprintf("remove: %v\n%s", err, out)), nil
		}
	}
	// prev 是进入前的 repo（=floor 锚点 roots[0]），floor 下恒在 roots 内、不会
	// 报错；万一报错也透明回模型。
	if err := x.Cwd.SetCwd(prev); err != nil {
		return softErr("ExitWorktree", fmt.Sprintf("cannot restore cwd: %v", err)), nil
	}
	x.Enter.previous = ""
	x.Enter.branch = ""

	// Best-effort sidecar cleanup so `biu --resume` doesn't try to
	// land back in a worktree the user just left. Missing file is
	// fine.
	if x.Enter.State != nil && env != nil && env.AppState != nil {
		_ = x.Enter.State.Delete(env.AppState.SessionID)
	}

	return text(fmt.Sprintf("Exited worktree (%s). Restored cwd to %s.",
		action, prev)), nil
}

// ─── helpers ──────────────────────────────────────────

func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func isGitRepo(ctx context.Context, dir string) bool {
	_, err := runGit(ctx, dir, "rev-parse", "--git-dir")
	return err == nil
}

func hasUncommitted(ctx context.Context, dir string) bool {
	out, err := runGit(ctx, dir, "status", "--porcelain")
	if err != nil {
		return false
	}
	return strings.TrimSpace(out) != ""
}

func sanitizeName(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		s = "wt"
	}
	// Stick to safe filename chars; replace anything else with -.
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z',
			c >= 'A' && c <= 'Z',
			c >= '0' && c <= '9',
			c == '-' || c == '_' || c == '.':
			out = append(out, c)
		default:
			out = append(out, '-')
		}
	}
	return string(out)
}
