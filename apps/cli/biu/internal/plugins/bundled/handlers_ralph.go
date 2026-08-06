// ralph-loop handler — Stop hook that re-submits a goal prompt
// until a sentinel file marks the loop complete.
//
// Decision shape:
//
//	loop NOT done  → hooks.Decision{ ReplacePrompt: <ralph.goal contents> }
//	loop done      → hooks.Decision{} (no-op; the Stop completes naturally)
//	files missing  → no-op (we don't loop on a project that didn't opt in)
//
// We use ReplacePrompt rather than spawning a new turn because the
// Stop hook fires AFTER the assistant has finished producing its
// turn; ReplacePrompt is biu's documented mechanism for "re-enter
// the engine with this prompt" and routes through the same turn
// machinery as a normal user message.

package bundled

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/biumind/biumind/apps/cli/biu/internal/hooks"
)

// File names checked. ralph.done's presence (any contents) ends the
// loop; ralph.goal's contents are the prompt to re-submit. Constants
// rather than hooks.json config because the values are part of the
// plugin's contract — users are scripting against the file names.
const (
	ralphGoalFile = "ralph.goal"
	ralphDoneFile = "ralph.done"
)

// ralphLoopStop is the Stop event handler.
//
// We resolve the project root via os.Getwd() because the Stop event
// payload doesn't carry the working directory and biu doesn't pass
// session-cwd into hook payloads today. This means ralph-loop fires
// against the cwd of biu's process, which is the right answer for
// the typical "started biu in the project root" use case.
func ralphLoopStop(ctx context.Context, payload []byte) (hooks.Decision, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return hooks.Decision{}, nil // no cwd → can't loop, harmless no-op
	}
	goalPath := filepath.Join(cwd, ralphGoalFile)
	donePath := filepath.Join(cwd, ralphDoneFile)

	// done file present → loop terminates naturally.
	if _, err := os.Stat(donePath); err == nil {
		return hooks.Decision{}, nil
	}
	// goal file missing → user didn't opt in to ralph-loop in this dir.
	// Silent no-op; we don't want to be a noisy plugin that fires in
	// every project just because it's enabled.
	goal, err := os.ReadFile(goalPath)
	if err != nil {
		return hooks.Decision{}, nil
	}
	prompt := strings.TrimSpace(string(goal))
	if prompt == "" {
		return hooks.Decision{}, nil
	}
	return hooks.Decision{ReplacePrompt: prompt}, nil
}
