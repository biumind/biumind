// Engine-native Bash tool with sandbox + streaming output.
//
// Why a new Bash when tools.Bash already exists? The legacy adapter
// returns a single string at the end — the LLM can't see "first 10
// lines look fine, last lines are an error" until the whole command
// finishes. This native implementation:
//
//   * pipes stdout/stderr line-by-line through env.OnProgress
//     (renders as a live ⏺ Bash row in the TUI)
//   * wraps the command in sandbox.Wrap so destructive commands
//     can't escape the project root on macOS / Linux
//   * surfaces the sandbox mode + exit code in the final result so
//     the model knows whether the run was confined.

package web

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/internal/bashsec"
	"github.com/biumind/biumind/apps/cli/biu/internal/bgtask"
	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
	"github.com/biumind/biumind/apps/cli/biu/internal/permissions"
	"github.com/biumind/biumind/apps/cli/biu/internal/sandbox"
	"github.com/biumind/biumind/apps/cli/biu/internal/state"
	"github.com/biumind/biumind/apps/cli/biu/pkg/exechost"
)

type BashTool struct {
	// AllowNetworkByDefault flips the per-call default. UI / settings
	// can keep this off and let the model opt in via input.allow_net=true.
	AllowNetworkByDefault bool

	// BgTasks, when non-nil, enables `run_in_background: true` —
	// long-running commands fork into the bgtask store and Bash
	// returns immediately with a task ID. nil means the parameter
	// is silently ignored (Bash always runs foreground).
	BgTasks *bgtask.Store

	// Layered sandbox configuration. Forwarded directly to
	// sandbox.Options on every call. Empty slices = use the simple
	// defaults (cwd-write + AllowNetworkByDefault).
	//
	// These fields are NOT per-call inputs — wiring sets them once
	// at construction time so the LLM can't loosen them by passing
	// flags. Tightening (e.g. `dangerously_disable_sandbox: true`)
	// is the only direction the LLM can move, and even that
	// requires a permission ask in the runner.
	FSReadDeny             []string
	FSReadAllowWithinDeny  []string
	FSWriteAllowExtra      []string
	FSWriteDenyWithinAllow []string

	// PermCtx, when non-nil, contributes its
	// AdditionalDirectoryPaths() to the sandbox's writable roots on
	// every call. Held as a pointer so /add-dir at the REPL or a
	// settings.json reload propagates without a tool re-registration:
	// the sandbox allowWrite list re-derives from settings + bootstrap
	// state on every refresh.
	//
	// nil is fine — falls back to the static FSWriteAllowExtra slice.
	PermCtx *permissions.Context

	// ExecHost (Runtime v3 轴 B) 决定命令往哪落地执行。nil 或 Mode()==local
	// → 走下面的本机富流式路径（今天的行为，零变化）。Mode()==cloud/none
	// → divert 到 ExecHost.Exec：cloud 当前是 stub（R5 接 services/sandbox），
	// none 直接拒绝。这道分流保证 runtime_env_mode=cloud 的 agent **不会**
	// 静默在 daemon 本机跑命令（安全：见 v3 风险 #4）。
	ExecHost exechost.Host
}

func (BashTool) Name() string { return "Bash" }

// Description returns the long-form steering prompt plus a
// dynamically-rendered sandbox section listing the active fs
// allow/deny lists. The base text is identical across calls so the
// prompt cache stays warm; only the sandbox section varies based on
// the BashTool struct fields.
//
// We use a value receiver here even though we read fields off the
// struct, matching the rest of the BashTool methods. That means a
// zero-value BashTool emits no sandbox section (defaults only); a
// configured one prints its lists so the model knows the
// constraints up front instead of discovering them via failed
// commands.
func (b BashTool) Description(_ map[string]any) string {
	// Long-form Description — steers the model toward the dedicated
	// file/search tools instead of shelling out, and toward correct
	// parallelism / sleep / git habits. (Anthropic-internal sections
	// like undercover instructions, attribution texts, embedded-tools
	// experiments, and feature-flagged Monitor are deliberately not
	// carried over.)
	//
	// Stable text — included verbatim in every tool catalog frame.
	// Keep it under ~2KB so it doesn't dominate the system prompt.
	return `Executes a given bash command and returns its output.

The working directory persists between commands, but shell state does not. The shell is sandboxed (macOS sandbox-exec / Linux bwrap when present); writes outside the project directory are blocked unless explicitly allowed.

IMPORTANT: Avoid using this tool to run ` + "`find`, `grep`, `cat`, `head`, `tail`, `sed`, `awk`, or `echo`" + ` commands unless explicitly instructed or you have verified that a dedicated tool cannot accomplish your task. Prefer:

- File search: use Glob (NOT find or ls)
- Content search: use Grep (NOT grep or rg directly)
- Read files: use Read (NOT cat/head/tail)
- Edit files: use Edit (NOT sed/awk)
- Write files: use Write (NOT echo >/cat <<EOF)
- Communication: output text directly (NOT echo/printf)

The dedicated tools provide better permission flow, structured results, and don't burn shell roundtrip latency.

# Instructions

- If your command will create new directories or files, first run ` + "`ls`" + ` to verify the parent directory exists.
- Always quote file paths that contain spaces (e.g., ` + "`cd \"path with spaces\"`" + `).
- Maintain the current working directory by using absolute paths and avoiding ` + "`cd`" + `. You may use ` + "`cd`" + ` if the user explicitly asks.
- ` + "`timeout_sec`" + ` is optional (default 120s, max 600s). Long-running work that doesn't need an inline result should use ` + "`run_in_background: true`" + ` — the agent loop is automatically notified when it exits, so you don't poll.
- When issuing multiple commands:
  - If the commands are independent, make multiple Bash calls in a single message so they run in parallel.
  - If they must run sequentially, use a single Bash call with ` + "`&&`" + ` to chain. Use ` + "`;`" + ` only when you don't care about earlier failures.
  - Do NOT use newlines to separate commands (newlines inside quoted strings are fine).
- For git commands:
  - Prefer creating a new commit over amending an existing one.
  - Before destructive operations (` + "`git reset --hard`, `git push --force`, `git checkout --`, `git clean -f`" + `), prefer a safer alternative when one exists.
  - Never skip hooks (` + "`--no-verify`, `--no-gpg-sign`" + `) unless the user asked.
- Avoid unnecessary ` + "`sleep`" + `:
  - Don't sleep between commands that can run immediately.
  - Long-running work → ` + "`run_in_background: true`" + ` rather than a sleep loop.
  - Don't retry failing commands in a sleep loop — diagnose the root cause.
  - If you must poll, prefer a check command (e.g. ` + "`gh run view`" + `) and keep waits short (1-5s).

# Background tasks

Set ` + "`run_in_background: true`" + ` to fork the command and return a task ID immediately. Poll captured output via BashOutput / TaskOutput; terminate via KillBash / TaskStop. Use this for dev servers, log tails, watchers — anything where the inline result isn't useful.

# Sandbox` + b.sandboxSection() + `

Set ` + "`dangerously_disable_sandbox: true`" + ` per call when a legitimate command needs filesystem or network access the active sandbox forbids. The permission ask flow gates every disable; the next call returns to sandboxed defaults regardless.`
}

// sandboxSection renders a brief summary of the active fs
// allow/deny lists, prepended into the Description so the model
// reads the constraints before making a tool call.
//
// Two fixed lines always render — the cwd-write hint and the
// network policy — so the model never has to guess whether the
// section "applies". Extra lines surface only the layered lists
// the caller actually configured; suppressing empty entries keeps
// the prompt tight when defaults are in use.
func (b BashTool) sandboxSection() string {
	lines := []string{
		"  - Default policy: writes restricted to project cwd; reads everywhere unless listed below.",
	}
	if len(b.FSReadDeny) > 0 {
		lines = append(lines, "  - Read-blocked paths: "+strings.Join(b.FSReadDeny, ", "))
	}
	if len(b.FSReadAllowWithinDeny) > 0 {
		lines = append(lines, "  - Read-allowed within blocks: "+strings.Join(b.FSReadAllowWithinDeny, ", "))
	}
	if eff := b.effectiveWriteAllowExtra(); len(eff) > 0 {
		lines = append(lines, "  - Extra writable roots: "+strings.Join(eff, ", "))
	}
	if len(b.FSWriteDenyWithinAllow) > 0 {
		lines = append(lines, "  - Write-blocked within allows: "+strings.Join(b.FSWriteDenyWithinAllow, ", "))
	}
	if b.AllowNetworkByDefault {
		lines = append(lines, "  - Network: allowed by default")
	} else {
		lines = append(lines, "  - Network: blocked by default; pass `allow_network: true` to opt in per call")
	}
	return "\n\n" + strings.Join(lines, "\n")
}

// effectiveWriteAllowExtra returns the union of the static
// FSWriteAllowExtra slice (set at construction from settings.sandbox)
// and the live ctx.AdditionalDirectoryPaths() (sourced from /add-dir,
// settings.permissions.additionalDirectories, --add-dir, PWD symlink
// case).
//
// Re-built on every call so a /add-dir issued mid-session takes
// effect on the next Bash invocation without restarting the engine.
// Cost is dominated by an RLock on the Context, which is cheap.
func (b BashTool) effectiveWriteAllowExtra() []string {
	if b.PermCtx == nil {
		return b.FSWriteAllowExtra
	}
	extras := b.PermCtx.AdditionalDirectoryPaths()
	if len(extras) == 0 {
		return b.FSWriteAllowExtra
	}
	if len(b.FSWriteAllowExtra) == 0 {
		return extras
	}
	// Union with dedup. Static set first (preserves operator
	// configuration order), then ctx-sourced.
	seen := make(map[string]struct{}, len(b.FSWriteAllowExtra)+len(extras))
	out := make([]string, 0, len(b.FSWriteAllowExtra)+len(extras))
	for _, p := range b.FSWriteAllowExtra {
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	for _, p := range extras {
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return out
}

func (BashTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{
				"type":        "string",
				"description": "Shell command (interpreted by /bin/sh -c).",
			},
			"timeout_sec": map[string]any{
				"type":        "integer",
				"description": "Hard timeout (foreground only). 0 = 120s default.",
			},
			"allow_network": map[string]any{
				"type":        "boolean",
				"description": "Permit outbound network access for this call.",
			},
			"run_in_background": map[string]any{
				"type":        "boolean",
				"description": "Fork the command into the background and return a task ID immediately. Use BashOutput to poll captured output and KillBash to terminate. Only set when you don't need the result inline (long log tails, dev servers, watchers).",
			},
			"dangerously_disable_sandbox": map[string]any{
				"type":        "boolean",
				"description": "Bypass the sandbox layer for this single call. Required when a legitimate command needs filesystem or network access the active sandbox forbids (writes to ~/.config/git, network egress to a non-allowed host, etc.). Default false. Per-call only — the next call returns to sandboxed defaults regardless of this flag.",
			},
		},
		"required": []string{"command"},
	}
}

// Bash is destructive by default. The runner already asks before
// running so we don't second-guess the rule engine here.
func (BashTool) IsReadOnly(_ map[string]any) bool        { return false }
func (BashTool) IsDestructive(_ map[string]any) bool     { return true }
func (BashTool) IsConcurrencySafe(_ map[string]any) bool { return false }

// Block on Ctrl-C: bash side-effects are easier to reason about when
// they finish.
func (BashTool) InterruptBehavior() string { return "block" }

// Warnings flags destructive command patterns (rm -rf, git reset
// --hard, terraform destroy, …) so the permission ask dialog can
// pre-warn the user. Advisory only — the permission decision still
// flows through the rule engine.
func (BashTool) Warnings(input map[string]any) []string {
	cmd, _ := input["command"].(string)
	if cmd == "" {
		return nil
	}
	if w := bashsec.Warning(cmd); w != "" {
		return []string{w}
	}
	return nil
}

func (b BashTool) Call(ctx context.Context, input map[string]any, env *engine.ToolEnv) (*engine.ToolResultPayload, error) {
	command, _ := input["command"].(string)
	if strings.TrimSpace(command) == "" {
		// 模型友好的错误 —— 给具体例子让 LLM 下一轮能自我修正。
		// 实测 glm-5.1 思考 + 多工具调用时容易 emit arguments="{}"
		// (空参), 普通 "command is required" 太短模型解析不出修复方向,
		// 还可能直接 upstream 400。给完整范例 + 列出可选字段即可。
		return softErr("Bash",
			"missing required parameter 'command'. "+
				"Please retry with arguments like: "+
				`{"command": "ls -la", "timeout_sec": 60, "allow_network": false}. `+
				"Required: command (string). Optional: timeout_sec (number, default 120), allow_network (bool, default false)."), nil
	}
	timeout := 120 * time.Second
	if t, ok := input["timeout_sec"].(float64); ok && t > 0 {
		timeout = time.Duration(t) * time.Second
	}
	allowNet := b.AllowNetworkByDefault
	if v, ok := input["allow_network"].(bool); ok {
		allowNet = v
	}
	disableSandbox, _ := input["dangerously_disable_sandbox"].(bool)

	cwd := ""
	if env != nil {
		cwd = env.Cwd
	}

	// Runtime v3 轴 B：非 local 执行环境（cloud/none）分流到 ExecHost，不走
	// 下面的本机富流式路径。cloud 当前是 stub（R5 接 services/sandbox），
	// none 直接拒绝。这保证 runtime_env_mode=cloud 的 agent 不会静默在
	// daemon 本机执行命令。nil / local → 落到原路径，行为零变化。
	if b.ExecHost != nil && b.ExecHost.Mode() != exechost.ModeLocal {
		ec, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		res, err := b.ExecHost.Exec(ec, exechost.ExecRequest{
			Argv:         []string{"/bin/sh", "-c", command},
			Workdir:      cwd,
			TimeoutSec:   int(timeout / time.Second),
			AllowNetwork: allowNet,
		})
		if err != nil {
			return softErr("Bash", err.Error()), nil
		}
		out := res.Stdout
		if res.Stderr != "" {
			out += "\n" + res.Stderr
		}
		return &engine.ToolResultPayload{
			Content: []state.ContentBlock{{Type: state.ContentText, Text: out}},
		}, nil
	}

	// Background path: detach from the engine's context (so an Agent
	// dispatch ending doesn't murder the user's dev server) and
	// register with the shared store. Sandbox is intentionally
	// SKIPPED for backgrounded commands — the typical use cases
	// (long log tails, dev server, file watchers) need network +
	// outside-cwd access and the user has implicitly consented by
	// asking for run_in_background.
	if bg, _ := input["run_in_background"].(bool); bg {
		if b.BgTasks == nil {
			return softErr("Bash", "run_in_background requested but background-task store is not wired in this build"), nil
		}
		task, err := b.BgTasks.Spawn(context.Background(), bgtask.SpawnRequest{
			Command: command,
			Cwd:     cwd,
		})
		if err != nil {
			return softErr("Bash", "background spawn: "+err.Error()), nil
		}
		body := fmt.Sprintf("Background task started with id=%s. "+
			"Use BashOutput{task_id: %q} to poll captured output, "+
			"KillBash{task_id: %q} to terminate.",
			task.ID, task.ID, task.ID)
		return &engine.ToolResultPayload{
			Content: []state.ContentBlock{{Type: state.ContentText, Text: body}},
		}, nil
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd, mode := sandbox.Wrap(cctx, command, sandbox.Options{
		Cwd: cwd, AllowNetwork: allowNet,
		// `dangerously_disable_sandbox: true` flips Mode to "off"
		// regardless of platform. The permission ask flow is the
		// gate; the runtime here just honours the input.
		Disable: disableSandbox,
		// Layered fs allow/deny lists from BashTool config (FSReadDeny
		// / FSReadAllowWithinDeny / FSWriteAllowExtra /
		// FSWriteDenyWithinAllow) — set them through the BashTool
		// struct fields, not per-call input, so the LLM can't
		// loosen the sandbox in one call.
		FSReadDeny:             b.FSReadDeny,
		FSReadAllowWithinDeny:  b.FSReadAllowWithinDeny,
		FSWriteAllowExtra:      b.effectiveWriteAllowExtra(),
		FSWriteDenyWithinAllow: b.FSWriteDenyWithinAllow,
	})
	stdoutPipe, _ := cmd.StdoutPipe()
	stderrPipe, _ := cmd.StderrPipe()

	if err := cmd.Start(); err != nil {
		return softErr("Bash", fmt.Sprintf("start: %v", err)), nil
	}

	var (
		mu     sync.Mutex
		stdout strings.Builder
		stderr strings.Builder
		wg     sync.WaitGroup
	)
	stream := func(prefix string, r io.Reader, sink *strings.Builder) {
		defer wg.Done()
		scanner := bufio.NewScanner(r)
		scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			mu.Lock()
			sink.WriteString(line)
			sink.WriteByte('\n')
			mu.Unlock()
			if env != nil && env.OnProgress != nil {
				env.OnProgress(engine.ProgressData{
					"kind": prefix, "line": line,
				})
			}
		}
	}
	wg.Add(2)
	go stream("stdout", stdoutPipe, &stdout)
	go stream("stderr", stderrPipe, &stderr)

	err := cmd.Wait()
	wg.Wait()

	exitCode := 0
	if err != nil {
		if ee, ok := err.(interface{ ExitCode() int }); ok {
			exitCode = ee.ExitCode()
		} else {
			exitCode = -1
		}
	}
	if cctx.Err() == context.DeadlineExceeded {
		return softErr("Bash", fmt.Sprintf("timeout after %s", timeout)), nil
	}

	mu.Lock()
	soText := stdout.String()
	seText := stderr.String()
	mu.Unlock()

	// Command-aware exit-code interpretation: grep/rg/diff/find/test
	// use exit 1 as a signal, not a failure. Without this, the model
	// sees `grep "foo" *.txt` (no matches) as IsError and chases a
	// non-existent bug. See command_semantics.go.
	isErr, semNote := interpretCommandResult(command, exitCode, soText, seText)

	// Surface the sandbox state in a single header line. When the
	// caller forced ModeOff via dangerously_disable_sandbox we
	// distinguish "off-disabled" from "off" (no helper found) so the
	// model sees that the bypass actually took effect.
	modeLabel := string(mode)
	if disableSandbox && mode == sandbox.ModeOff {
		modeLabel = "off-disabled"
	}
	body := fmt.Sprintf("[sandbox=%s exit=%d]\n", modeLabel, exitCode)
	// Destructive-pattern post-warning: even though the user already
	// approved this run, surface the same warning here so the model
	// reads it in its own context. Lets the model self-audit ("I just
	// did rm -rf — should I confirm with the user before continuing?")
	// without a separate hook channel.
	if dw := bashsec.Warning(command); dw != "" {
		body += "[warning] " + dw + "\n"
	}
	if semNote != "" {
		body += "[note] " + semNote + "\n"
	}
	if soText != "" {
		body += "stdout:\n" + soText
	}
	if seText != "" {
		body += "\nstderr:\n" + seText
	}
	payload := &engine.ToolResultPayload{
		Content: []state.ContentBlock{{Type: state.ContentText, Text: body}},
	}
	if isErr {
		payload.IsError = true
		payload.SoftError = fmt.Sprintf("exit code %d", exitCode)
	}
	return payload, nil
}

func softErr(name, msg string) *engine.ToolResultPayload {
	return &engine.ToolResultPayload{
		Content: []state.ContentBlock{{
			Type: state.ContentText,
			Text: fmt.Sprintf("%s error: %s", name, msg),
		}},
		IsError: true, SoftError: msg,
	}
}

func text(s string) *engine.ToolResultPayload {
	return &engine.ToolResultPayload{
		Content: []state.ContentBlock{{Type: state.ContentText, Text: s}},
	}
}
