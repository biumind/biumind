// Register a custom tool the agent can call. Demonstrates the full
// builder shape — InputSchema, concurrency flags, error returns.
//
// Run:
//
//   ANTHROPIC_API_KEY=sk-ant-… go run ./pkg/biumindkit/examples/customtool

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/pkg/biumindkit"
)

func main() {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "ANTHROPIC_API_KEY is required")
		os.Exit(2)
	}

	// A tiny "WordCount" tool — counts words in the supplied text.
	// Read-only + concurrency-safe so the runner can batch it next to
	// other safe tools in the same turn.
	wc := biumindkit.NewTool(biumindkit.ToolDef{
		Name:        "WordCount",
		Description: "Count the number of whitespace-separated words in `text`.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"text": map[string]any{"type": "string"},
			},
			"required":             []string{"text"},
			"additionalProperties": false,
		},
		IsReadOnly:        true,
		IsConcurrencySafe: true,
		Run: func(_ context.Context, args map[string]any) (string, error) {
			text, ok := args["text"].(string)
			if !ok {
				return "", errors.New("WordCount: `text` must be a string")
			}
			n := len(strings.Fields(text))
			return fmt.Sprintf("%d", n), nil
		},
	})

	ag, err := biumindkit.New(biumindkit.Options{
		APIKey:     apiKey,
		Model:      envOr("ANTHROPIC_MODEL", "claude-sonnet-4-6"),
		Cwd:        ".",
		ExtraTools: []biumindkit.Tool{wc},
		// Allow tools the rules don't pre-approve so the demo doesn't
		// get blocked on the first call. Don't do this in production.
		PermissionPolicy: biumindkit.PermissionAllow(),
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer ag.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	text, _, err := ag.Run(ctx,
		"Use the WordCount tool to count words in: \"the quick brown fox jumps over the lazy dog\".")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(text)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
