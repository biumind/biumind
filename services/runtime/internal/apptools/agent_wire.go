// Adapters that bridge apptools (this package) into the agent loop's
// indirection-by-function-pointer pattern.
//
// agent.AppToolDeps holds three func fields (LoadFn / RegisterFn /
// PromptFn) so agent.go doesn't import apptools — apptools imports
// agent (for the Tool / Registry types), and the reverse import
// would be a cycle. The wrappers below match the signatures
// agent.AppToolDeps expects and forward to the typed apptools
// functions.

package apptools

import (
	"context"

	"github.com/biumind/biumind/services/runtime/internal/agent"
	"github.com/google/uuid"
)

// MakeAgentDeps wires this package into agent.AppToolDeps. Pass the
// per-run AgentID + OrgID; the returned AppToolDeps captures the
// loader / registry / recorder for use during agent.Run().
//
// Example call site (in services/runtime/internal/api/handle_run):
//
//	agent.RunInput{
//	    Apps: apptools.MakeAgentDeps(loader, biureg, recorder, authz,
//	                                 agentID, orgID),
//	    ...
//	}
func MakeAgentDeps(loader *Loader, deps ToolDeps, agentID uuid.UUID, orgID string) *agent.AppToolDeps {
	return &agent.AppToolDeps{
		AgentID: agentID,
		OrgID:   orgID,
		LoadFn: func(ctx context.Context, userID, agentID uuid.UUID, orgID string) (agent.AppsLoaded, error) {
			loaded, err := loader.LoadForAgent(ctx, LoadInput{
				UserID:  userID,
				OrgID:   orgID,
				AgentID: agentID,
			})
			if err != nil {
				return nil, err
			}
			return loaded, nil
		},
		RegisterFn: func(reg *agent.Registry, loaded agent.AppsLoaded) int {
			l, ok := loaded.(*Loaded)
			if !ok || l == nil {
				return 0
			}
			return RegisterTools(reg, l, deps)
		},
		PromptFn: func(loaded agent.AppsLoaded) string {
			l, ok := loaded.(*Loaded)
			if !ok || l == nil {
				return ""
			}
			return BuildSystemPromptBlock(l)
		},
	}
}
