package agents

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ScaffoldScope picks the directory where a freshly-scaffolded agent
// definition lands.
type ScaffoldScope string

const (
	// ScopeUser writes to ~/.biumind/agents/<name>.md — the agent
	// is available in every biu session for this user.
	ScopeUser ScaffoldScope = "user"
	// ScopeProject writes to <cwd>/.biumind/agents/<name>.md — the
	// agent ships with the repo so collaborators on the project
	// share the same definitions.
	ScopeProject ScaffoldScope = "project"
)

// ScaffoldOptions configures a single Scaffold call. Cwd is required
// when Scope == ScopeProject; for ScopeUser it's ignored.
type ScaffoldOptions struct {
	Name  string
	Scope ScaffoldScope
	Cwd   string
	Home  string // resolved $HOME; empty = derive via os.UserHomeDir
	// Force overwrites an existing file. Without it, Scaffold
	// refuses when the target exists — same posture as /init.
	Force bool

	// Preset selects a starter template. Empty = "default" (a
	// minimal stub the user fills in). Other recognised values
	// match the names of built-in agents so a user wanting to
	// customise the read-only research posture can run
	// `/agents create my-search --from explore` and get the
	// shape pre-filled.
	Preset string
}

// ScaffoldResult records what got written. Path is the absolute
// filename so callers can echo "wrote /Users/x/.biumind/agents/Y.md".
type ScaffoldResult struct {
	Path        string
	UsedPreset  string // "default" or the preset name actually rendered
	Overwritten bool
}

// validNamePattern is what we accept as agent names. It follows the
// kebab-case convention but also tolerates plain ASCII titles
// (e.g. "MyAgent") because biu's built-ins use mixed case.
var validNamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}$`)

// reservedNames is the set of built-in agent names a user cannot
// scaffold over — overwriting one of these would mask the built-in
// in surprising ways. Power users who do want to override should
// edit the file by hand AFTER running `/agents create my-plan` and
// renaming.
var reservedNames = map[string]bool{
	"Plan":            true,
	"Explore":         true,
	"CodeReview":      true,
	"Verification":    true,
	"general-purpose": true,
}

// Scaffold writes a starter agent definition under the requested
// scope. The file is YAML-frontmatter + Markdown body, the same
// shape Load() consumes — so a fresh scaffold becomes a valid
// agent on the next /agents reload (or biu restart).
//
// Returns ScaffoldResult on success. Errors:
//
//   - empty / invalid name (regex mismatch)
//   - reserved name (collision with a built-in)
//   - target exists + Force=false
//   - filesystem failures (mkdir / write / stat)
func Scaffold(opt ScaffoldOptions) (ScaffoldResult, error) {
	if err := validateScaffoldName(opt.Name); err != nil {
		return ScaffoldResult{}, err
	}
	if reservedNames[opt.Name] {
		return ScaffoldResult{}, fmt.Errorf(
			"agents: %q collides with a built-in (Plan / Explore / "+
				"CodeReview / Verification / general-purpose). Pick "+
				"another name, or edit the built-in's .md file under "+
				"~/.biumind/agents/ to override it after creation",
			opt.Name)
	}

	dir, err := scaffoldDir(opt)
	if err != nil {
		return ScaffoldResult{}, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ScaffoldResult{}, fmt.Errorf("agents: mkdir %s: %w", dir, err)
	}
	target := filepath.Join(dir, opt.Name+".md")

	overwritten := false
	if _, err := os.Stat(target); err == nil {
		if !opt.Force {
			return ScaffoldResult{}, fmt.Errorf(
				"agents: %s already exists. Re-run with --force to overwrite",
				target)
		}
		overwritten = true
	}

	preset := canonicalisePreset(opt.Preset)
	body := renderScaffold(opt.Name, preset)
	if err := os.WriteFile(target, []byte(body), 0o644); err != nil {
		return ScaffoldResult{}, fmt.Errorf("agents: write %s: %w", target, err)
	}
	return ScaffoldResult{
		Path:        target,
		UsedPreset:  presetLabel(preset),
		Overwritten: overwritten,
	}, nil
}

// canonicalisePreset folds aliases (review → review, code-review →
// review, codereview → review) to one form AND coerces unknown
// values to "" (which renderScaffold treats as default). Centralised
// so the result struct's UsedPreset matches what renderScaffold
// actually used.
func canonicalisePreset(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "explore":
		return "explore"
	case "review", "code-review", "codereview":
		return "review"
	case "verify", "verification":
		return "verify"
	case "plan":
		return "plan"
	default:
		return ""
	}
}

// scaffoldDir resolves the directory the new file lands in based on
// the requested scope. Validation is centralised here so callers
// can pass loosely-typed scope strings ("user"/"project"/"") and
// get a single error path.
func scaffoldDir(opt ScaffoldOptions) (string, error) {
	switch opt.Scope {
	case "", ScopeUser:
		home := opt.Home
		if home == "" {
			h, err := os.UserHomeDir()
			if err != nil {
				return "", fmt.Errorf("agents: cannot resolve $HOME: %w", err)
			}
			home = h
		}
		return filepath.Join(home, ".biumind", "agents"), nil
	case ScopeProject:
		if opt.Cwd == "" {
			return "", errors.New("agents: project scope requires a cwd")
		}
		return filepath.Join(opt.Cwd, ".biumind", "agents"), nil
	default:
		return "", fmt.Errorf("agents: unknown scope %q (use user|project)", opt.Scope)
	}
}

func validateScaffoldName(name string) error {
	if name == "" {
		return errors.New("agents: name is required")
	}
	if !validNamePattern.MatchString(name) {
		return fmt.Errorf(
			"agents: %q is not a valid name (use letters/digits/_/- only, "+
				"start with a letter, ≤64 chars)", name)
	}
	return nil
}

// presetLabel canonicalises the preset name we record back. Empty
// becomes "default" so result strings are uniform.
func presetLabel(p string) string {
	if p == "" {
		return "default"
	}
	return p
}

// renderScaffold builds the YAML-frontmatter + body text. Each
// preset chooses a different (tools, permissionMode, model, body)
// shape that matches a real-world starting point.
func renderScaffold(name, preset string) string {
	var (
		description     string
		tools           string
		disallowedTools string
		permissionMode  string
		model           string
		body            string
	)

	switch preset {
	case "explore":
		description = "Read-only repo exploration. Use when you need files / patterns located fast."
		tools = "Read, Glob, Grep, Bash, WebFetch"
		disallowedTools = "Edit, Write, MultiEdit, NotebookEdit, Agent, ExitPlanMode"
		model = "claude-haiku-4-5"
		body = exploreBodyTemplate(name)
	case "review", "code-review", "codereview":
		description = "Read-only code reviewer. Pass the diff scope or files in the prompt; returns severity-tagged feedback."
		tools = "Read, Glob, Grep, Bash, WebFetch"
		disallowedTools = "Edit, Write, MultiEdit, NotebookEdit, Agent, ExitPlanMode"
		model = "claude-sonnet-4-6"
		body = reviewBodyTemplate(name)
	case "verify", "verification":
		description = "Runs the implementation to find runtime issues. Returns PASS / FAIL / PARTIAL with command outputs."
		tools = "Read, Glob, Grep, Bash, WebFetch, BashOutput, KillBash"
		disallowedTools = "Edit, Write, MultiEdit, NotebookEdit, Agent, ExitPlanMode"
		model = "claude-sonnet-4-6"
		body = verifyBodyTemplate(name)
	case "plan":
		description = "Read-only planner. Designs an implementation plan, then ExitPlanMode with allowedPrompts."
		tools = "Read, Glob, Grep, Bash, WebFetch, ExitPlanMode"
		permissionMode = "plan"
		body = planBodyTemplate(name)
	default:
		preset = "" // canonicalise unknown → default
		description = "TODO: one-line description (shown in /agents listing + Agent tool selector)."
		tools = "Read, Glob, Grep" // safe default: read-only
		body = defaultBodyTemplate(name)
	}

	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "name: %s\n", name)
	fmt.Fprintf(&b, "description: %s\n", description)
	if tools != "" {
		fmt.Fprintf(&b, "tools: %s\n", tools)
	}
	if disallowedTools != "" {
		fmt.Fprintf(&b, "disallowedTools: %s\n", disallowedTools)
	}
	if permissionMode != "" {
		fmt.Fprintf(&b, "permissionMode: %s\n", permissionMode)
	}
	if model != "" {
		fmt.Fprintf(&b, "model: %s\n", model)
	}
	b.WriteString("---\n\n")
	b.WriteString(body)
	if !strings.HasSuffix(body, "\n") {
		b.WriteByte('\n')
	}
	return b.String()
}

// ─── body templates ─────────────────────────────────────

func defaultBodyTemplate(name string) string {
	return fmt.Sprintf(`You are the %q sub-agent for biu.

TODO: rewrite this system prompt with the actual job description.
Keep it short and operational — the model already knows how to use
its tools; this prompt is for narrowing scope and biasing style.

## What to do

- TODO: bullet list of the agent's responsibilities.

## Output format

- TODO: how should this agent's final reply look? Markdown? a
  numbered list? a fixed verdict line the parent parses?
`, name)
}

func exploreBodyTemplate(name string) string {
	return fmt.Sprintf(`You are the %q sub-agent — a read-only repo explorer.

=== READ-ONLY MODE ===
You are STRICTLY PROHIBITED from creating / modifying / deleting any
files. Use Bash only for read-only inspection: ls, git log, git diff,
cat, head, tail.

## Your strengths

- Locating files via glob patterns
- Searching contents with regex
- Summarising findings with file:line citations

## Output

- Lead with a 1–2 sentence answer.
- Cite findings with absolute or repo-relative paths + line numbers.
- Stop as soon as the question is answered — don't gold-plate.
`, name)
}

func reviewBodyTemplate(name string) string {
	return fmt.Sprintf(`You are the %q code reviewer.

=== READ-ONLY MODE ===
You read code; you do not modify it. Use Bash only for `+"`"+`git diff`+"`"+`,
`+"`"+`git log`+"`"+`, `+"`"+`git blame`+"`"+`, and project linters in dry-run mode.

## Severity tags (REQUIRED)

Every finding must carry one of:
[BLOCKER] [MAJOR] [MINOR] [NITPICK] [QUESTION] [PRAISE]

## Output

Start with: **Summary:** N BLOCKERs, M MAJORs.
Then per-file findings, then optional "Open questions".
`, name)
}

func verifyBodyTemplate(name string) string {
	return fmt.Sprintf(`You are the %q verifier — try to break the implementation, not confirm it.

=== READ-ONLY ON THE PROJECT ===
You may write ephemeral test scripts to /tmp via Bash redirection,
but you MUST NOT touch the project directory. You may also use
`+"`"+`Bash{run_in_background:true}`+"`"+` for long-running probes (dev server,
log tail) and clean up with KillBash before returning a verdict.

## Required steps

1. Read BIUMIND.md for build/test commands.
2. Run the build (when applicable) — broken build = automatic FAIL.
3. Run the test suite.
4. At least ONE adversarial probe (concurrency / boundary / orphan op).

## Verdict (REQUIRED last line)

End with EXACTLY one of:
VERDICT: PASS
VERDICT: FAIL
VERDICT: PARTIAL
`, name)
}

func planBodyTemplate(name string) string {
	return fmt.Sprintf(`You are the %q planner — design implementation plans, never modify code.

=== READ-ONLY MODE ===
The session enforces this — Edit / Write / Bash mutations are denied
by the permission engine. Do not attempt them.

## Process

1. Re-state the task to surface misalignments.
2. Explore the codebase via Read / Glob / Grep.
3. Design the solution + alternatives + trade-offs.
4. Detail the plan as numbered steps that name the files involved.

## Finish with

Call ExitPlanMode with:
- the polished plan as `+"`"+`plan`+"`"+`
- an `+"`"+`allowedPrompts`+"`"+` array covering shell categories the plan needs
  ([{tool: "Bash", prompt: "run tests"}, …]).
`, name)
}
