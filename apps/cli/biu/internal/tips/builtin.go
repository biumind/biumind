// Built-in tip set. Each tip points users at a biu feature they're
// likely missing. Adding a new tip = one entry here; the scheduler
// + history apparatus auto-handles the rest.
//
// Style guide for tips:
//   - One actionable command per tip (`/foo` or `biu foo`).
//   - Why this is useful in ≤ 1 sentence.
//   - No "did you know"; no marketing voice.

package tips

// RegisterBuiltins seeds the registry with biu's starter tip set.
// Called once at REPL startup; pass the same registry the
// scheduler queries.
func RegisterBuiltins(r *Registry) {
	for _, t := range builtinTips() {
		r.Register(t)
	}
}

func builtinTips() []Tip {
	return []Tip{
		{
			ID:    "doctor-on-failure",
			Title: "/doctor first when something feels wrong",
			Body:  "Runs a tiered health check (runtime / git / shell / engine / MCP) in under a second.",
		},
		{
			ID:    "rewind-files",
			Title: "/rewind undoes file changes from this session",
			Body:  "Snapshots are captured before every Edit/Write. List with /rewind, restore with /rewind <uuid>.",
		},
		{
			ID:    "plugin-marketplace",
			Title: "Plugin marketplaces aggregate plugin installs",
			Body:  "biu plugin marketplace add <name> <git+https://repo.git>; then biu plugin install <plugin>@<name>.",
		},
		{
			ID:    "fast-mode",
			Title: "/fast for quick one-off questions",
			Body:  "Switches to Haiku (cheapest, fastest). /effort high to go back to Opus when you need depth.",
		},
		{
			ID:    "compact-warning",
			Title: "biu warns you before auto-compact",
			Body:  "info at 50%, urgent at 85% of context. /compact to summarise manually with control.",
		},
		{
			ID:    "share-session",
			Title: "/share copies a session export to your clipboard",
			Body:  "Useful for bug reports — the path goes to clipboard, paste it into Slack / email / gist.",
		},
		{
			ID:    "skill-paths",
			Title: "Skills auto-attach by file path glob",
			Body:  "Add `paths: [\"**/*.go\"]` to a SKILL.md frontmatter; biu folds the body into the system prompt when those files are open.",
		},
		{
			ID:     "plugin-disable",
			Title:  "/plugin disable <name> shuts off a plugin without uninstalling",
			Body:   "Persists to settings.json. /plugin enable <name> reverses. Useful when triaging which plugin caused a regression.",
			Weight: 2, // common-need bias
		},
		{
			ID:    "session-memory",
			Title: "biu remembers context across compacts",
			Body:  "After /compact, the summary writes into ~/.biumind/sessionMemory/<id>.md and re-attaches on the next session. Edit the file directly to seed memory.",
		},
		{
			ID:    "trust-shell-hooks",
			Title: "Shell hooks need /trust here for new directories",
			Body:  "biu refuses to run plugin / settings shell hooks in untrusted dirs. /trust adds the cwd to the persistent allow-list.",
		},
		{
			ID:    "structured-output",
			Title: "Headless calls can request structured JSON via StructuredOutput",
			Body:  "Pass interactive.Options.StructuredOutputSchema; the model is instructed to emit a final JSON matching your shape.",
		},
		{
			ID:    "doctor-mcp-not-running",
			Title: "An MCP server config that isn't running shows up in /doctor",
			Body:  "Settings.json `mcpServers` plus /doctor tells you which ones connected and which need a fix.",
		},
	}
}
