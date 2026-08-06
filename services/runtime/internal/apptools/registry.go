// Bind LoadedApp action specs into the Runtime agent.Tool fleet.
//
// Each manifest action becomes one Tool. Naming convention:
//
//	<identifier>.<action>     e.g. "rss.fetch", "email.draft"
//
// Risk maps from the manifest's ActionRisk into agent.Risk. Authz +
// invocations audit are folded into the Invoke closure so the agent
// loop's PermissionMode gate stays the only thing it knows about.

package apptools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/biumind/biumind/packages/go-sdk/biu/biuapp"
	"github.com/biumind/biumind/services/runtime/internal/agent"
)

// ToolDeps captures what the bound Invoke closure needs at call time.
// Decoupled from Loader so tests can plug a fake authz / fake invoker
// without touching DB.
type ToolDeps struct {
	// Registry is the in-process biuapp.Registry that holds the App
	// implementations. The bound Invoke closure routes via this.
	Registry *biuapp.Registry

	// Authz, when non-nil, is consulted before each invoke. Returns
	// nil to allow, error to deny. Production wires the central Authz
	// HTTP client; tests pass a stub.
	Authz AuthChecker

	// Recorder, when non-nil, writes one row to app_center.invocations
	// per call (status / duration / tokens). Production wires a
	// pgxpool-backed recorder; tests pass nil and assert on the
	// returned (action, install) pair via a custom hook.
	Recorder InvocationRecorder
}

// AuthChecker is the slice of central Authz our Tool closures need.
// Mirrors services/runtime/internal/authz.Decider but defined here
// to avoid a circular import between agent and apptools.
type AuthChecker interface {
	Check(ctx context.Context, installID, action string, principal map[string]any, resource map[string]any) error
}

// InvocationRecorder writes a row to app_center.invocations after a
// tool call completes (success or error).
type InvocationRecorder interface {
	Record(ctx context.Context, inv InvocationRecord) error
}

// InvocationRecord is the audit row written per call. Mirrors the
// columns in the app_center.invocations table 1:1.
type InvocationRecord struct {
	InstallID  string
	AppID      string
	Identifier string
	Action     string
	Caller     string // "user" | "agent"
	CallerID   string
	TraceID    string
	DurationMs int
	Status     string // "ok" | "error" | "denied" | "timeout"
	ErrorCode  string
}

// RegisterTools binds every (LoadedApp × Action) pair into reg as one
// agent.Tool. Returns the count of tools added so callers can log /
// emit metrics. Existing reg entries are left untouched — call sites
// are expected to clone the base Registry (per agent.go does for
// memory + skills) before passing it here.
func RegisterTools(reg *agent.Registry, loaded *Loaded, deps ToolDeps) int {
	if reg == nil || loaded == nil {
		return 0
	}
	count := 0
	for _, la := range loaded.Apps {
		for _, action := range la.AvailableActions {
			tool := buildTool(la, action, deps)
			reg.Register(tool)
			count++
		}
	}
	return count
}

func buildTool(la LoadedApp, action biuapp.ActionSpec, deps ToolDeps) *agent.Tool {
	name := la.Identifier + "." + action.Name

	// Risk mapping: manifest ActionRisk ("low"/"medium"/"high") into
	// runtime agent.Risk. Default to RiskMedium when unset so an App
	// author who forgot to declare risk doesn't accidentally land in
	// the auto-allow bucket.
	risk := agent.RiskMedium
	switch action.Risk {
	case biuapp.RiskLow:
		risk = agent.RiskLow
	case biuapp.RiskHigh:
		risk = agent.RiskHigh
	}

	// JSON Schema for the model. Pass through as-is — biuapp validator
	// already checked it's well-formed.
	params := action.InputSchema
	if params == nil {
		params = map[string]any{"type": "object"}
	}

	// Snapshot identity into closure variables. The agent loop spins
	// many goroutines per run; we want each Tool's Invoke to be safe
	// to call concurrently without sharing mutable state.
	installID := la.InstallID
	identifier := la.Identifier
	appID := la.InstallID // M3.6: rewire to apps.id once catalogue write lands
	actionName := action.Name

	return &agent.Tool{
		Name:        name,
		Description: action.Description,
		Parameters:  params,
		Risk:        risk,
		IsReadOnly:  risk == agent.RiskLow,
		Invoke: func(ctx context.Context, args map[string]any) (string, error) {
			// 1. Authz (if wired) — fail-closed.
			if deps.Authz != nil {
				if err := deps.Authz.Check(ctx, installID, "app:invoke",
					nil, /* principal filled by caller via context */
					map[string]any{
						"id":         installID,
						"identifier": identifier,
						"enabled":    true, // loader filtered to enabled only
					},
				); err != nil {
					recordOutcome(ctx, deps, installID, appID, identifier, actionName, "denied", err.Error(), 0)
					return "", fmt.Errorf("apptools: authz denied %s: %w", name, err)
				}
			}

			// 2. Marshal args → json.RawMessage for biuapp.Invoke.
			raw, err := json.Marshal(args)
			if err != nil {
				recordOutcome(ctx, deps, installID, appID, identifier, actionName, "error", "marshal", 0)
				return "", fmt.Errorf("apptools: marshal args: %w", err)
			}

			// 3. Dispatch via biuapp.Registry. The registry already
			// validates the action exists in manifest.actions[]; on
			// unknown action it errors cleanly.
			out, err := deps.Registry.Invoke(ctx, identifier, actionName, raw)
			if err != nil {
				recordOutcome(ctx, deps, installID, appID, identifier, actionName, "error", err.Error(), 0)
				return "", fmt.Errorf("apptools: invoke %s: %w", name, err)
			}

			// 4. Stringify the output. The model-relay stream layer expects a
			// string body; tools that return JSON-shaped data simply
			// re-encode the output map. This matches the existing
			// runtime tool fleet's contract.
			outBytes, err := json.Marshal(out)
			if err != nil {
				recordOutcome(ctx, deps, installID, appID, identifier, actionName, "error", "marshal_out", 0)
				return "", fmt.Errorf("apptools: marshal result: %w", err)
			}
			recordOutcome(ctx, deps, installID, appID, identifier, actionName, "ok", "", 0)
			return string(outBytes), nil
		},
	}
}

// recordOutcome is a best-effort wrapper around the optional
// recorder. Failures to record do NOT propagate — auditing is
// secondary to the call itself and shouldn't break the agent loop.
func recordOutcome(ctx context.Context, deps ToolDeps, installID, appID, identifier, action, status, errCode string, durationMs int) {
	if deps.Recorder == nil {
		return
	}
	_ = deps.Recorder.Record(ctx, InvocationRecord{
		InstallID:  installID,
		AppID:      appID,
		Identifier: identifier,
		Action:     action,
		Caller:     "agent",
		Status:     status,
		ErrorCode:  errCode,
		DurationMs: durationMs,
	})
}
