// User-defined slash command lookup + dispatch. The default-case
// fallthrough in runSlash calls lookupUserCommand; on hit the
// rendered body goes through the engine via runUserCommand.

package repl

import (
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/biumind/biumind/apps/cli/biu/internal/client"
	"github.com/biumind/biumind/apps/cli/biu/internal/commands"
	"github.com/biumind/biumind/apps/cli/biu/internal/plugins"
	"github.com/biumind/biumind/apps/cli/biu/internal/session"
)

// loadCommandsWithPlugins is the canonical loader for the REPL's
// slash dispatcher: user/project files first, plugin contributions
// second. Stateless on purpose — the dropdown re-renders on every
// keystroke and the dispatcher re-runs on every line, so a fresh
// load picks up plugin install / uninstall without restart.
//
// Errors from plugin loading are dropped here (user can see them
// via /plugin list); a malformed plugin shouldn't make /<command>
// stop working.
func loadCommandsWithPlugins(cwd string) (*commands.Registry, error) {
	reg, err := commands.Load(cwd)
	if err != nil {
		return nil, err
	}
	plugins.LoadAll(plugins.DefaultRoots(cwd), nil).AttachCommands(reg)
	return reg, nil
}

// userSlashItems materialises a SlashCmd row for every command
// loaded from ~/.biumind/commands/ + the project layer. Returns nil
// when none are configured — the matcher then renders only the
// built-in catalog. Loaded fresh on every dropdown filter so users
// editing files mid-session see updates immediately.
func (m model) userSlashItems() []SlashCmd {
	cwd, _ := os.Getwd()
	reg, err := loadCommandsWithPlugins(cwd)
	if err != nil || reg == nil {
		return nil
	}
	all := reg.All()
	if len(all) == 0 {
		return nil
	}
	out := make([]SlashCmd, 0, len(all))
	for _, c := range all {
		desc := c.Description
		if desc == "" {
			desc = "(custom command)"
		}
		desc = "[" + string(c.Source) + "] " + desc
		out = append(out, SlashCmd{
			Name: "/" + c.Name, Args: "[args…]",
			Description: desc,
		})
	}
	return out
}

// lookupUserCommand checks whether `slash` (e.g. "/refactor") is a
// user-defined command. Returns the command + the verbatim arg
// string (everything after the slash + a single space) when found.
//
// Loaded fresh on every call so a user editing
// ~/.biumind/commands/foo.md doesn't need to restart biu — the
// next `/foo` re-reads the file.
func (m model) lookupUserCommand(slash, line string) (*commands.Command, string, bool) {
	if !strings.HasPrefix(slash, "/") {
		return nil, "", false
	}
	name := strings.TrimPrefix(slash, "/")
	cwd, _ := os.Getwd()
	reg, err := loadCommandsWithPlugins(cwd)
	if err != nil {
		return nil, "", false
	}
	cmd, ok := reg.Lookup(name)
	if !ok {
		return nil, "", false
	}
	args := strings.TrimSpace(strings.TrimPrefix(line, slash))
	return cmd, args, true
}

// runUserCommand renders a user-defined command's body with the
// supplied args + the standard $CWD / $DATE substitutions, then
// dispatches it to the engine as if the user had typed the
// rendered prompt directly. Echoes the source slash invocation
// into history so the transcript records what the user typed.
func (m model) runUserCommand(cmd *commands.Command, args string) (tea.Model, tea.Cmd) {
	if m.engine == nil {
		m.appendSystemNote(fmt.Sprintf("/%s: requires engine path "+
			"(custom commands need the agent loop)", cmd.Name))
		return m, nil
	}
	rendered := cmd.Render(args)
	if strings.TrimSpace(rendered) == "" {
		m.appendSystemNote(fmt.Sprintf("/%s: command body is empty (check %s)",
			cmd.Name, cmd.Path))
		return m, nil
	}
	display := "/" + cmd.Name
	if args != "" {
		display += " " + args
	}
	m.history = append(m.history, client.Message{Role: "user", Content: display})
	if m.sessionLog != nil {
		_ = m.sessionLog.Append(session.Event{
			Type: "user_message", Content: display,
		})
	}
	return m.startEngineStream(rendered)
}
