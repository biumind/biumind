// Decision chain: Decide evaluates a Request against an ordered list
// of named policies. First match wins; each policy returns ok=false
// to pass the request down the chain.
//
// Order of precedence (mirrors the design doc
// BiuMind-Auto-Permission-Mode-Design §3):
//
//  1. bypass mode → allow (legacy yolo; skips rule evaluation)
//  2. deny rule match → deny (strongest; vetoes everything below)
//  3. plan mode → deny anything not read-only
//  4. auto + dangerous command (tool warnings) → ask
//  5. auto + sensitive path (.env / keys / .git …) → ask
//  6. auto mode → allow (deny rules above still win)
//  7. session grant cache → allow
//  8. allowed prompts (ExitPlanMode batch approvals) → allow
//  9. ask rule match → ask
//  10. allow rule match → allow
//  11. read-only & non-destructive → allow (working-dir gate → ask)
//  12. write path outside working dirs → ask
//  13. acceptEdits + Edit/Write → allow
//  14. dontAsk → deny
//  15. otherwise → ask
//
// The two guards (4, 5) only fire in auto mode with guards enabled
// (Context.GuardsEnabled, default on; cloud sandboxes turn it off —
// see Context.SetGuardsEnabled). In every other mode the same calls
// already land on ask further down the chain, so the guards would be
// redundant there.

package permissions

import "strings"

// ChainPolicy is one stage of the decision chain. Decide returns the
// verdict and ok=true when the stage takes responsibility for the
// request, or ok=false to pass it to the next stage.
type ChainPolicy interface {
	Name() string
	Decide(ctx *Context, r Request) (Decision, Reason, bool)
}

// DecisionChain returns the ordered policy list Decide evaluates.
// Exposed so embedders (runtime service, tests) can inspect or wrap
// the chain without re-implementing it.
func DecisionChain() []ChainPolicy {
	return decisionChain
}

var decisionChain = []ChainPolicy{
	bypassModePolicy{},
	denyRulesPolicy{},
	planModePolicy{},
	dangerousCommandGuardPolicy{},
	sensitivePathGuardPolicy{},
	autoModePolicy{},
	sessionGrantPolicy{},
	allowedPromptPolicy{},
	askRulesPolicy{},
	allowRulesPolicy{},
	readOnlyPolicy{},
	workdirWriteGatePolicy{},
	acceptEditsPolicy{},
	dontAskPolicy{},
	fallbackAskPolicy{},
}

// Decide evaluates a Request against a Context by walking
// decisionChain in order. fallbackAskPolicy terminates the chain, so
// Decide always returns a verdict. The winning policy's Name() is
// stamped onto Reason.Policy so callers can branch on the policy
// identity rather than the human-readable text.
func Decide(ctx *Context, r Request) (Decision, Reason) {
	if ctx == nil {
		ctx = NewContext()
	}
	for _, p := range decisionChain {
		if d, reason, ok := p.Decide(ctx, r); ok {
			reason.Policy = p.Name()
			return d, reason
		}
	}
	// Unreachable while fallbackAskPolicy terminates the chain.
	return DecideAsk, Reason{Kind: "default", Detail: "first use of " + r.Tool}
}

// bypassModePolicy (1): bypass / full_access mode — never ask, never
// evaluate rules. This is the legacy yolo mode; ModeAuto is the safe
// alternative that keeps deny rules authoritative.
type bypassModePolicy struct{}

func (bypassModePolicy) Name() string { return "bypass-mode-allow" }

func (bypassModePolicy) Decide(ctx *Context, _ Request) (Decision, Reason, bool) {
	switch ctx.Mode() {
	case ModeBypass, ModeFullAccess:
		return DecideAllow, Reason{Kind: "mode", Detail: "bypassPermissions"}, true
	}
	return 0, Reason{}, false
}

// denyRulesPolicy (2): deny rules — strongest. A matching deny vetoes
// everything else, including session grants, allowed prompts and
// auto mode.
type denyRulesPolicy struct{}

func (denyRulesPolicy) Name() string { return "user-configured-deny" }

func (denyRulesPolicy) Decide(ctx *Context, r Request) (Decision, Reason, bool) {
	for _, rule := range ctx.AllRules(BehaviorDeny) {
		if rule.Value.MatchTool(r.Tool, r.Args) {
			return DecideDeny, Reason{
				Kind: "rule", Detail: "deny rule match",
				Source: rule.Source, Rule: rule.Value,
			}, true
		}
	}
	return 0, Reason{}, false
}

// planModePolicy (3): plan mode — read-only browsing, deny writes.
//
// EnterPlanMode and ExitPlanMode are exempt: they're the legitimate
// transition tools that flip the mode itself. Without this exemption
// ExitPlanMode would be denied and the session would have no way out.
type planModePolicy struct{}

func (planModePolicy) Name() string { return "plan-mode-readonly-deny" }

func (planModePolicy) Decide(ctx *Context, r Request) (Decision, Reason, bool) {
	if ctx.Mode() == ModePlan && !r.IsReadOnly && !isPlanTransition(r.Tool) {
		return DecideDeny, Reason{Kind: "mode", Detail: "plan mode (read-only)"}, true
	}
	return 0, Reason{}, false
}

// dangerousCommandGuardPolicy (4): auto mode + tool-flagged
// dangerous call (Warner warnings, e.g. rm -rf / git push --force) →
// ask. Only fires with guards enabled; sandboxes disable the guards
// because their asks fail-closed. In other modes the same call
// already reaches fallback-ask, so the guard is auto-only.
type dangerousCommandGuardPolicy struct{}

func (dangerousCommandGuardPolicy) Name() string { return "dangerous-command-ask" }

func (dangerousCommandGuardPolicy) Decide(ctx *Context, r Request) (Decision, Reason, bool) {
	if ctx.Mode() != ModeAuto || !ctx.GuardsEnabled() || len(r.Warnings) == 0 {
		return 0, Reason{}, false
	}
	return DecideAsk, Reason{
		Kind:   "guard",
		Detail: "dangerous command guard: " + strings.Join(r.Warnings, "; "),
	}, true
}

// sensitivePathGuardPolicy (5): auto mode + path arg naming a
// sensitive location (.env, private keys, .git/.ssh/.aws segments,
// shell rc files) → ask. Same auto-only, guards-enabled gating as (4).
type sensitivePathGuardPolicy struct{}

func (sensitivePathGuardPolicy) Name() string { return "sensitive-path-ask" }

func (sensitivePathGuardPolicy) Decide(ctx *Context, r Request) (Decision, Reason, bool) {
	if ctx.Mode() != ModeAuto || !ctx.GuardsEnabled() {
		return 0, Reason{}, false
	}
	path := stringField(r.Args, "file_path", "path", "pattern", "notebook_path")
	if path == "" || !isSensitivePath(path) {
		return 0, Reason{}, false
	}
	return DecideAsk, Reason{
		Kind:   "guard",
		Detail: "sensitive path guard: " + path,
	}, true
}

// autoModePolicy (6): auto mode — approve everything that reached
// this point. Deny rules (2) have already had their veto, so unlike
// bypass this mode stays safe against user-configured denials; the
// P1 guards (dangerous commands, sensitive paths) will slot in ahead
// of this stage.
type autoModePolicy struct{}

func (autoModePolicy) Name() string { return "auto-mode-approve" }

func (autoModePolicy) Decide(ctx *Context, _ Request) (Decision, Reason, bool) {
	if ctx.Mode() == ModeAuto {
		return DecideAllow, Reason{Kind: "mode", Detail: "auto mode"}, true
	}
	return 0, Reason{}, false
}

// sessionGrantPolicy (7): always-allow this exact (tool, key) granted
// earlier in the session.
type sessionGrantPolicy struct{}

func (sessionGrantPolicy) Name() string { return "session-grant-allow" }

func (sessionGrantPolicy) Decide(ctx *Context, r Request) (Decision, Reason, bool) {
	if ctx.HasGrant(SessionGrantKey(r.Tool, r.Args)) {
		return DecideAllow, Reason{Kind: "session", Detail: "session grant"}, true
	}
	return 0, Reason{}, false
}

// allowedPromptPolicy (8): semantic batch approvals from
// ExitPlanMode. Deny rules have already run (2), so a prompt-allow
// can never override an explicit deny.
type allowedPromptPolicy struct{}

func (allowedPromptPolicy) Name() string { return "allowed-prompt-allow" }

func (allowedPromptPolicy) Decide(ctx *Context, r Request) (Decision, Reason, bool) {
	matched, ok := ctx.MatchAllowedPrompt(r.Tool, r.Args)
	if !ok {
		return 0, Reason{}, false
	}
	return DecideAllow, Reason{
		Kind:   "allowedPrompt",
		Detail: "plan approval: " + matched.Prompt,
	}, true
}

// askRulesPolicy (9): ask rules — force a confirm even on otherwise
// auto-allowable calls (e.g. read-only).
type askRulesPolicy struct{}

func (askRulesPolicy) Name() string { return "user-configured-ask" }

func (askRulesPolicy) Decide(ctx *Context, r Request) (Decision, Reason, bool) {
	for _, rule := range ctx.AllRules(BehaviorAsk) {
		if rule.Value.MatchTool(r.Tool, r.Args) {
			return DecideAsk, Reason{
				Kind: "rule", Detail: "ask rule match",
				Source: rule.Source, Rule: rule.Value,
			}, true
		}
	}
	return 0, Reason{}, false
}

// allowRulesPolicy (10): allow rules.
type allowRulesPolicy struct{}

func (allowRulesPolicy) Name() string { return "user-configured-allow" }

func (allowRulesPolicy) Decide(ctx *Context, r Request) (Decision, Reason, bool) {
	for _, rule := range ctx.AllRules(BehaviorAllow) {
		if rule.Value.MatchTool(r.Tool, r.Args) {
			return DecideAllow, Reason{
				Kind: "rule", Detail: "allow rule match",
				Source: rule.Source, Rule: rule.Value,
			}, true
		}
	}
	return 0, Reason{}, false
}

// readOnlyPolicy (11): read-only & non-destructive → allow. A
// read-only path tool whose path is outside every registered working
// dir falls through to ask instead.
type readOnlyPolicy struct{}

func (readOnlyPolicy) Name() string { return "readonly-approve" }

func (readOnlyPolicy) Decide(ctx *Context, r Request) (Decision, Reason, bool) {
	if !r.IsReadOnly || r.IsDestructive {
		return 0, Reason{}, false
	}
	if isPathTool(r.Tool) && pathOutsideWorkingDirs(ctx, r) {
		return DecideAsk, Reason{
			Kind:   "workingDir",
			Detail: "path is outside the allowed working directories",
		}, true
	}
	return DecideAllow, Reason{Kind: "readOnly", Detail: "read-only tool"}, true
}

// workdirWriteGatePolicy (12): write-style path tools — same
// working-dir gate. Without this, a model writing to /etc/passwd
// would slip through acceptEdits or the default mode.
type workdirWriteGatePolicy struct{}

func (workdirWriteGatePolicy) Name() string { return "workdir-write-gate" }

func (workdirWriteGatePolicy) Decide(ctx *Context, r Request) (Decision, Reason, bool) {
	if isWritePathTool(r.Tool) && pathOutsideWorkingDirs(ctx, r) {
		return DecideAsk, Reason{
			Kind:   "workingDir",
			Detail: "path is outside the allowed working directories",
		}, true
	}
	return 0, Reason{}, false
}

// acceptEditsPolicy (13): acceptEdits / auto_edit mode — Edit/Write
// get auto-allowed.
type acceptEditsPolicy struct{}

func (acceptEditsPolicy) Name() string { return "accept-edits-approve" }

func (acceptEditsPolicy) Decide(ctx *Context, r Request) (Decision, Reason, bool) {
	switch ctx.Mode() {
	case ModeAcceptEdits, ModeAutoEdit:
		if isEditish(r.Tool) || (!r.IsDestructive && !isShell(r.Tool)) {
			return DecideAllow, Reason{Kind: "mode", Detail: "acceptEdits"}, true
		}
	}
	return 0, Reason{}, false
}

// dontAskPolicy (14): dontAsk mode — answer every ask with deny
// (panic mode).
type dontAskPolicy struct{}

func (dontAskPolicy) Name() string { return "dont-ask-deny" }

func (dontAskPolicy) Decide(ctx *Context, _ Request) (Decision, Reason, bool) {
	if ctx.Mode() == ModeDontAsk {
		return DecideDeny, Reason{Kind: "mode", Detail: "dontAsk mode"}, true
	}
	return 0, Reason{}, false
}

// fallbackAskPolicy (15): chain terminator — first use of a
// destructive tool in default mode asks.
type fallbackAskPolicy struct{}

func (fallbackAskPolicy) Name() string { return "fallback-ask" }

func (fallbackAskPolicy) Decide(_ *Context, r Request) (Decision, Reason, bool) {
	return DecideAsk, Reason{Kind: "default", Detail: "first use of " + r.Tool}, true
}
