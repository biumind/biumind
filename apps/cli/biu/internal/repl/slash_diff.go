// /diff slash — git diff against HEAD or a given ref.
//
// Convenience over `Bash(git diff …)`
// because: (1) shorter to type during code-review flows;
// (2) auto-resolves the right base (origin/HEAD → main → master)
// for "diff against the integration branch I should be merging into".
//
// Sub-forms:
//
//	/diff                — vs HEAD (working tree changes)
//	/diff staged         — vs HEAD --staged
//	/diff main           — current branch vs main (range diff)
//	/diff <ref>          — vs <ref> (any rev-parseable)
//
// Renders with --stat by default + a "use Bash(git diff <args>) for
// full content" hint to keep the slash output bounded.

package repl

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func (m model) handleDiff(parts []string) string {
	if _, err := exec.LookPath("git"); err != nil {
		return "/diff: git not on PATH"
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "/diff: " + err.Error()
	}
	if out, err := gitOut(cwd, "rev-parse", "--is-inside-work-tree"); err != nil ||
		strings.TrimSpace(out) != "true" {
		return "/diff: not inside a git work tree"
	}

	args, label := buildDiffArgs(parts)

	// --stat for the slash output (compact). Full diff via Bash.
	statArgs := append([]string{"diff", "--stat"}, args...)
	statOut, err := gitOut(cwd, statArgs...)
	if err != nil {
		return "/diff: " + err.Error() + "\n" + statOut
	}
	statOut = strings.TrimRight(statOut, "\n")

	if statOut == "" {
		return fmt.Sprintf("/diff: no changes %s", label)
	}

	// Quick stat — line counts + first 20 entries.
	var b strings.Builder
	fmt.Fprintf(&b, "/diff %s:\n%s\n", label, statOut)
	fmt.Fprintf(&b, "\nfor full content: Bash(git diff %s)", strings.Join(args, " "))
	return b.String()
}

// buildDiffArgs returns (git diff args, human label). The label is
// surfaced in the slash output so users know exactly what was
// diffed without having to re-derive from the args.
func buildDiffArgs(parts []string) (args []string, label string) {
	if len(parts) <= 1 {
		return nil, "(working tree vs HEAD)"
	}
	switch strings.ToLower(parts[1]) {
	case "staged", "cached":
		return []string{"--staged"}, "(staged vs HEAD)"
	default:
		ref := parts[1]
		// `<ref>...HEAD` for branch ranges; the 3-dot form keeps
		// the merge-base behaviour. Only treat the arg as a branch
		// when it doesn't look like a flag.
		if strings.HasPrefix(ref, "-") {
			return parts[1:], fmt.Sprintf("(custom: %s)", strings.Join(parts[1:], " "))
		}
		return []string{ref + "...HEAD"}, fmt.Sprintf("(vs %s, range)", ref)
	}
}
