// Run is the public entry — wraps the bubbletea program lifecycle
// so cmd/biu/main.go barely changes.

package repl

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/biumind/biumind/apps/cli/biu/internal/clierr"
)

// Run blocks until the user quits the TUI (Ctrl-D, Ctrl-C twice, or
// /quit). Returns nil on clean exit.
func Run(ctx context.Context, opt Options) error {
	if opt.Provider == nil {
		return clierr.WithHint(
			clierr.Newf("repl", "no provider configured"),
			"run `biu init` to write ~/.biu/config.toml")
	}
	m := New(opt)
	prog := tea.NewProgram(
		m,
		tea.WithAltScreen(),       // full-screen TUI
		tea.WithMouseCellMotion(), // enable mouse scroll
		tea.WithContext(ctx),
	)
	if _, err := prog.Run(); err != nil {
		return clierr.Wrapf("repl", err, "TUI program")
	}
	return nil
}
