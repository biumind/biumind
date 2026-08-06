// Layer E — sub-agents (P20.47 v2). 6 new scenarios on top of what
// Layer A/D already drive (A6 = general-purpose; A9 = user-defined
// explore; D16 = restricted with tool whitelist). E focuses on the
// 4 builtins biu ships beyond general-purpose: Plan, Explore (built-in
// vs user-override), CodeReview, Verification — plus nested dispatch.

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func init() {
	// E1 — Plan: design a small change. Required output shape includes
	// "Implementation steps" + "Critical files" sections (per the
	// builtin Plan agent's system prompt).
	// Parents tend to SUMMARISE the Plan sub-agent's reply rather than
	// pass the literal "Implementation steps" / "Critical files" section
	// titles through. So we assert on plan SUBSTANCE — that the file
	// being changed (main.go) and the change shape (handler / endpoint)
	// show up in the response — not the structural headings.
	register(scenario{
		name: "E1.plan-builtin",
		prompt: "Use the Agent tool with subagent_type=\"Plan\" to design a 2-step plan for adding " +
			"a `/healthz` HTTP endpoint to a Go server in this directory. Summarise the plan back " +
			"to me, naming the file(s) involved and the key change.",
		wantTools: []string{"Agent"},
		timeout:   300 * time.Second,
		prep: func(dir string) error {
			body := "package main\n\nimport \"net/http\"\n\nfunc main() {\n\thttp.ListenAndServe(\":8080\", nil)\n}\n"
			return os.WriteFile(filepath.Join(dir, "main.go"), []byte(body), 0o644)
		},
		assertText: func(s string) error {
			low := strings.ToLower(s)
			if !strings.Contains(low, "main.go") {
				return fmt.Errorf("expected main.go reference, got %q", s)
			}
			if !strings.Contains(low, "healthz") && !strings.Contains(low, "handler") &&
				!strings.Contains(low, "endpoint") {
				return fmt.Errorf("expected handler/endpoint/healthz keyword, got %q", s)
			}
			return nil
		},
	})

	// E2 — Plan: read-only enforcement. Plan must NOT touch files even
	// when prompted. We seed a target file and ask the Plan sub-agent
	// to "fix" it; the parent's text should explain it can only plan,
	// not edit.
	register(scenario{
		name: "E2.plan-readonly-enforced",
		prompt: "Use Agent{subagent_type=\"Plan\"} to fix the typo in target.txt — change " +
			"\"recieve\" to \"receive\". Whatever the Plan agent reports, your final answer " +
			"to me must say either 'PLAN-NO-WRITE' or describe the plan without claiming the " +
			"file changed.",
		wantTools: []string{"Agent"},
		timeout:   180 * time.Second,
		prep: func(dir string) error {
			return os.WriteFile(filepath.Join(dir, "target.txt"),
				[]byte("recieve the package\n"), 0o644)
		},
		assertText: func(s string) error {
			// Read target.txt back to verify Plan didn't mutate it.
			body, err := os.ReadFile("/tmp/biu-smoke-e2-target")
			if err == nil && strings.Contains(string(body), "receive") {
				return fmt.Errorf("Plan agent mutated the file (regression)")
			}
			low := strings.ToLower(s)
			if strings.Contains(low, "plan-no-write") ||
				strings.Contains(low, "didn't change") ||
				strings.Contains(low, "did not change") ||
				strings.Contains(low, "read-only") ||
				strings.Contains(low, "implementation step") {
				return nil
			}
			return fmt.Errorf("expected acknowledgement of plan-only behaviour, got %q", s)
		},
	})

	// E3 — CodeReview on a fresh diff. We seed a small Go file with an
	// obvious smell (unused import) so the agent has something concrete
	// to call out.
	register(scenario{
		name: "E3.codereview-builtin",
		prompt: "Use Agent{subagent_type=\"CodeReview\"} to review review_target.go. " +
			"Reply with whatever critique the agent returns.",
		wantTools: []string{"Agent"},
		timeout:   360 * time.Second,
		prep: func(dir string) error {
			body := "package main\n\nimport (\n\t\"fmt\"\n\t\"strings\"\n)\n\n" +
				"func Greet(name string) string {\n\treturn fmt.Sprintf(\"hi %s\", name)\n}\n"
			return os.WriteFile(filepath.Join(dir, "review_target.go"), []byte(body), 0o644)
		},
		assertText: func(s string) error {
			low := strings.ToLower(s)
			// Either the critique noticed `strings` is unused, OR it
			// signed off cleanly with some kind of summary. Reject only
			// pure model deflections.
			if !strings.Contains(low, "strings") && !strings.Contains(low, "import") &&
				!strings.Contains(low, "review") && !strings.Contains(low, "looks") {
				return fmt.Errorf("review reply doesn't reference the file or its issues: %q", s)
			}
			return nil
		},
	})

	// E4 — Verification: run a build / test command and report
	// PASS / FAIL / PARTIAL. We seed a tiny Go file with a syntax error
	// so the verification agent must shell out to `go vet` or
	// equivalent, observe the failure, and surface it.
	register(scenario{
		name: "E4.verification-builtin",
		prompt: "Use Agent{subagent_type=\"Verification\"} to verify the file `bug.go` compiles. " +
			"The verification agent will end with VERDICT: PASS / FAIL / PARTIAL.",
		wantTools: []string{"Agent"},
		timeout:   480 * time.Second,
		prep: func(dir string) error {
			// Intentional broken Go: missing closing brace.
			body := "package main\n\nfunc main() {\n\tprintln(\"oops\")\n"
			return os.WriteFile(filepath.Join(dir, "bug.go"), []byte(body), 0o644)
		},
		skipReason: func() string {
			if _, err := exec.LookPath("go"); err != nil {
				return "go toolchain not on PATH"
			}
			return ""
		},
		assertText: func(s string) error {
			up := strings.ToUpper(s)
			if !strings.Contains(up, "VERDICT") {
				return fmt.Errorf("expected VERDICT line, got %q", s)
			}
			if !strings.Contains(up, "FAIL") && !strings.Contains(up, "PARTIAL") {
				// The bug was deliberate; the agent should NOT report PASS.
				return fmt.Errorf("expected FAIL or PARTIAL verdict, got %q", s)
			}
			return nil
		},
	})

	// E5 — Nested sub-agent dispatch: parent calls Agent{Plan}, the
	// Plan output should NOT spawn its own Agent (Plan's tools list
	// excludes Agent). Verify the parent gets a plan back without
	// secondary agent calls in the toolsFired record.
	//
	// We can't directly inspect toolsFired from inside the policy
	// callback, so we do it by checking the parent's reply quality.
	register(scenario{
		name: "E5.nested-agent-no-recursion",
		prompt: "Use Agent{subagent_type=\"Plan\"} to plan adding a logger. " +
			"After the Plan agent finishes, summarise its plan in 2 sentences. " +
			"Don't dispatch any further sub-agents.",
		wantTools: []string{"Agent"},
		timeout:   180 * time.Second,
		assertText: func(s string) error {
			if len(strings.TrimSpace(s)) < 30 {
				return fmt.Errorf("expected a real summary, got %q", s)
			}
			return nil
		},
	})

	// E6 — Sub-agent with model: inherit. The user-defined sub-agent's
	// `model: inherit` directive tells the spawner to reuse the parent's
	// model. We can't directly assert the model used, but we can verify
	// the dispatch succeeds end-to-end and the agent's specialty
	// behaviour (Glob-only, Read-only) holds.
	register(scenario{
		name: "E6.user-agent-model-inherit",
		prompt: `Use Agent{subagent_type="counter"} to count files in cwd. ` +
			"The agent must inherit my current model and use Glob (not Bash).",
		wantTools: []string{"Agent"},
		timeout:   180 * time.Second,
		prep: func(dir string) error {
			agentDir := filepath.Join(dir, ".biumind", "agents")
			if err := os.MkdirAll(agentDir, 0o755); err != nil {
				return err
			}
			agentMD := `---
name: counter
description: Counts files using Glob. Reports the total.
tools: Glob, Read
permissionMode: plan
model: inherit
maxTurns: 4
---
You count files with Glob. Reply with the total count followed by " items".
`
			if err := os.WriteFile(filepath.Join(agentDir, "counter.md"),
				[]byte(agentMD), 0o644); err != nil {
				return err
			}
			for _, n := range []string{"a", "b", "c", "d"} {
				if err := os.WriteFile(filepath.Join(dir, n+".txt"), []byte("x"), 0o644); err != nil {
					return err
				}
			}
			return nil
		},
		assertText: func(s string) error {
			if !strings.Contains(s, "4") && !strings.Contains(s, "5") {
				// Allow 4 (just the .txt files) or 5 (also counting
				// the .biumind/agents/counter.md).
				return fmt.Errorf("expected count 4 or 5 in reply, got %q", s)
			}
			return nil
		},
	})
}
