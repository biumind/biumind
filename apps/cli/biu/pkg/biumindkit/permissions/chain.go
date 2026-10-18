// Decision chain: Decide evaluates a Request against an ordered list
// of named policies. First match wins; each policy returns ok=false
// to pass the request down the chain.
//
// Order of precedence (mirrors the design doc
// BiuMind-Auto-Permission-Mode-Design §3):
//
//  1. bypass mode → allow
//  2. plan mode → deny anything not read-only
//  3. session grant cache → allow
//  4. allowed prompts (ExitPlanMode batch approvals) → allow
//     unless a deny rule also matches
//  5. deny rule match → deny
//  6. ask rule match → ask
//  7. allow rule match → allow
//  8. read-only & non-destructive → allow (working-dir gate → ask)
//  9. write path outside working dirs → ask
//  10. acceptEdits + Edit/Write → allow
//  11. dontAsk → deny
//  12. otherwise → ask
//
// Mode-specific policies (auto-mode approve, dangerous-command guard)
// slot into this chain by precedence — see the design doc for their
// positions.

package permissions

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
	planModePolicy{},
	sessionGrantPolicy{},
	allowedPromptPolicy{},
	denyRulesPolicy{},
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
// Decide always returns a verdict.
func Decide(ctx *Context, r Request) (Decision, Reason) {
	if ctx == nil {
		ctx = NewContext()
	}
	for _, p := range decisionChain {
		if d, reason, ok := p.Decide(ctx, r); ok {
			return d, reason
		}
	}
	// Unreachable while fallbackAskPolicy terminates the chain.
	return DecideAsk, Reason{Kind: "default", Detail: "first use of " + r.Tool}
}

// bypassModePolicy (1): bypass / full_access mode — never ask.
type bypassModePolicy struct{}

func (bypassModePolicy) Name() string { return "bypass-mode-allow" }

func (bypassModePolicy) Decide(ctx *Context, _ Request) (Decision, Reason, bool) {
	switch ctx.Mode() {
	case ModeBypass, ModeFullAccess:
		return DecideAllow, Reason{Kind: "mode", Detail: "bypassPermissions"}, true
	}
	return 0, Reason{}, false
}

// planModePolicy (2): plan mode — read-only browsing, deny writes.
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

// sessionGrantPolicy (3): always-allow this exact (tool, key) granted
// earlier in the session.
type sessionGrantPolicy struct{}

func (sessionGrantPolicy) Name() string { return "session-grant-allow" }

func (sessionGrantPolicy) Decide(ctx *Context, r Request) (Decision, Reason, bool) {
	if ctx.HasGrant(SessionGrantKey(r.Tool, r.Args)) {
		return DecideAllow, Reason{Kind: "session", Detail: "session grant"}, true
	}
	return 0, Reason{}, false
}

// allowedPromptPolicy (4): semantic batch approvals from
// ExitPlanMode. Checked BEFORE deny rules so an allowedPrompts entry
// can't override an explicit deny: when a deny rule also matches the
// policy passes the request down so denyRulesPolicy emits the proper
// rule-source reason. Priority order is (deny > prompt-allow > ask >
// allow).
type allowedPromptPolicy struct{}

func (allowedPromptPolicy) Name() string { return "allowed-prompt-allow" }

func (allowedPromptPolicy) Decide(ctx *Context, r Request) (Decision, Reason, bool) {
	matched, ok := ctx.MatchAllowedPrompt(r.Tool, r.Args)
	if !ok {
		return 0, Reason{}, false
	}
	for _, rule := range ctx.AllRules(BehaviorDeny) {
		if rule.Value.MatchTool(r.Tool, r.Args) {
			// Deny wins — pass down to denyRulesPolicy.
			return 0, Reason{}, false
		}
	}
	return DecideAllow, Reason{
		Kind:   "allowedPrompt",
		Detail: "plan approval: " + matched.Prompt,
	}, true
}

// denyRulesPolicy (5): deny rules — strongest. A matching deny vetoes
// everything else.
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

// askRulesPolicy (6): ask rules — force a confirm even on otherwise
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

// allowRulesPolicy (7): allow rules.
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

// readOnlyPolicy (8): read-only & non-destructive → allow. A
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

// workdirWriteGatePolicy (9): write-style path tools — same
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

// acceptEditsPolicy (10): acceptEdits / auto_edit mode — Edit/Write
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

// dontAskPolicy (11): dontAsk mode — answer every ask with deny
// (panic mode).
type dontAskPolicy struct{}

func (dontAskPolicy) Name() string { return "dont-ask-deny" }

func (dontAskPolicy) Decide(ctx *Context, _ Request) (Decision, Reason, bool) {
	if ctx.Mode() == ModeDontAsk {
		return DecideDeny, Reason{Kind: "mode", Detail: "dontAsk mode"}, true
	}
	return 0, Reason{}, false
}

// fallbackAskPolicy (12): chain terminator — first use of a
// destructive tool in default mode asks.
type fallbackAskPolicy struct{}

func (fallbackAskPolicy) Name() string { return "fallback-ask" }

func (fallbackAskPolicy) Decide(_ *Context, r Request) (Decision, Reason, bool) {
	return DecideAsk, Reason{Kind: "default", Detail: "first use of " + r.Tool}, true
}
