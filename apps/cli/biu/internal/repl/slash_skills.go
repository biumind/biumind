// File-based SKILL.md slash dispatch. Mirrors slash_commands.go but
// resolves names against the skills.Registry the wiring layer loaded
// from `~/.biumind/skills/` and `<cwd>/.biumind/skills/`.
//
// User-defined commands are looked up first (in model.runSlash's
// default fallthrough); skills tail-match only when no command
// claimed the name. Both registries support `$ARGS` body
// substitution so the user-visible behaviour of `/foo bar baz` is
// the same — the difference shows up only on the metadata side
// (skills carry frontmatter for paths, permissions, when-to-use
// hints; commands are bare prompts).

package repl

import (
	"context"
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/biumind/biumind/apps/cli/biu/internal/client"
	"github.com/biumind/biumind/apps/cli/biu/internal/session"
	"github.com/biumind/biumind/apps/cli/biu/internal/skills"
)

// skillSlashItems materialises a SlashCmd row for every loaded skill.
// Returns nil when the registry is unset or empty so the dropdown
// quietly drops the skills section. Source tag distinguishes them
// from user commands at a glance.
func (m model) skillSlashItems() []SlashCmd {
	if m.skills == nil {
		return nil
	}
	all := m.skills.All()
	if len(all) == 0 {
		return nil
	}
	out := make([]SlashCmd, 0, len(all))
	for _, s := range all {
		desc := s.Description
		if desc == "" {
			desc = "(skill)"
		}
		desc = "[skill:" + s.Source + "] " + desc
		out = append(out, SlashCmd{
			Name: "/" + s.Name, Args: "[args…]",
			Description: desc,
		})
	}
	return out
}

// lookupSkill checks whether `slash` (e.g. "/code-review") matches a
// loaded skill. Returns the skill + arg string when found.
//
// Mirrors lookupUserCommand's contract: the registry is consulted
// every call so a user editing SKILL.md doesn't need to restart biu.
// The wiring layer's registry is reused; for repls launched without
// Skills wired in, this short-circuits to false.
func (m model) lookupSkill(slash, line string) (skills.RuntimeSkill, string, bool) {
	if m.skills == nil || !strings.HasPrefix(slash, "/") {
		return skills.RuntimeSkill{}, "", false
	}
	name := strings.TrimPrefix(slash, "/")
	rs, ok := m.skills.Lookup(name)
	if !ok {
		return skills.RuntimeSkill{}, "", false
	}
	args := strings.TrimSpace(strings.TrimPrefix(line, slash))
	return rs, args, true
}

// runSkill expands the skill body with `$ARGS` substituted, then
// dispatches the rendered prompt through the engine just like a
// user-typed message. Echoes the source slash invocation into
// history so the transcript records what the user typed (not the
// expanded prompt — the latter can be huge).
func (m model) runSkill(rs skills.RuntimeSkill, args string) (tea.Model, tea.Cmd) {
	if m.engine == nil {
		m.appendSystemNote(fmt.Sprintf("/%s: requires engine path "+
			"(skills need the agent loop)", rs.Name()))
		return m, nil
	}
	rendered, err := rs.Run(context.Background(), args)
	if err != nil {
		m.appendSystemNote(fmt.Sprintf("/%s: %v", rs.Name(), err))
		return m, nil
	}
	if strings.TrimSpace(rendered) == "" {
		m.appendSystemNote(fmt.Sprintf("/%s: skill body is empty (check %s)",
			rs.Name(), rs.Path))
		return m, nil
	}
	display := "/" + rs.Name()
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

// pkg-level helper kept for tests: shadowed os.Getwd is a syscall
// we don't want unit tests to depend on. Mirrors slash_commands.go
// pattern (the test file does the same).
var _ = os.Getwd
