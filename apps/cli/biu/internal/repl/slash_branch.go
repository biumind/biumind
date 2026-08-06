// /branch slash — git branch state at a glance.
//
// Renders the current branch, the
// upstream tracking line, the dirty-vs-clean working tree state,
// and the most recent N commits. Pure read — never mutates git
// state. Useful as a sanity check before /commit / /code-review.
//
// Falls back gracefully when git isn't installed or the cwd isn't
// a repo: returns a one-line note instead of a panic stack.

package repl

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

func (m model) handleBranch(parts []string) string {
	if _, err := exec.LookPath("git"); err != nil {
		return "/branch: git not on PATH"
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "/branch: " + err.Error()
	}
	if out, err := gitOut(cwd, "rev-parse", "--is-inside-work-tree"); err != nil ||
		strings.TrimSpace(out) != "true" {
		return "/branch: not inside a git work tree"
	}

	var b strings.Builder

	// Current branch name (or "(detached at <sha>)").
	branch, _ := gitOut(cwd, "rev-parse", "--abbrev-ref", "HEAD")
	branch = strings.TrimSpace(branch)
	if branch == "HEAD" {
		// Detached OR fresh-repo-with-no-commits. Distinguish by
		// whether HEAD actually resolves.
		sha, shaErr := gitOut(cwd, "rev-parse", "--short", "HEAD")
		shaTrim := strings.TrimSpace(sha)
		if shaErr != nil || shaTrim == "" {
			initial, _ := gitOut(cwd, "symbolic-ref", "--short", "HEAD")
			fmt.Fprintf(&b, "branch: %s (no commits yet)\n",
				strings.TrimSpace(initial))
		} else {
			fmt.Fprintf(&b, "branch: (detached at %s)\n", shaTrim)
		}
	} else {
		fmt.Fprintf(&b, "branch: %s\n", branch)
		// Upstream tracking, if any.
		up, err := gitOut(cwd, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}")
		if err == nil {
			up = strings.TrimSpace(up)
			ahead, _ := gitOut(cwd, "rev-list", "--count", "@{upstream}..HEAD")
			behind, _ := gitOut(cwd, "rev-list", "--count", "HEAD..@{upstream}")
			fmt.Fprintf(&b, "upstream: %s (ahead %s, behind %s)\n",
				up, strings.TrimSpace(ahead), strings.TrimSpace(behind))
		} else {
			b.WriteString("upstream: (none — push with `git push -u origin HEAD`)\n")
		}
	}

	// Dirty / clean working tree.
	status, _ := gitOut(cwd, "status", "--porcelain")
	dirty := strings.TrimSpace(status)
	if dirty == "" {
		b.WriteString("working tree: clean\n")
	} else {
		lines := strings.Split(dirty, "\n")
		fmt.Fprintf(&b, "working tree: %d file(s) modified\n", len(lines))
		// Show first 5 changed paths so the user gets a feel for
		// what's outstanding without scrolling a wall.
		shown := 5
		if len(lines) < shown {
			shown = len(lines)
		}
		for i := 0; i < shown; i++ {
			fmt.Fprintf(&b, "  %s\n", lines[i])
		}
		if len(lines) > shown {
			fmt.Fprintf(&b, "  … +%d more\n", len(lines)-shown)
		}
	}

	// Recent commits (5).
	logOut, _ := gitOut(cwd, "log", "--oneline", "-5")
	logOut = strings.TrimRight(logOut, "\n")
	if logOut != "" {
		b.WriteString("\nrecent commits:\n")
		for _, l := range strings.Split(logOut, "\n") {
			fmt.Fprintf(&b, "  %s\n", l)
		}
	}

	return strings.TrimRight(b.String(), "\n")
}

// gitOut runs git with the supplied args under cwd and returns
// stdout. 5s timeout is generous for local-repo metadata commands;
// network-hitting subcommands are NOT used here so the timeout is
// purely defensive against pathological repos.
func gitOut(cwd string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = cwd
	out, err := cmd.Output()
	return string(out), err
}
