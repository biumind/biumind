// /hooks slash — list every registered hook entry across every
// event, grouped by event with source provenance.
//
// The biu hook system supports two
// hook types (`type=command` subprocess + `type=internal` Go
// handler from bundled plugins); the slash output discriminates
// the two so users debugging "why isn't my hook firing" can see
// the wire form quickly.
//
// Read-only — editing settings.json hooks blocks then `/reload` is
// the canonical edit path. Trust state is surfaced because hook
// firing is gated by directory trust; an "untrusted dir → 0 hooks
// even though config has them" scenario is the most common
// confusion.

package repl

import (
	"fmt"
	"sort"
	"strings"

	"github.com/biumind/biumind/apps/cli/biu/internal/hooks"
)

// handleHooks renders the registered hook set. parts is the slash +
// args slice; sub-commands are reserved for future use (e.g.
// `/hooks <event>` to filter).
func (m model) handleHooks(parts []string) string {
	if m.engine == nil {
		return "/hooks: engine not wired (chat-only mode shows no hooks)"
	}
	reg := m.engine.Hooks()
	if reg == nil {
		return "/hooks: no hook registry on this engine"
	}

	// Filter by sub-command when supplied. /hooks <event> shows
	// only the matching event; /hooks <substring> matches any
	// event name containing the substring (case-insensitive).
	filter := ""
	if len(parts) > 1 {
		filter = strings.ToLower(parts[1])
	}

	type eventEntries struct {
		Event hooks.Event
		Items []hooks.Entry
	}
	var groups []eventEntries
	for _, evt := range hooks.AllEvents {
		// For events with a key (PreToolUse / PostToolUse), pass
		// "" to get all entries. The runner does matcher matching
		// per-call; the slash just lists what's registered.
		entries := reg.For(evt, "")
		if filter != "" && !strings.Contains(strings.ToLower(string(evt)), filter) {
			continue
		}
		if len(entries) == 0 {
			continue
		}
		groups = append(groups, eventEntries{Event: evt, Items: entries})
	}

	if len(groups) == 0 {
		if filter != "" {
			return fmt.Sprintf("/hooks: no entries matching %q", filter)
		}
		return "/hooks: no hooks registered. Configure under " +
			"`hooks` in ~/.biumind/settings.json or via a plugin."
	}

	// Stable order — alphabetical by event name so the report is
	// diffable across sessions.
	sort.Slice(groups, func(i, j int) bool {
		return string(groups[i].Event) < string(groups[j].Event)
	})

	var b strings.Builder
	totalEntries := 0
	for _, g := range groups {
		totalEntries += len(g.Items)
	}
	fmt.Fprintf(&b, "/hooks: %d hook(s) across %d event(s)\n",
		totalEntries, len(groups))
	for _, g := range groups {
		fmt.Fprintf(&b, "\n[%s]  (%d entr%s)\n",
			g.Event, len(g.Items), pluralIES(len(g.Items)))
		for _, e := range g.Items {
			fmt.Fprint(&b, "  ", renderHookEntry(e), "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// renderHookEntry formats one Entry for the slash output. Source +
// matcher + type-specific payload (command body or handler name).
func renderHookEntry(e hooks.Entry) string {
	matcher := e.Matcher
	if matcher == "" {
		matcher = "*"
	}
	switch e.Command.Type {
	case "internal":
		return fmt.Sprintf("[%s] matcher=%q  internal:%s",
			e.Source, matcher, e.Command.Handler)
	case "", "command":
		cmd := e.Command.Command
		if len(cmd) > 80 {
			cmd = cmd[:77] + "…"
		}
		return fmt.Sprintf("[%s] matcher=%q  $ %s",
			e.Source, matcher, cmd)
	default:
		return fmt.Sprintf("[%s] matcher=%q  type=%q (unsupported in this build)",
			e.Source, matcher, e.Command.Type)
	}
}

func pluralIES(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}
