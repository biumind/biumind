// /agents slash + scaffold. List registered sub-agent types and
// scaffold new ones via `agents.Scaffold`.

package repl

import (
	"fmt"
	"os"
	"strings"

	"github.com/biumind/biumind/apps/cli/biu/internal/agents"
	"github.com/biumind/biumind/apps/cli/biu/internal/plugins"
)

// handleAgents dispatches /agents subcommands.
//
//	/agents                              — list registered types
//	/agents create <name> [flags]        — scaffold a new agent .md
//
// Create flags:
//
//	--scope user|project   (default: user → ~/.biumind/agents/)
//	--from <preset>        (default | explore | review | verify | plan)
//	--force                overwrite an existing file
//
// The list-only path re-reads the registry every call so a fresh
// scaffold shows up without restart (modulo `/memory reload`-style
// re-pickup for engine-side caching).
func (m model) handleAgents(parts []string) string {
	if len(parts) > 1 && parts[1] == "create" {
		return m.handleAgentsCreate(parts[2:])
	}
	return m.handleAgentsList()
}

// handleAgentsList renders the existing /agents output.
func (m model) handleAgentsList() string {
	cwd, _ := os.Getwd()
	reg, err := agents.Load(cwd)
	if err != nil {
		return "/agents: " + err.Error()
	}
	// Layer plugin-contributed agents on top so /agents reflects
	// what the engine's AgentTool will actually dispatch against.
	plugins.LoadAll(plugins.DefaultRoots(cwd), nil).AttachAgents(reg)
	all := reg.All()
	var b strings.Builder
	b.WriteString("registered sub-agent types:\n")
	b.WriteString("  general-purpose — fallback (parent catalog, no override)\n")
	for _, a := range all {
		fmt.Fprintf(&b, "  %s [%s] — %s\n",
			a.Name, a.Source, oneLineSlash(a.Description))
	}
	if len(all) == 0 {
		b.WriteString("  (no custom agents — `/agents create <name>` to scaffold one)\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// handleAgentsCreate parses the flags + invokes agents.Scaffold.
// Stateless flag parsing — same shape as /init. Returns a status
// note for the system message stream.
func (m model) handleAgentsCreate(args []string) string {
	if len(args) == 0 {
		return "/agents create: usage: /agents create <name> " +
			"[--scope user|project] [--from <preset>] [--force]"
	}
	opt := agents.ScaffoldOptions{Name: args[0]}
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--force", "-f":
			opt.Force = true
		case "--scope":
			if i+1 >= len(args) {
				return "/agents create: --scope requires user|project"
			}
			i++
			opt.Scope = agents.ScaffoldScope(args[i])
		case "--from":
			if i+1 >= len(args) {
				return "/agents create: --from requires a preset name"
			}
			i++
			opt.Preset = args[i]
		default:
			return "/agents create: unknown flag " + args[i]
		}
	}
	if opt.Scope == agents.ScopeProject {
		cwd, err := os.Getwd()
		if err != nil {
			return "/agents create: cannot resolve cwd: " + err.Error()
		}
		opt.Cwd = cwd
	}

	res, err := agents.Scaffold(opt)
	if err != nil {
		return "/agents create: " + err.Error()
	}
	verb := "wrote"
	if res.Overwritten {
		verb = "overwrote"
	}
	return fmt.Sprintf("/agents: %s %s (preset=%s). "+
		"Edit the file then re-run /agents (or restart biu) to pick it up.",
		verb, res.Path, res.UsedPreset)
}
