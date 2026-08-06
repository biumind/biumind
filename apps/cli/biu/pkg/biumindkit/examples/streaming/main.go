// Streaming biumindkit consumer that prints every event type as it
// arrives. Useful as a starting point for IDE plugins / progress UIs
// that need tool-call visibility.
//
// Run:
//
//   ANTHROPIC_API_KEY=sk-ant-… go run ./pkg/biumindkit/examples/streaming \
//       "what does main.go do?"

package main

import (
	"context"
	"encoding/json"
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
		fmt.Fprintln(os.Stderr, "usage: streaming <prompt…>")
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

	for ev := range ag.Submit(ctx, prompt) {
		switch e := ev.(type) {
		case biumindkit.AssistantText:
			fmt.Println(e.Text)
		case biumindkit.ToolStart:
			input, _ := json.Marshal(e.Input)
			fmt.Fprintf(os.Stderr, "⏺ %s %s\n", e.Name, input)
		case biumindkit.ToolResult:
			marker := "✓"
			if e.IsError {
				marker = "✗"
			}
			fmt.Fprintf(os.Stderr, "  %s %s [%s]\n", marker, e.Name, e.Elapsed)
		case biumindkit.CompactStarted:
			fmt.Fprintf(os.Stderr, "↻ compact (reason=%s, tokens_before=%d)\n",
				e.Reason, e.TokensBefore)
		case biumindkit.CompactFinished:
			fmt.Fprintf(os.Stderr, "✓ compact saved %d tokens\n", e.TokensSaved)
		case biumindkit.Error:
			fmt.Fprintf(os.Stderr, "ERR %v (recoverable=%v)\n", e.Err, e.Recoverable)
			if !e.Recoverable {
				os.Exit(1)
			}
		case biumindkit.Done:
			fmt.Fprintf(os.Stderr,
				"[stop=%s, %d in / %d out / %d cache_read / %d cache_write tokens, %s]\n",
				e.StopReason, e.InputTokens, e.OutputTokens,
				e.CacheReadTokens, e.CacheWriteTokens, e.Elapsed)
		}
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
