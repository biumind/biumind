// Examples that render on pkg.go.dev under the package overview.
// Each `Example*` runs as a normal test, so the snippets are compiled
// (and exercised at the level a real consumer would use them) — no
// stale documentation drift.
//
// Run: `go test -run Example ./pkg/biumindkit/...`

package biumindkit_test

import (
	"context"
	"fmt"

	"github.com/biumind/biumind/apps/cli/biu/pkg/biumindkit"
)

// One-shot prompt → final assistant text. Convenience pattern for CI
// jobs and small scripts that don't care about streaming events.
func ExampleAgent_Run() {
	// In real code this comes from os.Getenv("ANTHROPIC_API_KEY").
	apiKey := "sk-ant-…"

	ag, err := biumindkit.New(biumindkit.Options{
		APIKey: apiKey,
		Model:  "claude-sonnet-4-6",
		Cwd:    ".",
	})
	if err != nil {
		fmt.Println(err)
		return
	}
	defer ag.Close()

	text, stop, err := ag.Run(
		context.Background(),
		"Find every TODO comment in this repo, grouped by file.")
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Printf("stop_reason=%s\n%s\n", stop, text)
}

// Streaming pattern: drain the event channel, switch on type. Use
// this when you need tool-call visibility (progress UIs, audit logs)
// or want to react to compact / cost events as they happen.
func ExampleAgent_Submit() {
	ag, _ := biumindkit.New(biumindkit.Options{
		APIKey: "sk-ant-…",
	})
	defer ag.Close()

	for ev := range ag.Submit(context.Background(), "what does main.go do?") {
		switch e := ev.(type) {
		case biumindkit.AssistantText:
			fmt.Println(e.Text)
		case biumindkit.ToolStart:
			fmt.Printf("⏺ %s\n", e.Name)
		case biumindkit.ToolResult:
			if e.IsError {
				fmt.Printf("✗ %s — %s\n", e.Name, e.Output)
			}
		case biumindkit.Done:
			fmt.Printf("[%d in / %d out tokens, %s]\n",
				e.InputTokens, e.OutputTokens, e.Elapsed)
		case biumindkit.Error:
			fmt.Printf("ERR %v (recoverable=%v)\n", e.Err, e.Recoverable)
		}
	}
}

// Register a custom tool the agent can call. ExtraTools land last,
// so they override built-ins on name conflict — handy for replacing
// `Bash` with a sandboxed variant in CI.
func ExampleNewTool() {
	echo := biumindkit.NewTool(biumindkit.ToolDef{
		Name:        "Echo",
		Description: "Echoes the supplied message back to the model.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"msg": map[string]any{"type": "string"},
			},
			"required":             []string{"msg"},
			"additionalProperties": false,
		},
		IsReadOnly:        true,
		IsConcurrencySafe: true,
		Run: func(_ context.Context, args map[string]any) (string, error) {
			msg, _ := args["msg"].(string)
			return "echo: " + msg, nil
		},
	})

	ag, err := biumindkit.New(biumindkit.Options{
		APIKey:     "sk-ant-…",
		ExtraTools: []biumindkit.Tool{echo},
	})
	if err != nil {
		return
	}
	defer ag.Close()
	_, _, _ = ag.Run(context.Background(), "Use the Echo tool to say hello.")
}

// Override the permission policy. Default in SDK contexts is `Deny`
// (so the turn fails loud rather than hanging on a non-existent TTY).
// Use PermissionAllow for fully autonomous sandboxes, or supply a
// custom function for queue/stdin-driven workflows.
func ExamplePermissionAllow() {
	ag, _ := biumindkit.New(biumindkit.Options{
		APIKey:           "sk-ant-…",
		PermissionPolicy: biumindkit.PermissionAllow(),
		// Or pin a permission mode directly:
		// PermissionMode: "acceptEdits",
	})
	defer ag.Close()
}

// Custom policy: approve reads, ask the user (over a custom channel)
// for everything else. Real implementations would look up a mailbox
// or write to a UI bus — kept inline here for clarity.
func ExamplePermissionPolicyFn_custom() {
	policy := func(ctx context.Context, req biumindkit.PermissionRequest) biumindkit.PermissionDecision {
		switch req.ToolName {
		case "Read", "Glob", "Grep":
			return biumindkit.PermAllow
		default:
			// In real code: post to a mailbox, wait for an answer.
			return biumindkit.PermDeny
		}
	}

	ag, _ := biumindkit.New(biumindkit.Options{
		APIKey:           "sk-ant-…",
		PermissionPolicy: policy,
	})
	defer ag.Close()
}
