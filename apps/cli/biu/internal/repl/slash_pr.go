// /pr slash — push the current branch and open a pull request, with
// title + body drafted by the LLM from the branch's diff.
//
// Forms:
//
//	/pr               — push + draft + open PR via `gh`
//	/pr --dry-run     — draft + print title/body, do nothing
//	/pr --no-push     — assume the branch is already on origin
//	/pr --draft       — open as a GitHub draft PR (`gh pr create --draft`)
//	/pr --base <br>   — override the target branch (default: origin/HEAD)
//
// /pr depends on the `gh` CLI being authed; if it's missing we exit
// early with a hint rather than guess at REST calls.

package repl

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/internal/gitassist"
)

const prDiffCap = 64 * 1024

func (m model) handlePR(parts []string) string {
	flags := parsePRFlags(parts)

	if _, err := exec.LookPath("gh"); err != nil && !flags.dryRun {
		return "/pr: GitHub CLI not found — install gh and `gh auth login`, or run with --dry-run"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	branch, err := gitassist.CurrentBranch(ctx, gitassist.DefaultRunner)
	if err != nil {
		return "/pr: " + err.Error()
	}
	if branch == "" || branch == "(detached)" {
		return "/pr: not on a branch (detached HEAD?)"
	}

	base := flags.base
	if base == "" {
		b, err := gitassist.MainBranch(ctx, gitassist.DefaultRunner)
		if err != nil {
			return "/pr: " + err.Error()
		}
		base = b
	}
	if base == "" {
		return "/pr: cannot determine base branch — pass `--base <name>`"
	}
	if base == branch {
		return fmt.Sprintf("/pr: current branch %q is the base — switch branches first", branch)
	}

	diff, err := gitassist.BranchDiff(ctx, gitassist.DefaultRunner, base, prDiffCap)
	if err != nil {
		return "/pr: " + err.Error()
	}
	if strings.TrimSpace(diff) == "" {
		return fmt.Sprintf("/pr: no commits on %q that aren't on %q yet", branch, base)
	}
	log, _ := gitassist.BranchLog(ctx, gitassist.DefaultRunner, base)

	title, body := flags.title, flags.body
	if title == "" {
		if m.provider == nil || m.modelID == "" {
			return "/pr: provider/model not wired — pass --title and --body to skip the LLM"
		}
		gen := commitGeneratorFor(m.provider, m.modelID)
		raw, err := gen(ctx, gitassist.PRPrompt(branch, base, log, diff))
		if err != nil {
			return "/pr: LLM error: " + err.Error()
		}
		title, body = gitassist.SplitTitleBody(raw)
		if title == "" {
			return "/pr: LLM returned no title — got:\n" + raw
		}
	}

	if flags.dryRun {
		return "/pr: --dry-run — would open PR\n\nTitle: " + title + "\n\n" + body
	}

	if !flags.noPush {
		if !gitassist.HasRemote(ctx, gitassist.DefaultRunner) {
			return "/pr: no `origin` remote configured — add one or pass --no-push"
		}
		if _, err := gitassist.DefaultRunner(ctx, "push", "-u", "origin", branch); err != nil {
			return "/pr: push failed: " + err.Error()
		}
	}

	url, err := ghCreatePR(ctx, base, title, body, flags.draft)
	if err != nil {
		return "/pr: " + err.Error() + "\n\ntitle was:\n" + title + "\n\nbody was:\n" + body
	}
	return "/pr: opened\n" + url
}

// ghCreatePR shells out to `gh pr create`. The body comes via stdin
// to avoid shell quoting hell. Returns the URL printed by gh.
func ghCreatePR(ctx context.Context, base, title, body string, draft bool) (string, error) {
	args := []string{"pr", "create", "--base", base, "--title", title, "--body-file", "-"}
	if draft {
		args = append(args, "--draft")
	}
	cmd := exec.CommandContext(ctx, "gh", args...)
	cmd.Stdin = strings.NewReader(body)
	out, err := cmd.CombinedOutput()
	trimmed := strings.TrimSpace(string(out))
	if err != nil {
		return "", errors.New("gh pr create: " + err.Error() + " (" + trimmed + ")")
	}
	// gh prints the URL on stdout; sometimes there's a leading note.
	for _, line := range strings.Split(trimmed, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "https://") {
			return line, nil
		}
	}
	return trimmed, nil
}

type prFlags struct {
	dryRun bool
	noPush bool
	draft  bool
	base   string
	title  string
	body   string
}

func parsePRFlags(parts []string) prFlags {
	var out prFlags
	for i := 1; i < len(parts); i++ {
		switch parts[i] {
		case "--dry-run":
			out.dryRun = true
		case "--no-push":
			out.noPush = true
		case "--draft":
			out.draft = true
		case "--base":
			if i+1 < len(parts) {
				out.base = parts[i+1]
				i++
			}
		case "--title":
			if i+1 < len(parts) {
				out.title = parts[i+1]
				i++
			}
		case "--body":
			if i+1 < len(parts) {
				// rest of the line is the body — joined back into one string.
				out.body = strings.Join(parts[i+1:], " ")
				return out
			}
		}
	}
	return out
}
