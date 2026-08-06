// Layer A — SDK smoke (P20.47 v2). 20 scenarios that prove the
// engine + provider + tool catalog + permission flow work end-to-end
// against a real Anthropic-compatible endpoint.

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/pkg/biumindkit"
)

func init() {
	// A1-A7: pre-existing happy paths, kept verbatim from the
	// original smoketest 9-case sweep.
	register(scenario{
		name:    "A1.minimal-chat",
		prompt:  "Reply with literally the single word PONG. No punctuation.",
		timeout: 30 * time.Second,
		assertText: func(s string) error {
			if !strings.Contains(strings.ToUpper(s), "PONG") {
				return fmt.Errorf("expected PONG in reply, got %q", s)
			}
			return nil
		},
	})

	register(scenario{
		name:      "A2.read-tool",
		prompt:    "Read the file BIUMIND.md in the current directory and tell me what its first line says.",
		wantTools: []string{"Read"},
		timeout:   60 * time.Second,
		prep: func(dir string) error {
			return os.WriteFile(filepath.Join(dir, "BIUMIND.md"),
				[]byte("THIS IS THE EXPECTED FIRST LINE\nplus more body\n"), 0o644)
		},
		assertText: func(s string) error {
			if !strings.Contains(s, "EXPECTED FIRST LINE") {
				return fmt.Errorf("reply missing first-line phrase")
			}
			return nil
		},
	})

	register(scenario{
		name:      "A3.read-then-edit",
		prompt:    "Read demo.txt, then change the word 'placeholder' to 'replaced'. Confirm what you did.",
		wantTools: []string{"Edit", "MultiEdit"},
		timeout:   90 * time.Second,
		prep: func(dir string) error {
			return os.WriteFile(filepath.Join(dir, "demo.txt"),
				[]byte("this is a placeholder line\n"), 0o644)
		},
	})

	register(scenario{
		name:      "A4.glob-grep",
		prompt:    "Find every file ending in .txt under the current directory and tell me how many you found.",
		wantTools: []string{"Glob", "Bash", "Grep"},
		timeout:   60 * time.Second,
		prep: func(dir string) error {
			for _, n := range []string{"a.txt", "b.txt", "skip.go"} {
				if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644); err != nil {
					return err
				}
			}
			return nil
		},
		assertText: func(s string) error {
			if !strings.Contains(s, "2") {
				return fmt.Errorf("expected count of 2 in reply")
			}
			return nil
		},
	})

	register(scenario{
		name: "A5.multi-turn-reasoning",
		prompt: "List 3 ways to reduce LLM token costs, ranked by impact. One sentence each. " +
			"Format the reply as a numbered list with each item on its OWN LINE — " +
			"start each line with `1.`, `2.`, `3.` literally.",
		timeout: 60 * time.Second,
		assertText: func(s string) error {
			if !strings.Contains(s, "1.") || !strings.Contains(s, "2.") {
				return fmt.Errorf("expected at least items 1. and 2. in reply")
			}
			return nil
		},
	})

	register(scenario{
		name:      "A6.sub-agent-general",
		prompt:    "Use the Agent tool with subagent_type='general-purpose' to find every line containing 'TARGET' in files under the current directory. Reply with the count.",
		wantTools: []string{"Agent"},
		timeout:   120 * time.Second,
		prep: func(dir string) error {
			bodies := []string{
				"line one\nTARGET here\nline three\n",
				"no match\n",
				"TARGET on first\nrest plain\nanother TARGET\n",
			}
			for i, body := range bodies {
				p := filepath.Join(dir, fmt.Sprintf("f%d.txt", i))
				if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
					return err
				}
			}
			return nil
		},
	})

	register(scenario{
		name:      "A7.multi-edit",
		prompt:    "Read mod.go, then use MultiEdit (or Edit twice) to rename both `OldName` and `OldHelper` to `NewName` and `NewHelper`. Confirm what changed.",
		wantTools: []string{"Edit", "MultiEdit"},
		timeout:   120 * time.Second,
		prep: func(dir string) error {
			body := "package main\n\nfunc OldName() {}\nfunc OldHelper() {}\nfunc keepThis() {}\n"
			return os.WriteFile(filepath.Join(dir, "mod.go"), []byte(body), 0o644)
		},
	})

	// A8: deny path (real PermissionDeny, not bypass).
	register(scenario{
		name: "A8.permission-deny",
		prompt: "Try to delete the file /tmp/biu-smoke-deny-target with `rm`. " +
			"If permission is denied, reply with literally 'DENIED-OK' and stop.",
		wantTools: []string{"Bash"},
		timeout:   60 * time.Second,
		policy:    biumindkit.PermissionDeny(),
		assertText: func(s string) error {
			if !strings.Contains(strings.ToUpper(s), "DENIED") {
				return fmt.Errorf("expected DENIED-style acknowledgement, got %q", s)
			}
			return nil
		},
	})

	// A9: typed sub-agent (user-defined .biumind/agents/explore.md).
	register(scenario{
		name:      "A9.typed-sub-agent-explore",
		prompt:    `Use the Agent tool with subagent_type="explore" to find every file containing the marker FINDME under the current directory. Reply with what the sub-agent reports.`,
		wantTools: []string{"Agent"},
		timeout:   120 * time.Second,
		prep: func(dir string) error {
			agentDir := filepath.Join(dir, ".biumind", "agents")
			if err := os.MkdirAll(agentDir, 0o755); err != nil {
				return err
			}
			agentMD := `---
name: explore
description: Read-only repo exploration. Returns a numbered list.
tools: Read, Glob, Grep
permissionMode: plan
maxTurns: 5
---
You are the Explore sub-agent. NEVER write — only Read/Glob/Grep.
Reply with a numbered list of findings, no preamble.
`
			if err := os.WriteFile(filepath.Join(agentDir, "explore.md"),
				[]byte(agentMD), 0o644); err != nil {
				return err
			}
			for _, n := range []string{"a.txt", "b.txt"} {
				if err := os.WriteFile(filepath.Join(dir, n),
					[]byte("FINDME\n"), 0o644); err != nil {
					return err
				}
			}
			_ = os.WriteFile(filepath.Join(dir, "skip.txt"),
				[]byte("not interesting\n"), 0o644)
			return nil
		},
	})

	// A10-A20: P20.47a additions.

	register(scenario{
		name: "A10.write-new-file",
		prompt: "Create a new file hello.go in the current directory. " +
			"It must contain a `package main` declaration and a `main()` " +
			"function that prints PONG. Confirm what you wrote.",
		wantTools: []string{"Write"},
		timeout:   90 * time.Second,
		assertText: func(s string) error {
			if !strings.Contains(s, "hello.go") {
				return fmt.Errorf("reply doesn't reference hello.go")
			}
			return nil
		},
	})

	register(scenario{
		name: "A11.glob-read-summarise",
		prompt: "Find the largest .go file in the current directory by line count, " +
			"read its full content, then list its top-level function names. " +
			"Reply with one function name per line.",
		wantTools: []string{"Glob", "Read"},
		timeout:   120 * time.Second,
		prep: func(dir string) error {
			small := "package small\n\nfunc TinyA() {}\n"
			big := "package big\n\n"
			for i := 0; i < 6; i++ {
				big += fmt.Sprintf("func Big%d() { /* line padding */ }\n", i)
			}
			if err := os.WriteFile(filepath.Join(dir, "small.go"), []byte(small), 0o644); err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(dir, "big.go"), []byte(big), 0o644)
		},
		assertText: func(s string) error {
			if !strings.Contains(s, "Big") {
				return fmt.Errorf("expected Big* function names in reply")
			}
			return nil
		},
	})

	register(scenario{
		name: "A12.bash-pipe",
		prompt: "Use a single shell pipeline (with `|`) to count how many .txt files " +
			"are in the current directory. Reply with just the number.",
		wantTools: []string{"Bash"},
		timeout:   60 * time.Second,
		prep: func(dir string) error {
			for _, n := range []string{"x.txt", "y.txt", "z.txt", "skip.go"} {
				if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644); err != nil {
					return err
				}
			}
			return nil
		},
		assertText: func(s string) error {
			if !strings.Contains(s, "3") {
				return fmt.Errorf("expected count 3 in reply, got %q", s)
			}
			return nil
		},
	})

	register(scenario{
		name: "A13.bash-fail-recovery",
		prompt: "Run `lss -la` (intentionally wrong). When it fails with command-not-found, " +
			"recover by running `ls -la` instead, then report how many entries it lists. " +
			"Reply with the entry count only.",
		wantTools: []string{"Bash"},
		timeout:   90 * time.Second,
		prep: func(dir string) error {
			for _, n := range []string{"a", "b", "c"} {
				if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644); err != nil {
					return err
				}
			}
			return nil
		},
	})

	register(scenario{
		name: "A14.webfetch",
		prompt: "Use WebFetch to retrieve https://example.com — tell me what the H1 heading text is. " +
			"Reply with just the heading text.",
		wantTools: []string{"WebFetch"},
		timeout:   90 * time.Second,
		assertText: func(s string) error {
			if !strings.Contains(strings.ToLower(s), "example") {
				return fmt.Errorf("expected 'example' in heading, got %q", s)
			}
			return nil
		},
	})

	register(scenario{
		name: "A15.todowrite",
		prompt: "Use the TodoWrite tool to record exactly 4 tasks for migrating " +
			"authentication from middleware to a service layer. " +
			"After writing, confirm the count.",
		wantTools: []string{"TodoWrite"},
		timeout:   60 * time.Second,
	})

	register(scenario{
		name: "A16.task-create-update",
		prompt: "Use TaskCreate to create a task with subject 'verify build'. " +
			"Then use TaskUpdate to set its status to in_progress, " +
			"then update again to completed. Confirm the final status.",
		wantTools: []string{"TaskCreate", "TaskUpdate"},
		timeout:   90 * time.Second,
		assertText: func(s string) error {
			if !strings.Contains(strings.ToLower(s), "complet") {
				return fmt.Errorf("reply doesn't confirm completion")
			}
			return nil
		},
	})

	register(scenario{
		name: "A17.notebook-edit",
		prompt: "In demo.ipynb, replace the contents of cell 0 with a single line: print(42). " +
			"Use NotebookEdit. Confirm what you did.",
		wantTools: []string{"NotebookEdit"},
		timeout:   90 * time.Second,
		prep: func(dir string) error {
			nb := map[string]any{
				"cells": []map[string]any{
					{
						"cell_type":       "code",
						"execution_count": nil,
						"metadata":        map[string]any{},
						"outputs":         []any{},
						"source":          []string{"print('original')"},
					},
				},
				"metadata":       map[string]any{},
				"nbformat":       4,
				"nbformat_minor": 5,
			}
			data, err := json.MarshalIndent(nb, "", "  ")
			if err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(dir, "demo.ipynb"), data, 0o644)
		},
	})

	register(scenario{
		name:    "A18.system-prompt-injection",
		prompt:  "Hi — say hello.",
		system:  "You are a friendly assistant. End every reply with the literal token ZX9K-SENTINEL on its own line.",
		timeout: 30 * time.Second,
		assertText: func(s string) error {
			if !strings.Contains(s, "ZX9K-SENTINEL") {
				return fmt.Errorf("system-injected sentinel missing in reply")
			}
			return nil
		},
	})

	register(scenario{
		name:          "A19.model-override",
		prompt:        "Reply with literally the single word OK.",
		modelOverride: os.Getenv("ANTHROPIC_MODEL_ALT"),
		timeout:       30 * time.Second,
		skipReason: func() string {
			if os.Getenv("ANTHROPIC_MODEL_ALT") == "" {
				return "ANTHROPIC_MODEL_ALT not set"
			}
			return ""
		},
		assertText: func(s string) error {
			if !strings.Contains(strings.ToUpper(s), "OK") {
				return fmt.Errorf("expected OK in reply, got %q", s)
			}
			return nil
		},
	})

	register(scenario{
		name:    "A20.output-style-concise",
		prompt:  "What is 2 plus 2?",
		system:  "Output style: ULTRA-CONCISE. Reply with the digit answer only — no words, no punctuation, no explanation.",
		timeout: 30 * time.Second,
		assertText: func(s string) error {
			t := strings.TrimSpace(s)
			if !strings.Contains(t, "4") {
				return fmt.Errorf("expected '4' in reply, got %q", s)
			}
			if len(t) > 8 {
				return fmt.Errorf("reply not concise (len=%d): %q", len(t), s)
			}
			return nil
		},
	})
}
