// Layer D — tools (P20.47 v2). Focus on the per-tool happy + sad
// paths that Layer A's task-driven prompts don't naturally exercise.
//
// The model has 27 tools available. A already covers Read/Edit/Glob/
// Grep/Bash/WebFetch/Write/MultiEdit/NotebookEdit/Agent/TodoWrite/
// TaskCreate/TaskUpdate. Layer D fills in the gaps + sad paths.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/pkg/biumindkit"
)

// echoSentinel is a tiny custom tool used by D17 to verify the
// Options.ExtraTools extension point round-trips: the model invokes
// EchoSentinel{msg:"…"} and the tool just echoes the message back
// so the assistant's final text can carry it verbatim.
func echoSentinelTool() biumindkit.Tool {
	return biumindkit.NewTool(biumindkit.ToolDef{
		Name:        "EchoSentinel",
		Description: "Echoes the supplied msg verbatim. Useful for proving the SDK's ExtraTools field works end-to-end.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"msg": map[string]any{"type": "string"},
			},
			"required": []string{"msg"},
		},
		IsReadOnly:        true,
		IsConcurrencySafe: true,
		Run: func(_ context.Context, args map[string]any) (string, error) {
			msg, _ := args["msg"].(string)
			return msg, nil
		},
	})
}

func init() {
	// ── File tools — sad paths ─────────────────────────────────────
	register(scenario{
		name:    "D1.read-missing-file",
		prompt:  "Read the file `does-not-exist.txt`. If you can't, reply with literally NOFILE-OK.",
		timeout: 30 * time.Second,
		assertText: func(s string) error {
			if !strings.Contains(strings.ToUpper(s), "NOFILE") {
				return fmt.Errorf("expected NOFILE acknowledgement, got %q", s)
			}
			return nil
		},
	})

	register(scenario{
		name: "D2.edit-ambiguous-match",
		prompt: "In dup.txt, use Edit to change the word 'foo' to 'bar' (without replace_all). " +
			"If the change can't be made due to ambiguity, reply literally with AMBIGUOUS-OK and stop.",
		timeout: 60 * time.Second,
		prep: func(dir string) error {
			return os.WriteFile(filepath.Join(dir, "dup.txt"),
				[]byte("foo line one\nfoo line two\nfoo again\n"), 0o644)
		},
		assertText: func(s string) error {
			up := strings.ToUpper(s)
			if !strings.Contains(up, "AMBIGUOUS") &&
				!strings.Contains(up, "REPLACE_ALL") {
				return fmt.Errorf("expected ambiguity acknowledgement, got %q", s)
			}
			return nil
		},
	})

	register(scenario{
		name: "D3.glob-no-match",
		prompt: "Use Glob to find any files matching `*.nonexistent` in the current directory. " +
			"Tell me how many you found.",
		wantTools: []string{"Glob"},
		timeout:   30 * time.Second,
		assertText: func(s string) error {
			if !strings.Contains(s, "0") &&
				!strings.Contains(strings.ToLower(s), "no") &&
				!strings.Contains(strings.ToLower(s), "none") {
				return fmt.Errorf("expected zero / none result, got %q", s)
			}
			return nil
		},
	})

	register(scenario{
		name: "D4.grep-bad-regex",
		prompt: "Use Grep with the pattern `[unclosed` to search the current directory. " +
			"If the regex is invalid, reply with literally BADREGEX-OK and stop.",
		timeout: 30 * time.Second,
		prep: func(dir string) error {
			return os.WriteFile(filepath.Join(dir, "x.txt"), []byte("hi\n"), 0o644)
		},
		assertText: func(s string) error {
			up := strings.ToUpper(s)
			if !strings.Contains(up, "BADREGEX") && !strings.Contains(up, "INVALID") {
				return fmt.Errorf("expected BADREGEX-style ack, got %q", s)
			}
			return nil
		},
	})

	register(scenario{
		name: "D5.notebook-edit-out-of-range",
		prompt: "In demo.ipynb, use NotebookEdit to replace cell 99's source. " +
			"If the cell index is out of range, reply with OOR-OK and stop.",
		timeout: 60 * time.Second,
		prep: func(dir string) error {
			nb := map[string]any{
				"cells": []map[string]any{{
					"cell_type": "code", "execution_count": nil,
					"metadata": map[string]any{}, "outputs": []any{},
					"source": []string{"print(1)"},
				}},
				"metadata": map[string]any{}, "nbformat": 4, "nbformat_minor": 5,
			}
			data, _ := json.MarshalIndent(nb, "", "  ")
			return os.WriteFile(filepath.Join(dir, "demo.ipynb"), data, 0o644)
		},
		assertText: func(s string) error {
			up := strings.ToUpper(s)
			if !strings.Contains(up, "OOR") &&
				!strings.Contains(up, "OUT OF") &&
				!strings.Contains(up, "RANGE") {
				return fmt.Errorf("expected out-of-range ack, got %q", s)
			}
			return nil
		},
	})

	// ── Bash variants ──────────────────────────────────────────────
	register(scenario{
		name: "D6.bash-redirect-dev-null",
		prompt: "Use Bash to count `.go` files in the current directory using a pipeline that " +
			"redirects stderr to /dev/null. Reply with just the number.",
		wantTools: []string{"Bash"},
		timeout:   45 * time.Second,
		prep: func(dir string) error {
			for _, n := range []string{"a.go", "b.go", "c.go"} {
				if err := os.WriteFile(filepath.Join(dir, n), []byte("package x\n"), 0o644); err != nil {
					return err
				}
			}
			return nil
		},
		assertText: func(s string) error {
			if !strings.Contains(s, "3") {
				return fmt.Errorf("expected count 3, got %q", s)
			}
			return nil
		},
	})

	register(scenario{
		name: "D7.bash-multi-step-pipeline",
		prompt: "Use a SINGLE Bash invocation with a pipeline of `find`, `wc`, and `awk` " +
			"to count lines across all .txt files in the current directory. Reply with just the number.",
		wantTools: []string{"Bash"},
		timeout:   60 * time.Second,
		prep: func(dir string) error {
			contents := []string{"a\nb\n", "x\ny\nz\n", "single\n"}
			for i, body := range contents {
				if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%d.txt", i)),
					[]byte(body), 0o644); err != nil {
					return err
				}
			}
			return nil
		},
		// 6 lines total across 3 files. Allow 5–7 because line counters
		// behave slightly differently with trailing newlines.
		assertText: func(s string) error {
			for _, ok := range []string{"5", "6", "7"} {
				if strings.Contains(s, ok) {
					return nil
				}
			}
			return fmt.Errorf("expected line count near 6, got %q", s)
		},
	})

	// ── Worktree (file-based, requires git init) ─────────────────────
	register(scenario{
		name: "D8.worktree-enter-skipped-without-git",
		prompt: "Try to use EnterWorktree to start a new branch. If the directory is " +
			"not a git repository, reply with literally NOTGIT-OK and stop.",
		timeout: 60 * time.Second,
		assertText: func(s string) error {
			up := strings.ToUpper(s)
			if !strings.Contains(up, "NOTGIT") && !strings.Contains(up, "NOT") {
				return fmt.Errorf("expected NOTGIT ack, got %q", s)
			}
			return nil
		},
	})

	// ── Plan mode ─────────────────────────────────────────────────
	register(scenario{
		name: "D9.enter-plan-mode-then-exit",
		prompt: "Use EnterPlanMode. Inside plan mode, draft a 2-step plan for adding a " +
			"hello-world endpoint to a Go HTTP server. Then call ExitPlanMode to commit " +
			"the plan. Confirm the plan was committed.",
		wantTools: []string{"EnterPlanMode", "ExitPlanMode"},
		timeout:   90 * time.Second,
	})

	// ── Skill ─────────────────────────────────────────────────────
	register(scenario{
		name: "D10.skill-not-found",
		prompt: "Use the Skill tool to call a skill named `__definitely-no-skill__`. " +
			"If the skill is not found, reply with literally NOSKILL-OK and stop.",
		timeout: 30 * time.Second,
		assertText: func(s string) error {
			up := strings.ToUpper(s)
			if !strings.Contains(up, "NOSKILL") && !strings.Contains(up, "NOT FOUND") &&
				!strings.Contains(up, "DOES NOT EXIST") {
				return fmt.Errorf("expected NOSKILL ack, got %q", s)
			}
			return nil
		},
	})

	// ── Cron tool family ──────────────────────────────────────────
	register(scenario{
		name: "D11.cron-create-list-delete",
		prompt: "Use CronCreate to schedule a one-shot reminder at midnight tomorrow with prompt " +
			"\"check the deploy\" — set recurring=false. After creating it, list all crons with " +
			"CronList; the new entry must appear. Then delete it via CronDelete and confirm it's gone.",
		wantTools: []string{"CronCreate", "CronList", "CronDelete"},
		timeout:   60 * time.Second,
		assertText: func(s string) error {
			if !strings.Contains(strings.ToLower(s), "delet") &&
				!strings.Contains(strings.ToLower(s), "remov") &&
				!strings.Contains(strings.ToLower(s), "gone") {
				return fmt.Errorf("expected deletion confirmation, got %q", s)
			}
			return nil
		},
	})

	// ── PushNotification ─────────────────────────────────────────
	register(scenario{
		name: "D12.push-notification",
		prompt: "Use PushNotification (status=proactive) to send a one-line notice " +
			"\"build finished\". Confirm what you did.",
		wantTools: []string{"PushNotification"},
		timeout:   30 * time.Second,
	})

	// ── TaskList depth ───────────────────────────────────────────
	register(scenario{
		name:      "D13.task-list-empty",
		prompt:    "Call TaskList. If there are no tasks, reply with literally EMPTY-OK and stop.",
		wantTools: []string{"TaskList"},
		timeout:   30 * time.Second,
		assertText: func(s string) error {
			up := strings.ToUpper(s)
			if !strings.Contains(up, "EMPTY") && !strings.Contains(up, "NO TASK") {
				return fmt.Errorf("expected EMPTY ack, got %q", s)
			}
			return nil
		},
	})

	register(scenario{
		name: "D14.task-create-then-get",
		prompt: "Use TaskCreate to create a task with subject 'review PR'. " +
			"Then use TaskGet on the returned id to fetch its details. " +
			"Reply with the task's status field value.",
		wantTools: []string{"TaskCreate", "TaskGet"},
		timeout:   60 * time.Second,
		assertText: func(s string) error {
			if !strings.Contains(strings.ToLower(s), "pending") {
				return fmt.Errorf("expected pending status, got %q", s)
			}
			return nil
		},
	})

	register(scenario{
		name: "D15.task-block-dependency",
		prompt: "Use TaskCreate twice to make two tasks: 'A' and 'B'. " +
			"Then use TaskUpdate on B with addBlockedBy=[<id_of_A>]. " +
			"Confirm B is blocked by A by name.",
		wantTools: []string{"TaskCreate", "TaskUpdate"},
		timeout:   90 * time.Second,
		assertText: func(s string) error {
			if !strings.Contains(strings.ToUpper(s), "BLOCK") {
				return fmt.Errorf("expected blocked-by mention, got %q", s)
			}
			return nil
		},
	})

	// ── Agent + tool whitelist ───────────────────────────────────
	register(scenario{
		name:      "D16.agent-with-tool-whitelist",
		prompt:    `Use the Agent tool with subagent_type="restricted" to count files in cwd. The sub-agent must use Glob (not Bash). Tell me what it found.`,
		wantTools: []string{"Agent"},
		timeout:   120 * time.Second,
		prep: func(dir string) error {
			agentDir := filepath.Join(dir, ".biumind", "agents")
			if err := os.MkdirAll(agentDir, 0o755); err != nil {
				return err
			}
			agentMD := `---
name: restricted
description: Counts files using Glob ONLY. Never shells out.
tools: Glob, Read
permissionMode: plan
maxTurns: 4
---
You are a restricted sub-agent. Use Glob (NOT Bash) to find files,
then report the count. Never run shell commands.
`
			if err := os.WriteFile(filepath.Join(agentDir, "restricted.md"),
				[]byte(agentMD), 0o644); err != nil {
				return err
			}
			for _, n := range []string{"x.txt", "y.txt", "z.txt"} {
				if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644); err != nil {
					return err
				}
			}
			return nil
		},
		assertText: func(s string) error {
			if !strings.Contains(s, "3") {
				return fmt.Errorf("expected count 3, got %q", s)
			}
			return nil
		},
	})

	// ── ExtraTools custom — proves the SDK extension API works ─────
	register(scenario{
		name:       "D17.extra-tools-custom-echo",
		prompt:     "Use the EchoSentinel tool with msg=\"ZX9K-CUSTOM\". Repeat back the output you got.",
		wantTools:  []string{"EchoSentinel"},
		timeout:    45 * time.Second,
		extraTools: []biumindkit.Tool{echoSentinelTool()},
		assertText: func(s string) error {
			if !strings.Contains(s, "ZX9K-CUSTOM") {
				return fmt.Errorf("expected sentinel echo in reply, got %q", s)
			}
			return nil
		},
	})

	// ── Bash background path ─────────────────────────────────────
	register(scenario{
		name: "D18.bash-background-and-tail",
		prompt: "Use Bash with run_in_background=true to start `sleep 1; echo done`. " +
			"Then use BashOutput on the returned task_id to fetch the captured output. " +
			"Reply with whatever the captured stdout contains.",
		wantTools: []string{"Bash", "BashOutput"},
		timeout:   90 * time.Second,
		assertText: func(s string) error {
			if !strings.Contains(s, "done") {
				return fmt.Errorf("expected 'done' from background task output, got %q", s)
			}
			return nil
		},
	})

	// ── Bash dangerously_disable_sandbox ─────────────────────────
	register(scenario{
		name: "D19.bash-disable-sandbox",
		prompt: "Use Bash with dangerously_disable_sandbox=true to run `echo OK > /tmp/biu-d19-marker`. " +
			"After it runs, use Bash again to print the file's contents. Reply with whatever was inside.",
		wantTools: []string{"Bash"},
		timeout:   60 * time.Second,
		assertText: func(s string) error {
			if !strings.Contains(strings.ToUpper(s), "OK") {
				return fmt.Errorf("expected OK content, got %q", s)
			}
			return nil
		},
	})

	// ── WebSearch (likely degrades gracefully without Searxng) ────
	register(scenario{
		name: "D20.websearch-soft-fail",
		prompt: "Use WebSearch to query \"biumind cli\". If the search backend isn't configured, " +
			"reply literally NOSEARCH-OK and stop.",
		timeout: 45 * time.Second,
		// No wantTools assertion — WebSearch may simply not be wired
		// in the SDK's default registration.
		assertText: func(s string) error {
			up := strings.ToUpper(s)
			// Pass either if the search returned content OR the model
			// recognised the absence and bailed cleanly.
			if strings.Contains(up, "NOSEARCH") ||
				strings.Contains(up, "NOT CONFIGURED") ||
				strings.Contains(up, "BIUMIND") {
				return nil
			}
			return fmt.Errorf("expected NOSEARCH ack or genuine results, got %q", s)
		},
	})
}
