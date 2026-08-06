// /commit slash — generate a Conventional Commits message via LLM
// and (optionally) execute the commit.
//
// Forms:
//
//	/commit              — auto-stage all + draft message + commit
//	/commit --dry-run    — draft + print, do not commit
//	/commit --no-stage   — skip `git add -A` (use whatever is already staged)
//	/commit -m "<msg>"   — commit with the given message, no LLM call
//
// /commit is intentionally simple — there's no preview/confirm
// modal. Output shows the resulting commit so the user can
// `git reset HEAD~1` if they want a redo. That keeps the slash
// non-modal and replayable from /history.

package repl

import (
	"context"
	"strings"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/internal/client"
	"github.com/biumind/biumind/apps/cli/biu/internal/gitassist"
	"github.com/biumind/biumind/packages/go-sdk/biu/llm"
)

// commitDiffCap caps how much diff we send to the LLM. Most useful
// commits are < 8 KiB; massive diffs lead to expensive + noisy
// summaries. The cap is permissive enough to cover real refactors
// without being a budget grenade.
const commitDiffCap = 16 * 1024

func (m model) handleCommit(parts []string) string {
	flags := parseCommitFlags(parts)

	if flags.message != "" {
		return runCommit(flags.message)
	}

	if m.provider == nil || m.modelID == "" {
		return "/commit: provider/model not wired — try `/commit -m \"…\"` for a manual message"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if !flags.noStage {
		if err := gitassist.StageAll(ctx, gitassist.DefaultRunner); err != nil {
			return "/commit: " + err.Error()
		}
	}

	st, err := gitassist.GetStatus(ctx, gitassist.DefaultRunner)
	if err != nil {
		return "/commit: " + err.Error()
	}
	if len(st.Staged) == 0 {
		return "/commit: nothing staged (try removing --no-stage, or stage manually first)"
	}
	if len(st.Conflicts) > 0 {
		return "/commit: unresolved conflicts: " + strings.Join(st.Conflicts, ", ")
	}

	diff, err := gitassist.Diff(ctx, gitassist.DefaultRunner, true, commitDiffCap)
	if err != nil {
		return "/commit: " + err.Error()
	}
	log, _ := gitassist.RecentLog(ctx, gitassist.DefaultRunner, 8)

	gen := commitGeneratorFor(m.provider, m.modelID)
	msg, err := gitassist.GenerateCommitMessage(ctx, gen, diff, log)
	if err != nil {
		return "/commit: " + err.Error()
	}

	if flags.dryRun {
		return "/commit: --dry-run, would commit with:\n\n" + msg
	}
	out, err := gitassist.Commit(ctx, gitassist.DefaultRunner, msg)
	if err != nil {
		return "/commit: " + err.Error() + "\n\nproposed message was:\n" + msg
	}
	return "/commit: ok\n\n" + msg + "\n\n" + strings.TrimSpace(out)
}

// runCommit handles the `-m "<msg>"` shortcut — bypass the LLM
// entirely and just call git commit.
func runCommit(msg string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := gitassist.StageAll(ctx, gitassist.DefaultRunner); err != nil {
		return "/commit: " + err.Error()
	}
	out, err := gitassist.Commit(ctx, gitassist.DefaultRunner, msg)
	if err != nil {
		return "/commit: " + err.Error()
	}
	return "/commit: ok\n" + strings.TrimSpace(out)
}

// commitFlags is what /commit's tiny arg parser produces. We don't
// pull in a flag library here — the surface is too small.
type commitFlags struct {
	dryRun  bool
	noStage bool
	message string // when set, skip LLM entirely
}

// parseCommitFlags consumes the slash args. Recognised forms:
//
//	--dry-run
//	--no-stage
//	-m "msg" / -m msg
//
// Anything else is silently ignored — the user gets the default
// behaviour rather than a parse error blocking their commit.
func parseCommitFlags(parts []string) commitFlags {
	var out commitFlags
	for i := 1; i < len(parts); i++ {
		switch parts[i] {
		case "--dry-run":
			out.dryRun = true
		case "--no-stage":
			out.noStage = true
		case "-m", "--message":
			if i+1 < len(parts) {
				rest := strings.Join(parts[i+1:], " ")
				rest = strings.Trim(rest, "\"'")
				out.message = rest
				return out
			}
		}
	}
	return out
}

// commitGeneratorFor adapts a client.Provider into a
// gitassist.Generator. The slash uses the same model as the REPL
// session — no separate config knob.
func commitGeneratorFor(p client.Provider, model string) gitassist.Generator {
	return func(ctx context.Context, prompt string) (string, error) {
		frames, err := p.ChatStream(ctx, client.ChatRequest{
			Model: model,
			Messages: []client.Message{{
				Role: "user", Content: prompt,
			}},
			MaxTokens: 2048,
		})
		if err != nil {
			return "", err
		}
		return llm.CollectText(frames)
	}
}
