//go:build integration

// Spike test: prove the PTY harness actually drives biu's bubbletea
// REPL end-to-end before we commit to 26 cases.

package repl_test

import (
	"testing"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/test/integration/harness"
)

// TestSpike_HelpRendersUnderPTY is the minimum viable signal that
// harness.StartREPL actually works: launch biu, type "/help", press
// Enter, and verify the help output mentions a known command name.
//
// If this fails, the rest of Layer C is moot — fix the harness
// first.
func TestSpike_HelpRendersUnderPTY(t *testing.T) {
	sb := harness.NewSandbox(t)
	sb.SeedDirectConfig(t, harness.AnthropicEnv{
		APIKey:  "sk-placeholder-no-real-call",
		BaseURL: "http://127.0.0.1:1",
		Model:   "claude-opus-4-7",
	})

	r := harness.StartREPL(t, sb)
	defer r.Close()

	r.SendLine("/help")
	r.Expect("/quit", 5*time.Second)
}
