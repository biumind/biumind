// Package permissions evaluates per-tool permission decisions.
//
// The package now exposes two layers:
//
//   * Context + Decide — the model used by the engine
//     (rule matching across user/project/local/cliArg sources +
//     mode-driven defaults + ephemeral session grants).
//   * Policy + Evaluate — the legacy single-policy + single-allowlist
//     interface kept for older biu callers (config.toml). It is now a
//     thin facade that builds a temporary Context and delegates.
//
// Engine code should always use Context. Library callers / tests can
// keep using Policy until their config-loading path is migrated.

package permissions

import (
	"strings"
)

// Decision is the runtime verdict for one tool call.
type Decision int

const (
	DecideAllow Decision = iota
	DecideAsk
	DecideDeny
)

// Reason explains a Decision in human-readable form. Source is set
// when a rule produced the decision so the UI can render "blocked by
// projectSettings".
type Reason struct {
	Kind   string // "rule" | "mode" | "readOnly" | "session" | "default"
	Detail string
	Source Source // populated when Kind == "rule"
	Rule   RuleValue
}

// Request is the input to Decide / Evaluate.
type Request struct {
	Tool          string
	Args          map[string]any
	IsReadOnly    bool
	IsDestructive bool
}

// Decide evaluates a Request against a Context. Order of precedence:
//
//  1. bypass mode → allow
//  2. plan mode → deny anything not read-only
//  3. session grant cache → allow
//  4. deny rule match → deny
//  5. ask rule match → ask
//  6. allow rule match → allow
//  7. read-only & non-destructive → allow
//  8. acceptEdits + Edit/Write → allow
//  9. dontAsk → deny (sentinel)
//  10. default mode + non-destructive → allow
//  11. otherwise → ask
func Decide(ctx *Context, r Request) (Decision, Reason) {
	if ctx == nil {
		ctx = NewContext()
	}
	mode := ctx.Mode()

	// (1) Bypass — yolo mode.
	if mode == ModeBypass || mode == ModeFullAccess {
		return DecideAllow, Reason{Kind: "mode", Detail: "bypassPermissions"}
	}

	// (2) Plan — read-only browsing.
	//
	// EnterPlanMode and ExitPlanMode are exempt: they're the
	// legitimate transition tools that flip the mode itself. Without
	// this exemption ExitPlanMode would be denied and the session
	// would have no way out.
	if mode == ModePlan && !r.IsReadOnly && !isPlanTransition(r.Tool) {
		return DecideDeny, Reason{Kind: "mode", Detail: "plan mode (read-only)"}
	}

	// (3) Session grants (always-allow this exact (tool, key)).
	grantKey := SessionGrantKey(r.Tool, r.Args)
	if ctx.HasGrant(grantKey) {
		return DecideAllow, Reason{Kind: "session", Detail: "session grant"}
	}

	// (3b) Allowed prompts — semantic batch approvals from
	// ExitPlanMode. Checked AFTER deny-key cache to keep deny rules
	// authoritative, BEFORE deny rules so an `allowedPrompts` entry
	// can't override an explicit deny. Priority order is
	// (deny > prompt-allow > ask > allow).
	if matched, ok := ctx.MatchAllowedPrompt(r.Tool, r.Args); ok {
		// Deny rules still get a chance to veto below — fall through
		// to (4) first, then return Allow if none match.
		denyHit := false
		for _, rule := range ctx.AllRules(BehaviorDeny) {
			if rule.Value.MatchTool(r.Tool, r.Args) {
				denyHit = true
				break
			}
		}
		if !denyHit {
			return DecideAllow, Reason{
				Kind:   "allowedPrompt",
				Detail: "plan approval: " + matched.Prompt,
			}
		}
		// Deny wins — fall through to step (4) and emit the proper
		// rule-source reason.
	}

	// (4) Deny rules — strongest. A matching deny vetoes everything else.
	for _, rule := range ctx.AllRules(BehaviorDeny) {
		if rule.Value.MatchTool(r.Tool, r.Args) {
			return DecideDeny, Reason{
				Kind: "rule", Detail: "deny rule match",
				Source: rule.Source, Rule: rule.Value,
			}
		}
	}

	// (5) Ask rules — force a confirm even on otherwise auto-allowable
	// calls (e.g. read-only).
	for _, rule := range ctx.AllRules(BehaviorAsk) {
		if rule.Value.MatchTool(r.Tool, r.Args) {
			return DecideAsk, Reason{
				Kind: "rule", Detail: "ask rule match",
				Source: rule.Source, Rule: rule.Value,
			}
		}
	}

	// (6) Allow rules.
	for _, rule := range ctx.AllRules(BehaviorAllow) {
		if rule.Value.MatchTool(r.Tool, r.Args) {
			return DecideAllow, Reason{
				Kind: "rule", Detail: "allow rule match",
				Source: rule.Source, Rule: rule.Value,
			}
		}
	}

	// (7) Read-only auto-allow.
	if r.IsReadOnly && !r.IsDestructive {
		// Working-directory gate: a read-only tool whose path is
		// outside every registered working dir falls through to ask
		// even when the tool is read-only.
		if isPathTool(r.Tool) && pathOutsideWorkingDirs(ctx, r) {
			return DecideAsk, Reason{
				Kind:   "workingDir",
				Detail: "path is outside the allowed working directories",
			}
		}
		return DecideAllow, Reason{Kind: "readOnly", Detail: "read-only tool"}
	}

	// (7.5) Write-style path tools — same working-dir gate. Without
	// this, a model writing to /etc/passwd would slip through step
	// (8) acceptEdits or step (11) default.
	if isWritePathTool(r.Tool) && pathOutsideWorkingDirs(ctx, r) {
		return DecideAsk, Reason{
			Kind:   "workingDir",
			Detail: "path is outside the allowed working directories",
		}
	}

	// (8) acceptEdits — Edit/Write get auto-allowed.
	if mode == ModeAcceptEdits || mode == ModeAutoEdit {
		if isEditish(r.Tool) || (!r.IsDestructive && !isShell(r.Tool)) {
			return DecideAllow, Reason{Kind: "mode", Detail: "acceptEdits"}
		}
	}

	// (9) DontAsk — answer every ask with deny (panic mode).
	if mode == ModeDontAsk {
		return DecideDeny, Reason{Kind: "mode", Detail: "dontAsk mode"}
	}

	// (11) Default: ask.
	return DecideAsk, Reason{Kind: "default", Detail: "first use of " + r.Tool}
}

func isEditish(name string) bool {
	switch name {
	case "Edit", "edit", "Write", "write", "MultiEdit", "multi_edit",
		"NotebookEdit", "notebook_edit":
		return true
	}
	return false
}

func isShell(name string) bool {
	return strings.EqualFold(name, "Bash")
}

// isPathTool reports whether the named tool's primary input names a
// filesystem path the working-directory gate cares about. Read-side:
// Read / Glob / Grep / NotebookRead. Write-side tools are covered by
// isWritePathTool — kept separate so the gate's two call sites
// reflect the read-only vs write-side distinction.
func isPathTool(name string) bool {
	switch strings.ToLower(name) {
	case "read", "glob", "grep", "notebookread":
		return true
	}
	return false
}

// isWritePathTool covers the file-mutating tools whose path argument
// must be inside an allowed working directory.
func isWritePathTool(name string) bool {
	switch strings.ToLower(name) {
	case "edit", "multiedit", "write", "notebookedit":
		return true
	}
	return false
}

// pathOutsideWorkingDirs returns true iff the request names a path
// (path / file_path / pattern arg) that does NOT sit inside any
// registered working directory. Empty path / unresolvable path
// returns false (fail-open) so we don't accidentally gate a tool
// call that has no path semantic at all.
//
// Containment uses PathInAllowedWorkingPath, so the macOS /private
// quirk + case-insensitive comparison both apply.
func pathOutsideWorkingDirs(ctx *Context, r Request) bool {
	originalCwd := ""
	if ctx != nil {
		originalCwd = ctx.OriginalCwd()
	}
	// No anchor → fall back to allowing (early startup, headless tests
	// without a wiring step).
	if originalCwd == "" && (ctx == nil || len(ctx.AdditionalDirectoryPaths()) == 0) {
		return false
	}

	path := stringField(r.Args, "file_path", "path", "pattern", "notebook_path")
	if path == "" {
		// No path arg — caller is something like "Read" with no input;
		// engine should refuse for malformed input but this gate
		// shouldn't add a false ask.
		return false
	}
	return !PathInAllowedWorkingPath(path, ctx, originalCwd)
}

// isPlanTransition matches the two tools that flip plan mode itself.
// Always lower-case-equal to keep stable across registries that vary
// the exact symbol case.
func isPlanTransition(name string) bool {
	return strings.EqualFold(name, "EnterPlanMode") ||
		strings.EqualFold(name, "ExitPlanMode")
}

// ─── Legacy Policy facade ──────────────────────────────

// Policy is the historical struct kept for callers using the old
// "ask | auto_edit | full_access + allowlist" shape. New code should
// use Context.
type Policy struct {
	Mode      Mode
	Allowlist []string // patterns: "tool:detail" e.g. "bash:ls", "read:**"
}

// New constructs a Policy with the legacy shape.
func New(mode Mode, allowlist []string) *Policy {
	return &Policy{Mode: mode, Allowlist: allowlist}
}

// Evaluate is the legacy single-call API. Internally it builds a
// Context and delegates to Decide so behaviour stays consistent.
func (p *Policy) Evaluate(r Request) (Decision, string) {
	ctx := NewContext()
	ctx.SetMode(p.Mode)
	if len(p.Allowlist) > 0 {
		ctx.ReplaceRules(SrcUserSettings, BehaviorAllow, p.Allowlist)
	}
	dec, reason := Decide(ctx, r)
	return dec, reasonString(reason)
}

func reasonString(r Reason) string {
	if r.Kind == "rule" {
		return r.Detail + " (" + string(r.Source) + ":" + r.Rule.String() + ")"
	}
	return r.Detail
}
