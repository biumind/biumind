// System prompt block: tells the LLM which Apps it has tools for.
//
// We do NOT inline every action's full description — the model
// already sees the per-action description through hubclient.Tool
// schemas. The block here is the executive summary: "you have rss
// (subscribe / fetch / digest) and email (list_inbox / draft / send)
// available; consult the per-tool descriptions for input shapes."
//
// Mirrors the pattern in skills/inject.go.

package apptools

import (
	"sort"
	"strings"
)

// BuildSystemPromptBlock returns a markdown-friendly block describing
// the available apps. Returns "" when no apps are loaded so the
// caller can skip concatenation cleanly.
func BuildSystemPromptBlock(loaded *Loaded) string {
	if loaded == nil || len(loaded.Apps) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("## Available Apps\n\n")
	b.WriteString("You have access to apps the user has installed. Each app's actions are exposed as tools namespaced by the app identifier (e.g. `rss.fetch`).\n")
	b.WriteString("Use these tools when the user's request matches an app's purpose. Tools follow the user's PermissionMode; high-risk tools require approval.\n\n")

	// Stable ordering — deterministic across runs makes prompt-cache
	// hits more likely.
	apps := make([]LoadedApp, len(loaded.Apps))
	copy(apps, loaded.Apps)
	sort.Slice(apps, func(i, j int) bool { return apps[i].Identifier < apps[j].Identifier })

	for _, la := range apps {
		b.WriteString("- **")
		b.WriteString(la.Identifier)
		b.WriteString("** ")
		if title := la.Manifest.DisplayName(); title != "" && title != la.Identifier {
			b.WriteString("(")
			b.WriteString(title)
			b.WriteString(") ")
		}
		if la.Manifest.Description != "" {
			b.WriteString("— ")
			b.WriteString(la.Manifest.Description)
		}
		if len(la.AvailableActions) > 0 {
			b.WriteString("\n  Actions: ")
			names := make([]string, len(la.AvailableActions))
			for i, a := range la.AvailableActions {
				names[i] = la.Identifier + "." + a.Name
			}
			b.WriteString(strings.Join(names, ", "))
		}
		b.WriteString("\n")
	}

	if len(loaded.MissingFromRegistry) > 0 {
		// Don't surface these to the model — they're a deployment
		// concern, not a content one. They'd just confuse the LLM.
	}

	return b.String()
}
