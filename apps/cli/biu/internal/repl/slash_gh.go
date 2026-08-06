// /issue and /pr-comments slashes — thin wrappers over the `gh` CLI.
//
// Why shell out instead of hitting the REST API directly? `gh` already
// owns auth + token refresh + enterprise host config. Re-implementing
// that surface in biu is a maintenance tax we don't need to pay for
// what amounts to occasional read-only browsing.
//
// Forms:
//
//	/issue                  — list open issues (last 20)
//	/issue <n>              — show issue #n with comments
//	/issue comment <n> "x"  — post a comment to #n
//	/issue close <n>        — close #n
//
//	/pr-comments            — comments on the PR for the current branch
//	/pr-comments <n>        — comments on PR #n
//
// gh's output is shown verbatim — no LLM round-trip. The slash is a
// shortcut, not an analysis tool.

package repl

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/internal/gitassist"
)

func (m model) handleIssue(parts []string) string {
	if _, err := exec.LookPath("gh"); err != nil {
		return "/issue: GitHub CLI not found — install gh + `gh auth login`"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if len(parts) < 2 {
		return runGH(ctx, "issue", "list", "--limit", "20")
	}
	switch parts[1] {
	case "comment":
		if len(parts) < 4 {
			return "/issue comment: need <n> and a body"
		}
		n := parts[2]
		body := strings.Join(parts[3:], " ")
		body = strings.Trim(body, "\"'")
		return runGHStdin(ctx, body, "issue", "comment", n, "--body-file", "-")
	case "close":
		if len(parts) < 3 {
			return "/issue close: need <n>"
		}
		return runGH(ctx, "issue", "close", parts[2])
	default:
		// Treat parts[1] as an issue number.
		if _, err := strconv.Atoi(parts[1]); err != nil {
			return "/issue: unknown form (try `/issue`, `/issue <n>`, `/issue comment <n> \"x\"`, `/issue close <n>`)"
		}
		return runGH(ctx, "issue", "view", parts[1], "--comments")
	}
}

func (m model) handlePRComments(parts []string) string {
	if _, err := exec.LookPath("gh"); err != nil {
		return "/pr-comments: GitHub CLI not found"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var n string
	if len(parts) >= 2 {
		n = parts[1]
		if _, err := strconv.Atoi(n); err != nil {
			return fmt.Sprintf("/pr-comments: %q is not a PR number", n)
		}
	} else {
		// Resolve from current branch via gh's `--head` filter.
		branch, err := gitassist.CurrentBranch(ctx, gitassist.DefaultRunner)
		if err != nil || branch == "" || branch == "(detached)" {
			return "/pr-comments: not on a branch — pass a PR number"
		}
		out, err := exec.CommandContext(ctx,
			"gh", "pr", "list", "--head", branch, "--state", "open",
			"--json", "number", "--jq", ".[0].number").Output()
		if err != nil {
			return "/pr-comments: failed to find PR for branch " + branch + ": " + err.Error()
		}
		n = strings.TrimSpace(string(out))
		if n == "" {
			return "/pr-comments: no open PR for branch " + branch
		}
	}
	// `gh pr view --comments` includes review comments inline.
	return runGH(ctx, "pr", "view", n, "--comments")
}

// runGH invokes gh with the given args and returns combined output
// trimmed. Errors get the gh stderr folded in so the user can act on
// auth failures, etc.
func runGH(ctx context.Context, args ...string) string {
	cmd := exec.CommandContext(ctx, "gh", args...)
	out, err := cmd.CombinedOutput()
	trimmed := strings.TrimSpace(string(out))
	if err != nil {
		return "/gh " + strings.Join(args, " ") + ": " + err.Error() +
			"\n" + trimmed
	}
	return trimmed
}

// runGHStdin is the variant for write commands that take a body via
// stdin (issue comment, gist create, ...).
func runGHStdin(ctx context.Context, stdin string, args ...string) string {
	cmd := exec.CommandContext(ctx, "gh", args...)
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	trimmed := strings.TrimSpace(string(out))
	if err != nil {
		return "/gh " + strings.Join(args, " ") + ": " + err.Error() +
			"\n" + trimmed
	}
	return trimmed
}
