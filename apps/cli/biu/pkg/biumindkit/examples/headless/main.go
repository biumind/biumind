// One-shot headless usage of biumindkit.
//
// Run:
//
//   ANTHROPIC_API_KEY=sk-ant-… go run ./pkg/biumindkit/examples/headless \
//       "list every TODO in this repo, grouped by file"
//
// What you get on stdout: the final assistant text. What gets logged
// on stderr: the model's chosen stop reason. Exit code mirrors the
// agent's outcome (0 = clean, 1 = failure).

package main

import (
	"context"
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

	prompt := strings.Join(os.Args[1:], " ")
	if prompt == "" {
		fmt.Fprintln(os.Stderr, "usage: headless <prompt…>")
		os.Exit(2)
	}

	ag, err := biumindkit.New(biumindkit.Options{
		APIKey: apiKey,
		Model:  envOr("ANTHROPIC_MODEL", "claude-sonnet-4-6"),
		Cwd:    ".",
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer ag.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	text, stop, err := ag.Run(ctx, prompt)
	if err != nil {
		fmt.Fprintf(os.Stderr, "agent error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(text)
	fmt.Fprintf(os.Stderr, "stop_reason=%s\n", stop)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
