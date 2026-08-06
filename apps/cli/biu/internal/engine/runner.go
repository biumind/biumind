// Single tool-call executor. Layered between the AssistantMessage
// (which contains tool_use blocks the LLM emitted) and the Tool
// interface that actually does the work.
//
// Responsibilities:
//
//   1. Look up the tool in the registry; emit a soft error if missing
//      (LLM may have hallucinated a tool name; let it recover).
//   2. Resolve permissions:
//        a. Local fast-path (allowlist / denylist match).
//        b. Otherwise emit PermissionAskEvent and block on the
//           reply channel until UI / headless adapter responds.
//   3. Run any user-defined PreToolUse hook (placeholder until
//      hooks/ ships in Phase B).
//   4. Invoke the tool with progress fan-out into the engine's event
//      stream.
//   5. Run PostToolUse hook (placeholder).
//   6. Emit the ToolUseResultEvent and return the payload so the
//      caller can wire it into the next user-turn message.
//
// Cancellation:
//   * ctx cancel while waiting on PermissionAskEvent → returns
//     ctx.Err. The reply chan is abandoned (caller side responsibility
//     to drop).
//   * ctx cancel while tool runs → behavior depends on the tool's
//     InterruptBehavior():
//       "cancel" — pass through to tool ctx, drop result
//       "block"  — let the tool finish, then return its result.

package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/internal/hooks"
	"github.com/biumind/biumind/apps/cli/biu/internal/permissions"
	clauseSettings "github.com/biumind/biumind/apps/cli/biu/internal/settings"
	"github.com/biumind/biumind/apps/cli/biu/internal/state"
	sdkproto "github.com/biumind/biumind/packages/go-sdk/biu/sdkproto/v1"
)

// runnerInput is what the batcher hands to the runner.
type runnerInput struct {
	UseID string // tool_use id from the AssistantMessage block
	Name  string
	Input map[string]any
}

// runnerOutput is the result + bookkeeping. Always non-nil; an
// internal error becomes a soft tool result so the LLM can react.
type runnerOutput struct {
	UseID   string
	Name    string
	Payload ToolResultPayload // always populated
	Elapsed time.Duration
}

// runOne executes a single tool call. Synchronous within the calling
// goroutine — concurrency is the batcher's job.
func (e *QueryEngine) runOne(
	ctx context.Context,
	out chan<- Event,
	in runnerInput,
) runnerOutput {
	started := time.Now()
	done := ctx.Done()

	tool, ok := e.tools.Get(in.Name)
	if !ok {
		// Unknown tool. Fold into a soft tool_result so the LLM can
		// retry with a different tool.
		payload := softError(in.Name, fmt.Sprintf(
			"unknown tool %q (available: %s)",
			in.Name, joinNames(e.tools.List())))
		elapsed := time.Since(started)
		e.recordToolUsage(in.Name, elapsed, payload)
		SafeSend(out, &ToolUseResultEvent{
			ID: in.UseID, Name: in.Name,
			Result: payload, Elapsed: elapsed,
		}, done)
		return runnerOutput{UseID: in.UseID, Name: in.Name,
			Payload: payload, Elapsed: elapsed}
	}

	// Emit start *after* lookup so we don't lie about a tool that
	// doesn't exist.
	SafeSend(out, &ToolUseStartEvent{
		ID: in.UseID, Name: in.Name, Input: in.Input,
	}, done)

	// ── Permission check ─────────────────────────────────────
	answer, permErr := e.askPermission(ctx, out, tool, in)
	if permErr != nil {
		// ctx canceled or chan closed without response. Treat as
		// soft deny — engine's outer loop sees an error event too.
		payload := softError(in.Name, "permission flow aborted: "+permErr.Error())
		elapsed := time.Since(started)
		e.recordToolUsage(in.Name, elapsed, payload)
		SafeSend(out, &ToolUseResultEvent{
			ID: in.UseID, Name: in.Name,
			Result: payload, Elapsed: elapsed,
		}, done)
		return runnerOutput{UseID: in.UseID, Name: in.Name,
			Payload: payload, Elapsed: elapsed}
	}
	if answer.Decision == PermDeny {
		payload := softError(in.Name, "denied by user")
		elapsed := time.Since(started)
		e.recordToolUsage(in.Name, elapsed, payload)
		SafeSend(out, &ToolUseResultEvent{
			ID: in.UseID, Name: in.Name,
			Result: payload, Elapsed: elapsed,
		}, done)
		return runnerOutput{UseID: in.UseID, Name: in.Name,
			Payload: payload, Elapsed: elapsed}
	}
	// User may have edited args before approving.
	input := in.Input
	if answer.UpdatedInput != nil {
		input = answer.UpdatedInput
	}

	// ── PreToolUse hook ─────────────────────────────────────
	// The hook may block the call entirely (exit 2 or
	// {"block":true}); a non-blocking hook may return a Reason that we
	// treat as additional context for the model.
	if e.hooks.Has(hooks.EventPreToolUse) {
		entries := e.hooks.For(hooks.EventPreToolUse, in.Name)
		results := hooks.Run(ctx, entries, hooks.EventPreToolUse, map[string]any{
			"tool_name":   in.Name,
			"tool_input":  input,
			"tool_use_id": in.UseID,
		})
		if blocked := hooks.FirstBlocking(results); blocked != nil {
			payload := softError(in.Name,
				"PreToolUse hook blocked: "+hookReasonOr(blocked, "no reason"))
			elapsed := time.Since(started)
			e.recordToolUsage(in.Name, elapsed, payload)
			SafeSend(out, &ToolUseResultEvent{
				ID: in.UseID, Name: in.Name,
				Result: payload, Elapsed: elapsed,
			}, done)
			return runnerOutput{UseID: in.UseID, Name: in.Name,
				Payload: payload, Elapsed: elapsed}
		}
	}

	// ── Invoke ───────────────────────────────────────────────
	env := &ToolEnv{
		AppState:  e.state,
		AgentID:   e.agentID,
		ToolUseID: in.UseID,
		Cwd:       e.cwd,
		OnProgress: func(p ProgressData) {
			SafeSend(out, &ToolUseProgressEvent{ID: in.UseID, Data: p}, done)
		},
		Spawner: NewSpawner(e, SpawnerOptions{}),
		FileChanged: func(p string) {
			// Forward to the engine-wide LSP hook (was the original use).
			if e.fileChanged != nil {
				e.fileChanged(p)
			}
			// Then fire FileChanged event (P20.55) so users can wire
			// "lint on save" / "auto-format" / "audit log" hooks.
			if e.hooks != nil && e.hooks.Has(hooks.EventFileChanged) {
				hooks.Run(ctx,
					e.hooks.For(hooks.EventFileChanged, in.Name),
					hooks.EventFileChanged,
					map[string]any{
						"session_id": e.agentID,
						"tool_name":  in.Name,
						"path":       p,
					})
			}
		},
		Selections: e.selections,
		FireHook: func(event string, payload map[string]any) {
			if e.hooks == nil {
				return
			}
			ev := hooks.Event(event)
			if !e.hooks.Has(ev) {
				return
			}
			hooks.Run(ctx, e.hooks.For(ev, in.Name), ev, payload)
		},
		SnapshotFile: func(absPath string) error {
			if e.snapshotCapture == nil || e.currentUserUUID == "" {
				return nil
			}
			return e.snapshotCapture(e.currentUserUUID, absPath)
		},
		AskUser: func(askCtx context.Context, q UserQuestion) (UserAnswer, error) {
			respCh := make(chan UserAnswer, 1)
			SafeSend(out, &UserQuestionAskEvent{
				ToolUseID: in.UseID, Question: q, Decision: respCh,
			}, askCtx.Done())
			select {
			case <-askCtx.Done():
				return UserAnswer{}, askCtx.Err()
			case ans := <-respCh:
				return ans, nil
			}
		},
	}

	// Honour InterruptBehavior. "cancel" = pass ctx straight through
	// (default). "block" = give the tool a fresh ctx that doesn't
	// cancel on outer interrupt; the user sees the tool finish.
	toolCtx := ctx
	if tool.InterruptBehavior() == "block" {
		toolCtx = context.Background()
	}

	result, err := tool.Call(toolCtx, input, env)
	elapsed := time.Since(started)

	// ── Map outcome to ToolResultPayload ────────────────────
	var payload ToolResultPayload
	switch {
	case err != nil:
		payload = softError(in.Name, err.Error())
	case result == nil:
		payload = ToolResultPayload{
			Content: []state.ContentBlock{{Type: state.ContentText, Text: ""}},
		}
	default:
		payload = *result
		// Tools may forget to set Content; default to empty text so
		// downstream serialisation doesn't blow up.
		if len(payload.Content) == 0 && payload.SoftError == "" {
			payload.Content = []state.ContentBlock{{Type: state.ContentText, Text: ""}}
		}
	}

	// ── PlanVerifier observation ───────────────────────────
	// Fires before user hooks so the drift count reflects this call
	// when downstream code consults DriftCount().
	if e.planVerifier != nil && !payload.IsError && e.planVerifier.HasPlan() {
		e.planVerifier.Observe(in.Name, input)
	}

	// ── PostToolUse / PostToolUseFailure hook ──────────────
	if e.hooks != nil {
		evt := hooks.EventPostToolUse
		if payload.IsError {
			evt = hooks.EventPostToolUseFailure
		}
		if e.hooks.Has(evt) {
			results := hooks.Run(ctx, e.hooks.For(evt, in.Name), evt,
				map[string]any{
					"tool_name":   in.Name,
					"tool_input":  input,
					"tool_use_id": in.UseID,
					"response":    flattenContent(payload.Content),
					"is_error":    payload.IsError,
				})
			// Post hooks can ask us to surface an error back to the
			// model: convert to soft tool-result wrap.
			if blocked := hooks.FirstBlocking(results); blocked != nil {
				payload = softError(in.Name,
					"PostToolUse hook flagged: "+hookReasonOr(blocked, "no reason"))
			}
		}
	}

	e.recordToolUsage(in.Name, elapsed, payload)

	SafeSend(out, &ToolUseResultEvent{
		ID: in.UseID, Name: in.Name,
		Result: payload, Elapsed: elapsed,
	}, done)
	return runnerOutput{
		UseID: in.UseID, Name: in.Name,
		Payload: payload, Elapsed: elapsed,
	}
}

// recordToolUsage rolls one tool invocation into the per-tool cost
// slice (F4). Called from every runOne exit path — success path AND
// the four early returns (unknown tool / permission abort / user deny /
// hook block). nil-cost tracker is a no-op.
//
// Output size is the byte length of all text content blocks in the
// payload. Non-text blocks contribute zero — image / thinking blocks
// aren't a meaningful "size" signal for the per-tool leaderboard yet.
func (e *QueryEngine) recordToolUsage(name string, elapsed time.Duration, p ToolResultPayload) {
	if e == nil || e.cost == nil {
		return
	}
	bytes := 0
	for _, b := range p.Content {
		bytes += len(b.Text)
	}
	e.cost.AddTool(name, elapsed, bytes, p.IsError)
}

// flattenContent reduces a ContentBlock slice to a plain string for
// hook stdin payloads. Hooks don't need the full structured response.
func flattenContent(blocks []state.ContentBlock) string {
	out := ""
	for _, b := range blocks {
		if b.Type == state.ContentText {
			if out != "" {
				out += "\n"
			}
			out += b.Text
		}
	}
	return out
}

// askPermission handles the allow/deny/ask flow. The permission
// Context (rules + mode + session grants) makes the actual decision —
// this method only translates DecideAsk into a PermissionAskEvent and
// blocks on the user reply.
func (e *QueryEngine) askPermission(
	ctx context.Context,
	out chan<- Event,
	tool Tool,
	in runnerInput,
) (PermissionAnswer, error) {
	req := permissions.Request{
		Tool:          in.Name,
		Args:          in.Input,
		IsReadOnly:    tool.IsReadOnly(in.Input),
		IsDestructive: tool.IsDestructive(in.Input),
	}

	dec, reason := permissions.Decide(e.perms, req)

	// Fire PermissionRequest (P20.55) before we either auto-allow,
	// auto-deny, or escalate to the user. Hook can observe (audit
	// trail) or block via Decision.Block — a blocking hook flips the
	// outcome to Deny regardless of what permissions.Decide said.
	if e.hooks != nil && e.hooks.Has(hooks.EventPermissionRequest) {
		results := hooks.Run(ctx,
			e.hooks.For(hooks.EventPermissionRequest, in.Name),
			hooks.EventPermissionRequest,
			map[string]any{
				"session_id":    e.agentID,
				"tool_name":     in.Name,
				"tool_input":    in.Input,
				"rule_decision": permissionDecisionLabel(dec),
				"reason":        reason.Detail,
			})
		if blocked := hooks.FirstBlocking(results); blocked != nil {
			e.firePermissionDeniedHook(ctx, in.Name, in.Input, "hook",
				hookReasonOr(blocked, "PermissionRequest hook blocked"))
			return PermissionAnswer{Decision: PermDeny},
				fmt.Errorf("PermissionRequest hook blocked: %s",
					hookReasonOr(blocked, "no reason"))
		}
	}

	switch dec {
	case permissions.DecideAllow:
		return PermissionAnswer{Decision: PermAllow}, nil
	case permissions.DecideDeny:
		e.firePermissionDeniedHook(ctx, in.Name, in.Input,
			string(reason.Source), reason.Detail)
		return PermissionAnswer{Decision: PermDeny},
			fmt.Errorf("denied by %s: %s", reason.Source, reason.Detail)
	case permissions.DecideAsk:
		// Optional Warner enrichment: tools that know their input is
		// destructive (Bash with `rm -rf`, etc.) get to surface a
		// human-readable warning into the dialog. Type-assert so
		// non-Warner tools pay zero cost.
		if w, ok := tool.(Warner); ok {
			if notes := w.Warnings(in.Input); len(notes) > 0 {
				if reason.Detail != "" {
					reason.Detail += "\n"
				}
				for i, n := range notes {
					if i > 0 {
						reason.Detail += "\n"
					}
					reason.Detail += "Note: " + n
				}
			}
		}
		// Build "Allow + side-effect" suggestions the UI can render
		// alongside the bare allow/deny choices. See
		// generateAskSuggestions for which reasons currently produce
		// suggestions; an empty slice is a normal outcome (most asks
		// don't need a shortcut).
		suggestions := generateAskSuggestions(in, reason, e.perms)
		return e.interactiveAsk(ctx, out, in, reason, suggestions)
	}

	// Defensive: unreachable.
	return PermissionAnswer{Decision: PermDeny},
		fmt.Errorf("unknown permission decision")
}

// interactiveAsk emits a PermissionAskEvent and blocks on the user's
// reply. On approval with Remember=true it persists the grant into the
// permission context (session-scoped) so subsequent identical calls
// auto-allow. AppliedUpdates the user picked from the event's
// Suggestions are folded back into the runtime ctx (and optionally
// the on-disk settings.json) before returning Allow.
func (e *QueryEngine) interactiveAsk(
	ctx context.Context,
	out chan<- Event,
	in runnerInput,
	reason permissions.Reason,
	suggestions []AskSuggestion,
) (PermissionAnswer, error) {
	respCh := make(chan PermissionAnswer, 1)

	SafeSend(out, &PermissionAskEvent{
		ToolUseID:   in.UseID,
		ToolName:    in.Name,
		Input:       in.Input,
		Reason:      reason.Detail,
		Decision:    respCh,
		Suggestions: suggestions,
	}, ctx.Done())

	select {
	case <-ctx.Done():
		return PermissionAnswer{Decision: PermDeny}, ctx.Err()
	case ans, ok := <-respCh:
		if !ok {
			return PermissionAnswer{Decision: PermDeny},
				fmt.Errorf("permission channel closed")
		}
		if ans.Decision == PermDeny {
			e.firePermissionDeniedHook(ctx, in.Name, in.Input,
				"user", reason.Detail)
		}
		if ans.Remember && ans.Decision == PermAllow {
			e.perms.Grant(permissions.SessionGrantKey(in.Name, in.Input))
			// Mirror the legacy state.GrantPermission so any code
			// still reading from AppState (e.g. older tools) keeps
			// working until those callers migrate.
			e.state.GrantPermission(permissionKey(in.Name, in.Input))
		}
		// Apply user-picked suggestions on Allow. Each suggestion
		// already carries its destination, so persistence (when
		// applicable) goes through the standard PersistPermissionUpdate
		// path. Errors are non-fatal: a failed apply / persist
		// surfaces on stderr but does NOT veto the Allow decision —
		// the user already said yes; suggestions are applied
		// optimistically.
		if ans.Decision == PermAllow && len(ans.AppliedUpdates) > 0 {
			cwd := ""
			if e.state != nil {
				cwd = e.state.OriginalCwd
			}
			for _, u := range ans.AppliedUpdates {
				if err := permissions.ApplyPermissionUpdate(e.perms, u); err != nil {
					fmt.Fprintf(os.Stderr,
						"[biu] permission suggestion apply failed: %v\n", err)
					continue
				}
				if clauseSettings.SupportsPersistence(destinationOf(u)) {
					if err := clauseSettings.PersistPermissionUpdate(cwd, u); err != nil {
						fmt.Fprintf(os.Stderr,
							"[biu] permission suggestion persist failed: %v\n", err)
					}
				}
			}
		}
		return ans, nil
	}
}

// destinationOf extracts the Destination string from any of the
// sdkproto PermissionUpdate variants. Returns "" for variants
// without a Destination field (none today; defensive).
func destinationOf(u sdkproto.PermissionUpdate) string {
	switch v := u.(type) {
	case *sdkproto.AddDirectories:
		return v.Destination
	case *sdkproto.RemoveDirectories:
		return v.Destination
	case *sdkproto.AddRules:
		return v.Destination
	case *sdkproto.ReplaceRules:
		return v.Destination
	case *sdkproto.RemoveRules:
		return v.Destination
	case *sdkproto.SetModeUpdate:
		return v.Destination
	}
	return ""
}

// generateAskSuggestions inspects the (input, reason, ctx) tuple and
// returns the actionable shortcuts the UI should render alongside
// the bare allow/deny choices.
//
// Currently emits one suggestion per case:
//
//   - reason.Kind == "workingDir" — propose adding the parent
//     directory of the path argument as a session-source working
//     dir. Lets users widen the model's reach without leaving the
//     dialog ("Yes, allow all reads in <dirName>/ during this
//     session").
//
// Future suggestions (deferred until a clear need): setMode
// acceptEdits when in default/plan; narrowed `.claude/skills/X/**`
// allow rules.
func generateAskSuggestions(in runnerInput, reason permissions.Reason, ctx *permissions.Context) []AskSuggestion {
	if reason.Kind != "workingDir" {
		return nil
	}
	path := pathFromArgs(in.Input)
	if path == "" {
		return nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil
	}
	dir := filepath.Dir(abs)
	if dir == "" || dir == abs {
		return nil
	}
	// If the parent dir is already covered (e.g. user cd'd into a
	// subdir of an existing working dir mid-session), don't bother
	// showing the suggestion — the apply would be a no-op.
	if ctx != nil {
		originalCwd := ctx.OriginalCwd()
		if permissions.PathInAllowedWorkingPath(dir, ctx, originalCwd) {
			return nil
		}
	}
	return []AskSuggestion{
		{
			Label:  fmt.Sprintf("Allow + add %s/ to working dirs (this session)", filepath.Base(dir)),
			HotKey: "w",
			Update: &sdkproto.AddDirectories{
				Type:        sdkproto.PermissionUpdateAddDirectories,
				Directories: []string{dir},
				Destination: sdkproto.PermissionDestSession,
			},
		},
	}
}

// pathFromArgs picks the first plausible path field from a tool's
// input. Mirrors the field-search order permissions.pathOutsideWorkingDirs
// uses so the suggestion lines up with the gate that triggered it.
func pathFromArgs(input map[string]any) string {
	for _, k := range []string{"file_path", "path", "pattern", "notebook_path"} {
		if s, ok := input[k].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

// permissionDecisionLabel maps the int-iota Decision to a stable
// string for hook payloads. Direct string(int) would emit a single
// rune ("\x00") — not what an audit log wants.
func permissionDecisionLabel(d permissions.Decision) string {
	switch d {
	case permissions.DecideAllow:
		return "allow"
	case permissions.DecideAsk:
		return "ask"
	case permissions.DecideDeny:
		return "deny"
	default:
		return "unknown"
	}
}

// firePermissionDeniedHook centralises the EventPermissionDenied
// dispatch so all three denial sources (rule deny, hook block,
// user "no") emit the same payload shape (P20.55).
func (e *QueryEngine) firePermissionDeniedHook(ctx context.Context, name string, input map[string]any, source, reason string) {
	if e == nil || e.hooks == nil {
		return
	}
	if !e.hooks.Has(hooks.EventPermissionDenied) {
		return
	}
	hooks.Run(ctx,
		e.hooks.For(hooks.EventPermissionDenied, name),
		hooks.EventPermissionDenied,
		map[string]any{
			"session_id":    e.agentID,
			"tool_name":     name,
			"tool_input":    input,
			"denial_source": source,
			"reason":        reason,
		})
}

// permissionKey is a stable identifier for the (tool, args) tuple used
// to memoize "always-allow this thing" within a session. Conservative
// design: include the tool name + the most-meaningful arg (e.g.
// command for Bash, path for Edit). Keeps the cache small.
func permissionKey(name string, input map[string]any) string {
	// Hot args — these are what the user is approving in the prompt.
	switch name {
	case "Bash":
		if cmd, ok := input["command"].(string); ok {
			return name + ":" + cmd
		}
	case "Edit", "Write":
		if p, ok := input["path"].(string); ok {
			return name + ":" + p
		}
	}
	return name
}

// softError wraps an error message in a tool_result-shaped payload so
// the LLM sees it and can retry rather than the engine bailing out.
func softError(toolName, msg string) ToolResultPayload {
	return ToolResultPayload{
		Content: []state.ContentBlock{{
			Type: state.ContentText,
			Text: fmt.Sprintf("error in %s: %s", toolName, msg),
		}},
		IsError:   true,
		SoftError: msg,
	}
}

func joinNames(ts []Tool) string {
	if len(ts) == 0 {
		return "(none)"
	}
	out := ts[0].Name()
	for _, t := range ts[1:] {
		out += ", " + t.Name()
	}
	return out
}
