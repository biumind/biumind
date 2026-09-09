// Startup update check — REPL wiring for internal/updatecheck.
//
// The check runs as a tea.Cmd off Init(): bubbletea executes cmds in
// their own goroutine, so the 3s network budget never blocks the
// first paint. A nil result (up to date / throttled / dev build /
// offline) yields a nil tea.Msg, which bubbletea drops silently —
// the happy path is invisible.

package repl

import (
	"context"
	"fmt"
	"strings"

	"github.com/biumind/biumind/apps/cli/biu/internal/updatecheck"
	tea "github.com/charmbracelet/bubbletea"
)

// updateCheckMsg carries a positive "newer version available" result.
type updateCheckMsg struct{ res *updatecheck.Result }

// checkUpdateCmd is the startup check. Anything but a positive result
// returns nil (dropped by bubbletea, no notice shown).
func (m model) checkUpdateCmd() tea.Msg {
	info := installInfoForREPL()
	res, err := updatecheck.MaybeCheck(context.Background(), updatecheck.Options{
		CurrentVersion: info.Version,
		ManifestURL:    m.updateManifestURL,
	})
	if err != nil || res == nil || !res.Newer {
		return nil
	}
	return updateCheckMsg{res: res}
}

// handleUpdateCheck renders the one-line startup notice.
func (m *model) handleUpdateCheck(msg updateCheckMsg) {
	m.appendSystemNote(fmt.Sprintf(
		"update available: biu %s (current %s) — /upgrade check for details, /upgrade run to update",
		msg.res.Latest, msg.res.Current))
}

// ─── /upgrade check (explicit, unthrottled) ────────────

// upgradeCheckResultMsg carries the outcome of an explicit
// `/upgrade check`. res is nil on failure — err says why.
type upgradeCheckResultMsg struct {
	res *updatecheck.Result
	err error
}

// upgradeCheckCmd is armed by the /upgrade dispatch: it hits the
// network off the Update loop (blocking Update for the 3s HTTP budget
// would freeze the TUI) and reports back asynchronously.
func (m model) upgradeCheckCmd() tea.Msg {
	info := installInfoForREPL()
	if updatecheck.ShouldSkipVersion(info.Version) {
		return upgradeCheckResultMsg{err: fmt.Errorf(
			"update checks are skipped for this build (%s): dev and desktop-client-managed builds don't self-update",
			info.Version)}
	}
	res, err := updatecheck.CheckWithFallback(context.Background(), updatecheck.Options{
		CurrentVersion: info.Version,
		ManifestURL:    m.updateManifestURL,
	})
	if err == nil {
		_ = updatecheck.RecordCheck(res.Latest)
	}
	return upgradeCheckResultMsg{res: res, err: err}
}

// handleUpgradeCheckResult renders the /upgrade check report.
func (m *model) handleUpgradeCheckResult(msg upgradeCheckResultMsg) {
	if msg.err != nil {
		m.appendSystemNote("/upgrade check: " + msg.err.Error())
		return
	}
	res := msg.res
	var b strings.Builder
	if !res.Newer {
		fmt.Fprintf(&b, "/upgrade check: up to date (biu %s, %s)", res.Current, res.Source)
		m.appendSystemNote(b.String())
		return
	}
	fmt.Fprintf(&b, "/upgrade check: biu %s available (current %s)\n", res.Latest, res.Current)
	fmt.Fprintf(&b, "  source:  %s\n", res.Source)
	if res.DownloadSource != "" && res.DownloadSource != res.Source {
		fmt.Fprintf(&b, "  download via: %s\n", res.DownloadSource)
	}
	fmt.Fprintf(&b, "  release: %s\n", res.ReleaseURL)
	b.WriteString("  update:  /upgrade run, or /upgrade check skip to silence this version")
	m.appendSystemNote(b.String())
}
