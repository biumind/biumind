// /upgrade run — async upgrade executor.
//
// Runs as a tea.Cmd off the /upgrade dispatch so neither the hint
// command (brew/go install can take minutes) nor the self-update
// download blocks the Update loop.
//
// Routing by install method (install.Route, shared with `biu upgrade`):
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
	"strings"

	"github.com/biumind/biumind/apps/cli/biu/internal/install"
	"github.com/biumind/biumind/apps/cli/biu/internal/selfupdate"
	"github.com/biumind/biumind/apps/cli/biu/internal/updatecheck"
	tea "github.com/charmbracelet/bubbletea"
)

// upgradeRunResultMsg carries the outcome of /upgrade run.
type upgradeRunResultMsg struct{ text string }

func (m model) upgradeRunCmd() tea.Msg {
	info := install.Current()
	exe, _ := os.Executable()
	method, hint := install.Detect(exe, info.Version)

	switch install.Route(method) {
	case install.ActionRefuse:
		return upgradeRunResultMsg{text: "/upgrade run: this biu was installed by the BiuMind desktop client, which manages its updates — nothing to do"}
	case install.ActionDelegate:
		// homebrew / go install / snap: delegate.
		out, err := install.RunDelegated(context.Background(), hint)
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
