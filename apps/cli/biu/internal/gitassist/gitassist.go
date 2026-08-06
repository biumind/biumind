// Package gitassist drives biu's git-helper slashes: /commit and
// /pr. The package owns the small amount of git-shape logic so the
// REPL slashes stay readable.
//
// Design notes:
//
//   - LLM access is injected as a Generator func, not a concrete
//     summariser. That keeps gitassist independent of biu's
//     provider stack and trivially testable with a fake.
//   - Every git invocation goes through `runner` which is also a
//     test seam — tests inject a fake that records cmds + returns
//     canned output instead of touching a real repo.
//   - We never run destructive ops (push --force, reset --hard) and
//     we never use --no-verify. If a hook fails, the commit fails
//     loudly; that's the right behaviour.
package gitassist

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// Generator is the LLM-call seam. Returns the model's response text
// for the given prompt; cancelled context is silent (returns "" + ctx.Err()).
type Generator func(ctx context.Context, prompt string) (string, error)

// Runner is the git-call seam. Default impl runs `git` via exec.
type Runner func(ctx context.Context, args ...string) (string, error)

// DefaultRunner runs git commands in the current working directory.
// Stdout returned on success; stderr folded into the error on failure.
func DefaultRunner(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git %s: %w (output: %s)",
			strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// Status is the parsed output of `git status --porcelain=v1`.
type Status struct {
	Modified  []string // tracked files with unstaged changes
	Staged    []string // files in the index
	Untracked []string // ?? entries
	Conflicts []string // unmerged
}

// Empty reports whether there's anything to commit.
func (s Status) Empty() bool {
	return len(s.Modified) == 0 && len(s.Staged) == 0 &&
		len(s.Untracked) == 0 && len(s.Conflicts) == 0
}

// GetStatus runs `git status --porcelain` and parses it. Failures
// surface verbatim — no silent recovery.
func GetStatus(ctx context.Context, run Runner) (Status, error) {
	if run == nil {
		run = DefaultRunner
	}
	out, err := run(ctx, "status", "--porcelain=v1")
	if err != nil {
		return Status{}, err
	}
	return ParseStatus(out), nil
}

// ParseStatus turns `git status --porcelain=v1` text into a Status.
// Exposed so callers can pre-fetch raw output and parse separately
// (e.g. when batching).
func ParseStatus(raw string) Status {
	var st Status
	for _, line := range strings.Split(raw, "\n") {
		if len(line) < 3 {
			continue
		}
		x, y, path := line[0], line[1], strings.TrimSpace(line[3:])
		// Strip rename arrow "old -> new" → keep the new path.
		if i := strings.Index(path, " -> "); i >= 0 {
			path = path[i+4:]
		}
		switch {
		case x == 'U' || y == 'U' || (x == 'A' && y == 'A') || (x == 'D' && y == 'D'):
			st.Conflicts = append(st.Conflicts, path)
		case x == '?' && y == '?':
			st.Untracked = append(st.Untracked, path)
		default:
			if x != ' ' && x != '?' {
				st.Staged = append(st.Staged, path)
			}
			if y != ' ' && y != '?' {
				st.Modified = append(st.Modified, path)
			}
		}
	}
	return st
}

// Diff returns `git diff` output. When staged is true, returns
// `git diff --staged`. Output is truncated to maxBytes if positive
// to keep prompt size bounded — LLMs handle massive diffs poorly
// and cost adds up.
func Diff(ctx context.Context, run Runner, staged bool, maxBytes int) (string, error) {
	if run == nil {
		run = DefaultRunner
	}
	args := []string{"diff", "--no-color"}
	if staged {
		args = append(args, "--staged")
	}
	out, err := run(ctx, args...)
	if err != nil {
		return "", err
	}
	if maxBytes > 0 && len(out) > maxBytes {
		out = out[:maxBytes] + "\n\n…(truncated)"
	}
	return out, nil
}

// RecentLog returns the last n commits' subject lines so the
// generator can match the project's commit-message style.
func RecentLog(ctx context.Context, run Runner, n int) (string, error) {
	if run == nil {
		run = DefaultRunner
	}
	if n <= 0 {
		n = 10
	}
	return run(ctx, "log", fmt.Sprintf("-%d", n), "--pretty=format:%s")
}

// CurrentBranch returns the current branch name, or "(detached)" /
// "" when no commits / detached HEAD.
func CurrentBranch(ctx context.Context, run Runner) (string, error) {
	if run == nil {
		run = DefaultRunner
	}
	out, err := run(ctx, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", err
	}
	out = strings.TrimSpace(out)
	if out == "HEAD" {
		return "(detached)", nil
	}
	return out, nil
}

// CommitMessagePrompt builds the prompt sent to the Generator for
// /commit. Templated separately from GenerateCommitMessage so the
// prompt is easy to inspect in tests.
func CommitMessagePrompt(diff, recentLog string) string {
	var b strings.Builder
	b.WriteString("Write a Conventional Commits-style git commit message for the changes below.\n\n")
	b.WriteString("Constraints:\n")
	b.WriteString("- First line is `type(scope): subject`, ≤72 characters.\n")
	b.WriteString("- Type is one of: feat, fix, refactor, docs, test, chore, perf, build, ci.\n")
	b.WriteString("- Body (optional) explains *why*, not *what*. Wrap at 72 cols.\n")
	b.WriteString("- Output ONLY the commit message text. No code fences, no preamble.\n")
	if recentLog != "" {
		b.WriteString("\nRecent commit subjects from this repo (match the prevailing style):\n")
		b.WriteString(recentLog)
		b.WriteString("\n")
	}
	b.WriteString("\nDiff:\n")
	b.WriteString(diff)
	return b.String()
}

// GenerateCommitMessage builds the prompt + calls the generator and
// post-processes (strips code fences, trims whitespace) so the
// result drops straight into `git commit -m`.
func GenerateCommitMessage(ctx context.Context, gen Generator, diff, recentLog string) (string, error) {
	if gen == nil {
		return "", errors.New("no generator wired — set provider/model first")
	}
	if strings.TrimSpace(diff) == "" {
		return "", errors.New("nothing to commit (empty diff)")
	}
	raw, err := gen(ctx, CommitMessagePrompt(diff, recentLog))
	if err != nil {
		return "", err
	}
	return CleanLLMText(raw), nil
}

// CleanLLMText strips code fences and surrounding whitespace from an
// LLM response. Common pattern: the model wraps its answer in
// ```text … ``` even though we asked it not to.
func CleanLLMText(s string) string {
	s = strings.TrimSpace(s)
	// Strip leading ```anylang and trailing ``` if both present.
	if strings.HasPrefix(s, "```") {
		if i := strings.Index(s, "\n"); i > 0 {
			s = s[i+1:]
		}
	}
	s = strings.TrimSuffix(s, "```")
	s = strings.TrimSpace(s)
	return s
}

// StageAll runs `git add -A`. Returns the number of files staged
// (best-effort: counts via a fresh status call).
func StageAll(ctx context.Context, run Runner) error {
	if run == nil {
		run = DefaultRunner
	}
	_, err := run(ctx, "add", "-A")
	return err
}

// MainBranch returns the name of the upstream main branch — tries
// origin/HEAD's symref first, falling back to "main" / "master" /
// the current branch's @{upstream}. Empty string + nil when none
// can be determined (caller should refuse to compute a PR diff).
func MainBranch(ctx context.Context, run Runner) (string, error) {
	if run == nil {
		run = DefaultRunner
	}
	out, err := run(ctx, "symbolic-ref", "refs/remotes/origin/HEAD")
	if err == nil {
		out = strings.TrimSpace(out)
		// Format: "refs/remotes/origin/main"
		if idx := strings.LastIndex(out, "/"); idx >= 0 {
			return out[idx+1:], nil
		}
	}
	// Fallbacks — try the conventional names without erroring.
	for _, name := range []string{"main", "master"} {
		if _, err := run(ctx, "rev-parse", "--verify", name); err == nil {
			return name, nil
		}
	}
	return "", nil
}

// HasRemote reports whether `origin` exists. /pr needs this to
// decide between git push + gh, vs telling the user to add a remote.
func HasRemote(ctx context.Context, run Runner) bool {
	if run == nil {
		run = DefaultRunner
	}
	_, err := run(ctx, "remote", "get-url", "origin")
	return err == nil
}

// BranchDiff runs `git diff <base>...HEAD` for PR-shaped diffs (the
// three-dot form gives only what's in HEAD that isn't in base).
func BranchDiff(ctx context.Context, run Runner, base string, maxBytes int) (string, error) {
	if run == nil {
		run = DefaultRunner
	}
	if base == "" {
		return "", errors.New("no base branch")
	}
	out, err := run(ctx, "diff", "--no-color", base+"...HEAD")
	if err != nil {
		return "", err
	}
	if maxBytes > 0 && len(out) > maxBytes {
		out = out[:maxBytes] + "\n\n…(truncated)"
	}
	return out, nil
}

// BranchLog returns the commit subjects on HEAD that aren't on base.
// Used for both PR-body bullet lists and prompt context.
func BranchLog(ctx context.Context, run Runner, base string) (string, error) {
	if run == nil {
		run = DefaultRunner
	}
	if base == "" {
		return "", errors.New("no base branch")
	}
	return run(ctx, "log", base+"..HEAD", "--pretty=format:%s")
}

// PRPrompt builds the prompt for /pr title + body generation. The
// LLM is asked to return a short title on line 1, blank line, then
// markdown body — so callers can split on the first blank line.
func PRPrompt(branch, base, log, diff string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Write a GitHub pull-request title + body for merging branch %q into %q.\n\n", branch, base)
	b.WriteString("Format:\n")
	b.WriteString("- Line 1: PR title (≤72 chars, imperative mood, no trailing period).\n")
	b.WriteString("- Line 2: blank.\n")
	b.WriteString("- Line 3+: markdown body with two sections:\n")
	b.WriteString("    ## Summary\n    - 1–3 bullets describing what changed and why.\n")
	b.WriteString("    ## Test plan\n    - bullet checklist of how to verify.\n")
	b.WriteString("- Output ONLY the title + body. No code fences, no preamble.\n")
	if log != "" {
		b.WriteString("\nCommits on this branch:\n")
		b.WriteString(log)
		b.WriteString("\n")
	}
	b.WriteString("\nDiff:\n")
	b.WriteString(diff)
	return b.String()
}

// SplitTitleBody parses the LLM PR draft into (title, body).
// Convention: first non-empty line is the title; the rest after a
// blank line is the body. We tolerate a stray blank line or an
// accidentally-included `Title: ` prefix.
func SplitTitleBody(s string) (title, body string) {
	s = CleanLLMText(s)
	lines := strings.SplitN(s, "\n", 2)
	if len(lines) == 0 {
		return "", ""
	}
	title = strings.TrimSpace(lines[0])
	title = strings.TrimPrefix(strings.TrimPrefix(title, "Title:"), "title:")
	title = strings.TrimSpace(title)
	if len(lines) == 2 {
		body = strings.TrimSpace(lines[1])
	}
	return
}

// LatestTag returns the most recent annotated/lightweight tag
// reachable from HEAD, or "" when there are none.
func LatestTag(ctx context.Context, run Runner) (string, error) {
	if run == nil {
		run = DefaultRunner
	}
	out, err := run(ctx, "describe", "--tags", "--abbrev=0")
	if err != nil {
		// `git describe` exits non-zero when no tags exist; we
		// treat that as "" rather than an error.
		return "", nil
	}
	return strings.TrimSpace(out), nil
}

// TagList returns all tag names sorted by creation date desc.
func TagList(ctx context.Context, run Runner) (string, error) {
	if run == nil {
		run = DefaultRunner
	}
	return run(ctx, "tag", "--sort=-creatordate")
}

// CommitsSinceTag returns subjects of every commit since `tag` (or
// from the start of history when tag is empty), newest first.
func CommitsSinceTag(ctx context.Context, run Runner, tag string) (string, error) {
	if run == nil {
		run = DefaultRunner
	}
	rev := "HEAD"
	if tag != "" {
		rev = tag + "..HEAD"
	}
	return run(ctx, "log", rev, "--pretty=format:%s")
}

// TagPrompt builds the prompt for /tag changelog generation.
func TagPrompt(prevTag, log string) string {
	var b strings.Builder
	if prevTag != "" {
		fmt.Fprintf(&b, "Write a release changelog for the commits since tag %q.\n\n", prevTag)
	} else {
		b.WriteString("Write a release changelog for the commits below.\n\n")
	}
	b.WriteString("Format:\n")
	b.WriteString("- Group by Conventional Commits type: ## Features, ## Fixes, ## Other.\n")
	b.WriteString("- One bullet per commit, ≤80 chars, present tense.\n")
	b.WriteString("- Skip noisy commits (typo fixes, formatting, merge commits).\n")
	b.WriteString("- Output ONLY the changelog markdown.\n")
	b.WriteString("\nCommits (newest first):\n")
	b.WriteString(log)
	return b.String()
}

// CreateTag runs `git tag -a <name> -F -` with the body via stdin
// so commit-message-style multi-line works without shell quoting.
// When body is empty, creates a lightweight tag instead.
func CreateTag(ctx context.Context, name, body string) (string, error) {
	if strings.TrimSpace(name) == "" {
		return "", errors.New("empty tag name")
	}
	if strings.TrimSpace(body) == "" {
		cmd := exec.CommandContext(ctx, "git", "tag", name)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return string(out), fmt.Errorf("git tag failed: %w (%s)", err, strings.TrimSpace(string(out)))
		}
		return string(out), nil
	}
	cmd := exec.CommandContext(ctx, "git", "tag", "-a", name, "-F", "-")
	cmd.Stdin = strings.NewReader(body)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git tag -a failed: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// Commit runs `git commit -m <message>`. The message is passed via
// stdin (-F -) to dodge shell quoting issues when bodies contain
// quotes / dollars / backticks.
func Commit(ctx context.Context, run Runner, message string) (string, error) {
	if run == nil {
		run = DefaultRunner
	}
	if strings.TrimSpace(message) == "" {
		return "", errors.New("empty commit message")
	}
	cmd := exec.CommandContext(ctx, "git", "commit", "-F", "-")
	cmd.Stdin = strings.NewReader(message)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git commit failed: %w (output: %s)",
			err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
