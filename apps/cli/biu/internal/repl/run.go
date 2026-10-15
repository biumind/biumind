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
//
// opt.Provider may be nil: cmd/biu degrades to a signed-out REPL when
// the user isn't logged in (懒引导) — the model handles that via
// notReadyHint and the /login → RewireAfterLogin flow, so Run must
// NOT gate on it. (It once did, which made main.go's signed-out
// degradation unreachable dead code.)
func Run(ctx context.Context, opt Options) error {
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
