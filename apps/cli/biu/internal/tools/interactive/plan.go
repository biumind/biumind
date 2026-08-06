// EnterPlanMode / ExitPlanMode — flip the engine's permission context
// into / out of plan mode (read-only browsing).
//
// Plan mode is a soft contract with the model: "keep researching, but
// don't write." The permission context enforces it (plan mode denies
// every non-read-only call), so the LLM can't accidentally edit.
//
// ExitPlanMode is the explicit "I have a plan, please approve" gate.
// Three things happen on exit:
//
//   1. The plan markdown is persisted to disk so callers can reference
//      it later (`biu sessions show`, `--resume`, post-mortem review).
//   2. The mode is restored to whatever was active BEFORE EnterPlanMode
//      ran (via permissions.Context.ExitPlanMode), not blindly default.
//   3. Any `allowedPrompts` the model proposed are pre-staged as
//      session grants — when the user authorises execution on the next
//      turn, those tools run without prompting one-by-one.
//
// `allowedPrompts` is the "batch approval" feature: instead of asking
// for each shell command individually, the model declares the
// categories of action up front ("run tests", "install deps") and the
// user accepts the whole bundle.

package interactive

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
	"github.com/biumind/biumind/apps/cli/biu/internal/permissions"
	"github.com/biumind/biumind/apps/cli/biu/internal/state"
)

// PermissionsAccessor is the tiny surface plan tools need: read +
// write the active mode + push session grants.
//
// We don't import the engine type directly because that would require
// a parent reference on every ToolEnv. Each method maps onto a real
// permissions.Context method.
type PermissionsAccessor interface {
	Mode() permissions.Mode
	SetMode(permissions.Mode)
	EnterPlanMode() permissions.Mode
	ExitPlanMode() permissions.Mode
	Grant(key string)
	// SetPlanAttachment marks the session as carrying a plan that
	// must survive compact runs. ExitPlanMode calls this with the
	// approved plan body so the compactor can re-inject it. The
	// permissions.Context impl is the only production caller; tests
	// can supply a stub.
	SetPlanAttachment(plan string)
	// AddAllowedPrompts stages semantic batch approvals so the runner
	// can auto-allow tool calls that match the approved prompts via
	// the classifier. The permissions.Context impl persists these for
	// the lifetime of the session.
	AddAllowedPrompts(prompts []permissions.AllowedPrompt)
}

// PlanStore persists plans to disk so they survive across compact /
// resume. Optional — the tool falls back to in-memory only when nil.
//
// The default implementation (DiskPlanStore) writes to
// ~/.biu/plans/<session-id>.md.
type PlanStore interface {
	WritePlan(sessionID, plan string) (path string, err error)
}

// EnterPlanModeTool flips the active mode to plan. The model uses
// this when it's about to do extensive research that shouldn't
// accidentally mutate state.
type EnterPlanModeTool struct {
	Perms PermissionsAccessor
}

func (EnterPlanModeTool) Name() string { return "EnterPlanMode" }

func (EnterPlanModeTool) Description(_ map[string]any) string {
	return "Switch the session into plan mode (read-only browsing). " +
		"Use before extensive research; pair with ExitPlanMode when " +
		"you have a concrete plan to propose. The mode you were in " +
		"before this call is restored automatically when ExitPlanMode " +
		"runs."
}

func (EnterPlanModeTool) InputSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

func (EnterPlanModeTool) IsReadOnly(_ map[string]any) bool        { return false }
func (EnterPlanModeTool) IsDestructive(_ map[string]any) bool     { return false }
func (EnterPlanModeTool) IsConcurrencySafe(_ map[string]any) bool { return false }
func (EnterPlanModeTool) InterruptBehavior() string               { return "cancel" }

func (e EnterPlanModeTool) Call(_ context.Context, _ map[string]any, _ *engine.ToolEnv) (*engine.ToolResultPayload, error) {
	if e.Perms == nil {
		return softErr("EnterPlanMode", "no permissions context"), nil
	}
	prev := e.Perms.EnterPlanMode()
	msg := "Plan mode active. All write operations are now denied."
	if prev != "" && prev != permissions.ModePlan {
		msg += " Will restore to " + string(prev) + " on ExitPlanMode."
	}
	return text(msg), nil
}

// AllowedPrompt is one batch-approval entry the model proposes. The
// user's confirmation of ExitPlanMode implicitly approves these too,
// staging them as session grants so the runner doesn't ask again
// when the model executes the plan.
//
// Today we only honour `tool: "Bash"`; the schema field is there so
// the model can tell us what category each pre-approval covers, even
// though the runner today keys on tool name.
type AllowedPrompt struct {
	Tool   string `json:"tool"`
	Prompt string `json:"prompt"`
}

// ExitPlanModeTool announces a finished plan and reverts to the mode
// that was active before EnterPlanMode. The plan content lives in
// input.plan for the user to review.
type ExitPlanModeTool struct {
	Perms     PermissionsAccessor
	PlanStore PlanStore
	// SessionID is used as the plan file basename. Empty string is
	// fine — the store will fall back to a timestamp.
	SessionID string
}

func (ExitPlanModeTool) Name() string { return "ExitPlanMode" }

func (ExitPlanModeTool) Description(_ map[string]any) string {
	return "Present a finished plan for user review and exit plan mode. " +
		"Pass the full plan as `plan` (markdown). Optionally pass " +
		"`allowedPrompts` to batch-request execution permissions for " +
		"common categories of action (e.g. `[{tool:\"Bash\", prompt:\"run tests\"}]`); " +
		"approving the plan also approves those calls so you don't " +
		"prompt one-by-one. The mode active before EnterPlanMode is " +
		"restored automatically."
}

func (ExitPlanModeTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"plan": map[string]any{
				"type":        "string",
				"description": "The complete plan as markdown.",
			},
			"allowedPrompts": map[string]any{
				"type": "array",
				"description": "Batch-approval requests covering categories of action that the plan will require. Approving the plan auto-approves these calls.",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"tool": map[string]any{
							"type": "string",
							"enum": []string{"Bash"},
							"description": "The tool this pre-approval applies to (currently only Bash).",
						},
						"prompt": map[string]any{
							"type":        "string",
							"description": "Semantic description of what the tool will do (e.g. \"run tests\", \"install dependencies\").",
						},
					},
					"required": []string{"tool", "prompt"},
				},
			},
		},
		"required": []string{"plan"},
	}
}

func (ExitPlanModeTool) IsReadOnly(_ map[string]any) bool        { return false }
func (ExitPlanModeTool) IsDestructive(_ map[string]any) bool     { return false }
func (ExitPlanModeTool) IsConcurrencySafe(_ map[string]any) bool { return false }
func (ExitPlanModeTool) InterruptBehavior() string               { return "cancel" }

func (e ExitPlanModeTool) Call(_ context.Context, input map[string]any, _ *engine.ToolEnv) (*engine.ToolResultPayload, error) {
	if e.Perms == nil {
		return softErr("ExitPlanMode", "no permissions context"), nil
	}

	// Validation: refuse to run when not in plan mode. This stops the
	// model from "exiting" plan mode it was never in, which would
	// silently switch the session into the prePlanMode fallback
	// (default).
	if e.Perms.Mode() != permissions.ModePlan {
		return softErr("ExitPlanMode",
			"not currently in plan mode — call EnterPlanMode first or skip this tool"), nil
	}

	plan, _ := input["plan"].(string)
	if strings.TrimSpace(plan) == "" {
		return softErr("ExitPlanMode", "plan is required"), nil
	}

	// Pre-stage approvals for any batch-approved prompts. Two layers:
	//
	//   1. Exact-match SessionGrant — fires when the model literally
	//      proposes a command equal to the prompt string (the legacy
	//      behaviour we keep for cases where the prompt happens to be
	//      a command).
	//   2. Semantic AllowedPrompt — Decide consults the prompt
	//      classifier so commands semantically covered by the prompt
	//      ("run tests" → "go test ./...") auto-allow without
	//      prompting. The user implicitly approved these when they
	//      accepted ExitPlanMode.
	//
	// Both run before mode-restore so the next turn (executing under
	// the restored mode) sees them already cached.
	prompts := parseAllowedPrompts(input["allowedPrompts"])
	semantic := make([]permissions.AllowedPrompt, 0, len(prompts))
	for _, p := range prompts {
		key := permissions.SessionGrantKey(p.Tool, map[string]any{
			"command": p.Prompt,
		})
		e.Perms.Grant(key)
		semantic = append(semantic, permissions.AllowedPrompt{
			Tool: p.Tool, Prompt: p.Prompt,
		})
	}
	e.Perms.AddAllowedPrompts(semantic)

	// Persist the plan so resume / post-mortem can reference it.
	// Failure is non-fatal — the plan is still in the message
	// transcript, the disk copy is a convenience.
	planPath := ""
	if e.PlanStore != nil {
		if path, err := e.PlanStore.WritePlan(e.SessionID, plan); err == nil {
			planPath = path
		}
	}

	// Mark the session as carrying this plan so the compactor re-
	// injects it as a system attachment if the conversation grows
	// long enough to compact. Done BEFORE ExitPlanMode in case the
	// caller's mode-restore reaction observes the attachment field.
	e.Perms.SetPlanAttachment(plan)

	// Restore the mode that was active before EnterPlanMode, NOT
	// always default.
	restored := e.Perms.ExitPlanMode()

	var b strings.Builder
	b.WriteString("Plan presented. Mode restored to ")
	b.WriteString(string(restored))
	b.WriteString(".\n\n")
	if planPath != "" {
		b.WriteString("Plan saved to: ")
		b.WriteString(planPath)
		b.WriteString("\n\n")
	}
	if len(prompts) > 0 {
		b.WriteString("Pre-approved on plan acceptance:\n")
		for _, p := range prompts {
			b.WriteString("  - ")
			b.WriteString(p.Tool)
			b.WriteString(": ")
			b.WriteString(p.Prompt)
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	b.WriteString("## Plan\n\n")
	b.WriteString(plan)
	return text(b.String()), nil
}

// parseAllowedPrompts coerces the loose JSON shape the model
// produces into a typed slice. Tolerant: invalid entries are
// dropped silently rather than failing the whole tool.
func parseAllowedPrompts(raw any) []AllowedPrompt {
	arr, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]AllowedPrompt, 0, len(arr))
	for _, item := range arr {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		tool, _ := m["tool"].(string)
		prompt, _ := m["prompt"].(string)
		if tool == "" || prompt == "" {
			continue
		}
		out = append(out, AllowedPrompt{Tool: tool, Prompt: prompt})
	}
	return out
}

// ─── DiskPlanStore — the default persistence backend ─────

// DiskPlanStore writes plan files to ~/.biu/plans/<session>.md. We
// use the session ID instead of a word slug so plans collate next to
// their session log.
type DiskPlanStore struct {
	// Dir overrides the default ~/.biu/plans path. Empty = default.
	Dir string
}

// NewDiskPlanStore returns a store rooted at $HOME/.biu/plans (or
// the supplied override). The directory is created lazily on first
// WritePlan call.
func NewDiskPlanStore(dirOverride string) *DiskPlanStore {
	return &DiskPlanStore{Dir: dirOverride}
}

func (d *DiskPlanStore) WritePlan(sessionID, plan string) (string, error) {
	dir, err := d.resolveDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	name := sessionID
	if name == "" {
		// Fall back to a timestamp when no session is wired up. Same
		// granularity the session writer uses so the two collate.
		name = "plan-" + time.Now().UTC().Format("20060102-150405")
	}
	path := filepath.Join(dir, name+".md")
	body := planFileBody(plan)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func (d *DiskPlanStore) resolveDir() (string, error) {
	if d.Dir != "" {
		return d.Dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".biu", "plans"), nil
}

// planFileBody adds a tiny frontmatter header so a plan file is
// self-describing — matches the markdown style biu's `/init` writes
// for BIUMIND.md.
func planFileBody(plan string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "<!-- biu plan, written %s -->\n\n", time.Now().UTC().Format(time.RFC3339))
	b.WriteString(strings.TrimSpace(plan))
	b.WriteString("\n")
	return b.String()
}

// helpers — kept private so the package surface stays tight.

func softErr(name, msg string) *engine.ToolResultPayload {
	return &engine.ToolResultPayload{
		Content: []state.ContentBlock{{
			Type: state.ContentText,
			Text: name + " error: " + msg,
		}},
		IsError: true, SoftError: msg,
	}
}

func text(s string) *engine.ToolResultPayload {
	return &engine.ToolResultPayload{
		Content: []state.ContentBlock{{Type: state.ContentText, Text: s}},
	}
}
