// Custom permission policy: approve reads, deny writes, log every
// decision to a channel for downstream auditing.
//
// Pattern: route the policy callback to a queue/UI/RPC of your choice
// and gate the tool execution on the answer. Inline implementation
// here for clarity.
//
// Run:
//
//   ANTHROPIC_API_KEY=sk-ant-… go run ./pkg/biumindkit/examples/policy

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/pkg/biumindkit"
)

func main() {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "ANTHROPIC_API_KEY is required")
		os.Exit(2)
	}

	// Audit channel — every permission decision is logged here. In a
	// real system this might be Kafka, a Slack notifier, or a row
	// inserted into your audit table.
	audit := make(chan auditRow, 64)
	go func() {
		for row := range audit {
			b, _ := json.Marshal(row)
			fmt.Fprintln(os.Stderr, "AUDIT", string(b))
		}
	}()

	policy := func(_ context.Context, req biumindkit.PermissionRequest) biumindkit.PermissionDecision {
		decision := biumindkit.PermDeny
		switch req.ToolName {
		case "Read", "Glob", "Grep", "Bash":
			decision = biumindkit.PermAllow
		}
		audit <- auditRow{
			Tool:     req.ToolName,
			ID:       req.ToolUseID,
			Reason:   req.Reason,
			Decision: decisionLabel(decision),
		}
		return decision
	}

	ag, err := biumindkit.New(biumindkit.Options{
		APIKey:           apiKey,
		Model:            envOr("ANTHROPIC_MODEL", "claude-sonnet-4-6"),
		Cwd:              ".",
		PermissionPolicy: policy,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer ag.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	text, _, err := ag.Run(ctx,
		"Find all *.go files under internal/ and tell me which one is largest.")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(text)

	close(audit)
	time.Sleep(50 * time.Millisecond) // let the auditor drain
}

type auditRow struct {
	Tool     string `json:"tool"`
	ID       string `json:"id"`
	Reason   string `json:"reason,omitempty"`
	Decision string `json:"decision"`
}

func decisionLabel(d biumindkit.PermissionDecision) string {
	switch d {
	case biumindkit.PermAllow:
		return "allow"
	case biumindkit.PermAlways:
		return "always"
	default:
		return "deny"
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
