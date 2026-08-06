// lipgloss styles for the REPL TUI. Single source of truth so the
// model code stays focused on state transitions.

package repl

import "github.com/charmbracelet/lipgloss"

var (
	// Brand purple, matches BiuTokens.purple in the Flutter client.
	colorPurple   = lipgloss.Color("#5B5BD6")
	colorMuted    = lipgloss.Color("#9CA3AF")
	colorBorder   = lipgloss.Color("#E7E5E4")
	colorSubtle   = lipgloss.Color("#52525B")
	colorGreen    = lipgloss.Color("#10B981")
	colorRed      = lipgloss.Color("#DC2626")
	colorYellow   = lipgloss.Color("#EAB308")

	// Top status bar. Inverse-ish — purple bg + white text.
	styleStatus = lipgloss.NewStyle().
		Background(colorPurple).
		Foreground(lipgloss.Color("#FFFFFF")).
		Padding(0, 1).
		Bold(true)

	styleStatusMuted = lipgloss.NewStyle().
		Background(colorPurple).
		Foreground(lipgloss.Color("#E0DEFF")).
		Padding(0, 1)

	// Message prefixes
	styleUserPrefix = lipgloss.NewStyle().
		Foreground(colorPurple).
		Bold(true)

	styleAssistantPrefix = lipgloss.NewStyle().
		Foreground(colorGreen).
		Bold(true)

	styleErrorPrefix = lipgloss.NewStyle().
		Foreground(colorRed).
		Bold(true)

	// Tool call rendering (Claude Code-style: ⏺ Reading file:1-200)
	styleToolBullet = lipgloss.NewStyle().
		Foreground(colorPurple)

	styleToolDesc = lipgloss.NewStyle().
		Foreground(colorMuted)

	// Diff block rendering: green for additions, red for removals,
	// dim for hunk + meta lines.
	styleDiffAdd  = lipgloss.NewStyle().Foreground(lipgloss.Color("#3fb950"))
	styleDiffDel  = lipgloss.NewStyle().Foreground(lipgloss.Color("#f85149"))
	styleDiffHunk = lipgloss.NewStyle().Foreground(lipgloss.Color("#8b949e")).Italic(true)
	styleDiffMeta = lipgloss.NewStyle().Foreground(colorMuted)

	// Help / hint line under the input
	styleHint = lipgloss.NewStyle().
		Foreground(colorMuted).
		Italic(true)

	// Input area border
	styleInputBox = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorBorder).
		Padding(0, 1)

	styleInputBoxFocused = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorPurple).
		Padding(0, 1)

	// Slash command panel (overlaid above input when "/" typed)
	styleSlashPanel = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorPurple).
		Padding(0, 1)

	styleSlashSelected = lipgloss.NewStyle().
		Background(colorPurple).
		Foreground(lipgloss.Color("#FFFFFF")).
		Padding(0, 1)

	styleSlashDim = lipgloss.NewStyle().
		Foreground(colorMuted).
		Padding(0, 1)

	// Subtle separator line
	styleSeparator = lipgloss.NewStyle().
		Foreground(colorBorder)

	// Spinner colour
	styleSpinner = lipgloss.NewStyle().
		Foreground(colorPurple)
)

// renderUserPrefix puts a coloured "› user >" tag in front of a message.
func renderUserPrefix() string {
	return styleUserPrefix.Render("› ")
}

func renderAssistantPrefix() string {
	return styleAssistantPrefix.Render("✦ ")
}

func renderErrorPrefix() string {
	return styleErrorPrefix.Render("✗ ")
}
