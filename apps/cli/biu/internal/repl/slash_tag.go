// /tag slash — list / inspect / create git tags. Optional LLM
// drafts a Conventional-Commits-style changelog body.
//
// Forms:
//
//	/tag                        — list tags (newest first)
//	/tag <name>                 — create lightweight tag at HEAD
//	/tag <name> -m "msg"        — create annotated tag with message
//	/tag <name> --auto          — annotate with LLM-drafted changelog
//	/tag <name> --auto --from <prev>
//	                            — same, but base changelog on commits
//	                              after <prev> (defaults to latest tag)
//
// Annotated tags use `git tag -a` so the message survives push.

package repl

import (
	"context"
	"strings"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/internal/gitassist"
)

func (m model) handleTag(parts []string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if len(parts) < 2 {
		out, err := gitassist.TagList(ctx, gitassist.DefaultRunner)
		if err != nil {
			return "/tag: " + err.Error()
		}
		out = strings.TrimSpace(out)
		if out == "" {
			return "/tag: no tags yet"
		}
		return "/tag:\n" + out
	}

	name := parts[1]
	flags := parseTagFlags(parts[2:])

	if flags.message != "" {
		out, err := gitassist.CreateTag(ctx, name, flags.message)
		if err != nil {
			return "/tag: " + err.Error()
		}
		return "/tag: created " + name + "\n" + strings.TrimSpace(out)
	}

	if flags.auto {
		if m.provider == nil || m.modelID == "" {
			return "/tag: --auto needs provider/model wired — pass `-m \"…\"` instead"
		}
		prev := flags.from
		if prev == "" {
			lt, _ := gitassist.LatestTag(ctx, gitassist.DefaultRunner)
			prev = lt
		}
		log, err := gitassist.CommitsSinceTag(ctx, gitassist.DefaultRunner, prev)
		if err != nil {
			return "/tag: " + err.Error()
		}
		if strings.TrimSpace(log) == "" {
			return "/tag: no commits since " + prev + " — nothing to changelog"
		}
		gen := commitGeneratorFor(m.provider, m.modelID)
		raw, err := gen(ctx, gitassist.TagPrompt(prev, log))
		if err != nil {
			return "/tag: LLM error: " + err.Error()
		}
		body := gitassist.CleanLLMText(raw)
		out, err := gitassist.CreateTag(ctx, name, body)
		if err != nil {
			return "/tag: " + err.Error() + "\n\nbody was:\n" + body
		}
		return "/tag: created " + name + "\n\n" + body + "\n\n" + strings.TrimSpace(out)
	}

	// Bare `/tag <name>` → lightweight tag.
	out, err := gitassist.CreateTag(ctx, name, "")
	if err != nil {
		return "/tag: " + err.Error()
	}
	return "/tag: created lightweight tag " + name + "\n" + strings.TrimSpace(out)
}

type tagFlags struct {
	message string
	auto    bool
	from    string
}

func parseTagFlags(args []string) tagFlags {
	var out tagFlags
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-m", "--message":
			if i+1 < len(args) {
				rest := strings.Join(args[i+1:], " ")
				out.message = strings.Trim(rest, "\"'")
				return out
			}
		case "--auto":
			out.auto = true
		case "--from":
			if i+1 < len(args) {
				out.from = args[i+1]
				i++
			}
		}
	}
	return out
}
