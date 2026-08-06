// Convenience installer: register every orchestration tool onto an
// engine.SimpleRegistry (or any *SimpleRegistry-typed registry).
//
// Use from main.go after building the legacy tool registry, before
// constructing the QueryEngine.

package orchestration

import "github.com/biumind/biumind/apps/cli/biu/internal/engine"

// Options controls optional wiring. Zero value is fine for tests
// that don't care about per-type sub-agents.
type Options struct {
	// Agents is the file-loaded sub-agent registry. nil = AgentTool
	// runs in fallback mode (every type maps to general-purpose).
	Agents AgentRegistry

	// Teams + Messages plumb the swarm-tool dependencies through
	// (P20.53-2). When both nil the team tools soft-error gracefully;
	// production wiring sets them from QueryEngine.Teams() /
	// TeamMessages().
	Teams    *engine.TeamRegistry
	Messages *engine.MessageInbox
}

// All returns every orchestration tool with the supplied options.
// Pass a zero Options{} for the legacy "no sub-agent registry" mode.
//
// ToolSearch is intentionally NOT in this list — it needs a reference
// to the live registry, which only Register has (the registry the
// model dispatches against). Callers using All() directly can append
// ToolSearchTool{Registry: reg} themselves once their registry is
// fully populated.
func All(opt Options) []engine.Tool {
	return []engine.Tool{
		TodoWriteTool{},
		TaskCreateTool{},
		TaskListTool{},
		TaskGetTool{},
		TaskUpdateTool{},
		AgentTool{Registry: opt.Agents},
		AgentBackgroundTool{Registry: opt.Agents, Teams: opt.Teams},
		TeamCreateTool{Teams: opt.Teams},
		TeamDeleteTool{Teams: opt.Teams},
		SendMessageTool{Teams: opt.Teams, Messages: opt.Messages},
	}
}

// Register adds every orchestration tool to reg, including
// ToolSearchTool wired to that same registry. Call this AFTER all
// other tool packages have registered their tools — ToolSearch
// scans the registry for deferred tools at call time, so a
// late-arriving deferred tool is fine, but the search scope only
// covers tools the registry knows about.
func Register(reg *engine.SimpleRegistry, opt ...Options) {
	o := Options{}
	if len(opt) > 0 {
		o = opt[0]
	}
	for _, t := range All(o) {
		reg.Register(t)
	}
	reg.Register(ToolSearchTool{Registry: reg})
	reg.Register(REPLTool{Registry: reg})
}
