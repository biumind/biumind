// /upgrade run — async upgrade executor.
//
// Runs as a tea.Cmd off the /upgrade dispatch so neither the hint
// command (brew/go install can take minutes) nor the self-update
// download blocks the Update loop.
//
// Routing by install method:
//
//	homebrew / go install / snap → exec the delegated hint command
//	(client-managed)               → refuse (the client owns updates)
//	manual / unknown               → self-update (internal/selfupdate):
//	                                 download tar.gz, verify SHA-256,
//	                                 atomic swap, keep .old for rollback

package repl

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/internal/selfupdate"
	"github.com/biumind/biumind/apps/cli/biu/internal/updatecheck"
	tea "github.com/charmbracelet/bubbletea"
)

// upgradeRunResultMsg carries the outcome of /upgrade run.
type upgradeRunResultMsg struct{ text string }

func (m model) upgradeRunCmd() tea.Msg {
	exe, _ := os.Executable()
	method, hint := detectInstallMethod(exe)

	switch {
	case method == "client-managed (~/.local/bin)":
		return upgradeRunResultMsg{text: "/upgrade run: this biu was installed by the BiuMind desktop client, which manages its updates — nothing to do"}
	case method != "" && hint != "":
		// homebrew / go install / snap: delegate.
		out, err := runUpgradeCommand(hint)
		var b strings.Builder
		fmt.Fprintf(&b, "/upgrade run (%s):\n%s", method, out)
		if err != nil {
			b.WriteString("\nerror: " + err.Error())
		} else {
			b.WriteString("\nupgrade ran successfully — restart biu to load the new binary")
		}
		return upgradeRunResultMsg{text: b.String()}
	}

	// manual / unknown install → self-update.
	info := installInfoForREPL()
	if updatecheck.ShouldSkipVersion(info.Version) {
		return upgradeRunResultMsg{text: fmt.Sprintf(
			"/upgrade run: self-update is not available for this build (%s): dev and desktop-client-managed builds don't self-update",
			info.Version)}
	}
	res, err := updatecheck.CheckWithFallback(context.Background(), updatecheck.Options{
		CurrentVersion: info.Version,
		ManifestURL:    m.updateManifestURL,
	})
	if err != nil {
		return upgradeRunResultMsg{text: "/upgrade run: " + err.Error()}
	}
	if !res.Newer {
		return upgradeRunResultMsg{text: fmt.Sprintf(
			"/upgrade run: already up to date (biu %s, %s)", res.Current, res.Source)}
	}
	if res.AssetURL == "" {
		return upgradeRunResultMsg{text: fmt.Sprintf(
			"/upgrade run: no downloadable asset for this platform on %s — grab biu %s from %s",
			res.Source, res.Latest, res.ReleaseURL)}
	}

	if err := selfupdate.Apply(context.Background(), selfupdate.Options{
		AssetURL:      res.AssetURL,
		AssetFilename: res.AssetFilename,
		AssetSha256:   res.AssetSha256,
		ChecksumsURL:  res.ChecksumsURL,
	}); err != nil {
		return upgradeRunResultMsg{text: "/upgrade run: " + err.Error()}
	}
	return upgradeRunResultMsg{text: fmt.Sprintf(
		"/upgrade run: biu %s installed (was %s) — restart biu to load it; previous binary kept at %s.old",
		res.Latest, res.Current, exe)}
}

// runUpgradeCommand executes the delegated hint command via /bin/sh -c
// so multi-word args parse correctly. Times out at 5 minutes —
// `go install` from a cold module cache can be slow, so we don't go
// shorter than that.
func runUpgradeCommand(cmdline string) (string, error) {
	if strings.TrimSpace(cmdline) == "" {
		return "", fmt.Errorf("empty command")
	}
	// Only the prefix word (brew / go / sudo / snap) is shell-safe —
	// scrub the obvious foot-guns. We're not trying to be a full
	// sandbox; the install hints we emit are static strings under our
	// control, this is just a defence-in-depth against the table
	// being changed carelessly.
	if strings.ContainsAny(cmdline, "`$;|&><\n") {
		return "", fmt.Errorf("command contains shell metachars; refusing to run: %s", cmdline)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", cmdline)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}
