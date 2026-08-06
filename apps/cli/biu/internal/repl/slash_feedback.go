// /feedback slash — open a pre-filled GitHub issue with diagnostic
// context attached.
//
// Why a slash instead of just "open the URL yourself": the form
// gets people to actually file the bug, because the boring part —
// pasting biu version, OS, last error, last few prompts — is done
// for them. Without that, half the issues come back asking "what
// version are you on?".
//
// Forms:
//
//	/feedback                  — open browser via gh issue create
//	/feedback "summary"        — preset title
//	/feedback --print          — print the prefilled body to terminal
//	                             (useful when no gh / over SSH)
//
// Privacy: the body NEVER includes env-var values, file contents,
// or full prompts. We include the *count* of recent messages, the
// last error string (if any), the active model, and the OS+arch.
// Anything more sensitive is the user's call to add.

package repl

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// feedbackRepo is the GitHub repo issues are filed against. Pinned
// here rather than in config so we don't carry a knob nobody will
// ever turn — if biu forks, this gets edited.
const feedbackRepo = "biumind/biumind"

func (m model) handleFeedback(parts []string) string {
	flags := parseFeedbackFlags(parts)

	body := buildFeedbackBody(m)
	title := flags.title
	if title == "" {
		title = "biu feedback"
	}

	if flags.print {
		return "/feedback: title = " + title + "\n\n" + body
	}

	if _, err := exec.LookPath("gh"); err != nil {
		return "/feedback: gh CLI not found — copy this body into\n" +
			"https://github.com/" + feedbackRepo + "/issues/new\n\n" + body
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", "issue", "create",
		"--repo", feedbackRepo,
		"--title", title,
		"--body-file", "-")
	cmd.Stdin = strings.NewReader(body)
	out, err := cmd.CombinedOutput()
	trimmed := strings.TrimSpace(string(out))
	if err != nil {
		return "/feedback: gh failed: " + err.Error() + "\n" + trimmed +
			"\n\nbody was:\n" + body
	}
	return "/feedback: filed\n" + trimmed
}

// buildFeedbackBody assembles the diagnostic context. Stays
// deliberately small — we'd rather under-share than leak.
func buildFeedbackBody(m model) string {
	info := installInfoForREPL()
	exe, _ := os.Executable()
	method, _ := detectInstallMethod(exe)

	var b strings.Builder
	b.WriteString("## Environment\n\n")
	fmt.Fprintf(&b, "- biu version: %s (%s)\n", info.Version, info.Commit)
	fmt.Fprintf(&b, "- install method: %s\n", orUnknown(method))
	fmt.Fprintf(&b, "- os/arch: %s/%s\n", runtime.GOOS, runtime.GOARCH)
	fmt.Fprintf(&b, "- go runtime: %s\n", runtime.Version())
	if m.modelID != "" {
		fmt.Fprintf(&b, "- model: %s\n", m.modelID)
	}

	b.WriteString("\n## Session\n\n")
	user, asst := countMessages(m.history)
	fmt.Fprintf(&b, "- messages: %d total (%d user, %d assistant)\n",
		len(m.history), user, asst)
	if m.lastErr != nil {
		fmt.Fprintf(&b, "- last error: `%s`\n", m.lastErr.Error())
	}
	if m.sessionLog != nil {
		fmt.Fprintf(&b, "- session id: %s\n", m.sessionLog.ID())
	}

	b.WriteString("\n## What happened\n\n_describe the issue here_\n")
	b.WriteString("\n## What you expected\n\n_describe what you expected_\n")
	b.WriteString("\n## Reproduction\n\n_steps to reproduce, if known_\n")
	return b.String()
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

type feedbackFlags struct {
	print bool
	title string
}

func parseFeedbackFlags(parts []string) feedbackFlags {
	var out feedbackFlags
	if len(parts) < 2 {
		return out
	}
	for i := 1; i < len(parts); i++ {
		switch parts[i] {
		case "--print":
			out.print = true
		default:
			// First non-flag arg becomes the title.
			if out.title == "" {
				rest := strings.Join(parts[i:], " ")
				rest = strings.Trim(rest, "\"'")
				// stop on next flag
				if idx := strings.Index(rest, " --"); idx > 0 {
					rest = rest[:idx]
				}
				out.title = rest
			}
		}
	}
	return out
}
