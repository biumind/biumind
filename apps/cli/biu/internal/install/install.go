// Package install detects how the running biu binary was installed
// and decides what an upgrade should do with that knowledge.
//
// Extracted from internal/repl so both the REPL slash commands and
// the cobra `biu upgrade` subcommand share ONE detection table and
// ONE routing decision — two copies would drift the first time a new
// install plane (or hint) is added.
//
// Detection is version-aware, not purely path-based: the path alone
// can't distinguish a desktop-client-managed binary from a manual
// install into the same directory (~/.local/bin is both the client's
// install plane AND the XDG-standard manual location). The build
// metadata breaks the tie — client builds carry `+clientmanaged`,
// dev builds carry a `-` suffix (same semantics as
// updatecheck.ShouldSkipVersion), so a clean release version under
// ~/.local/bin is treated as a manual install and may self-update.
//
// Routing contract:
//
//	homebrew / go install / snap → delegate to the owning package
//	                               manager (it owns the binary)
//	client-managed (~/.local/bin) → refuse (the desktop client owns
//	                               that install plane and auto-updates)
//	manual / unknown             → self-update via internal/selfupdate
//	                               (download → SHA-256 → atomic swap)

package install

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/internal/updatecheck"
)

// Method identifies the install pathway of the running binary.
type Method int

const (
	// MethodUnknown: no tell-tale matched; self-update is the fallback.
	MethodUnknown Method = iota
	MethodHomebrew
	MethodGoInstall
	// MethodClientManaged: installed by the BiuMind desktop client
	// under ~/.local/bin — that plane auto-updates, never self-update.
	MethodClientManaged
	// MethodManual: user dropped the binary into /usr/local/bin.
	MethodManual
	MethodSnap
)

// String returns the human-facing label. These strings are user-visible
// in /install and /upgrade output — keep them stable.
func (m Method) String() string {
	switch m {
	case MethodHomebrew:
		return "homebrew"
	case MethodGoInstall:
		return "go install"
	case MethodClientManaged:
		return "client-managed (~/.local/bin)"
	case MethodManual:
		return "manual install (/usr/local/bin)"
	case MethodSnap:
		return "snap"
	default:
		return ""
	}
}

// Action is what an upgrade should do for a detected Method.
type Action int

const (
	// ActionDelegate: run the method's hint command (brew/go/snap).
	ActionDelegate Action = iota
	// ActionRefuse: upgrades are owned elsewhere (desktop client).
	ActionRefuse
	// ActionSelfUpdate: download + verify + swap the binary in place.
	ActionSelfUpdate
)

// Route maps a detected install method to the upgrade action. Kept as
// a pure function so REPL and cobra can never disagree.
func Route(m Method) Action {
	switch m {
	case MethodHomebrew, MethodGoInstall, MethodSnap:
		return ActionDelegate
	case MethodClientManaged:
		return ActionRefuse
	default:
		return ActionSelfUpdate
	}
}

// Detect inspects the binary path for tell-tales of each common
// install pathway. Best-effort — when unsure, returns MethodUnknown +
// no hint rather than guessing wrong.
//
// version matters for exactly one branch: ~/.local/bin is both the
// desktop client's install plane and the XDG-standard manual install
// location, so the path alone can't decide. A version that update
// checks would skip (empty, build metadata like `+clientmanaged`,
// prerelease/dev suffix, non-semver) means the client (or a dev
// `task cli:install`) owns the binary → MethodClientManaged. A clean
// X.Y.Z release version there is a manual install → MethodUnknown,
// which routes to self-update.
func Detect(exe, version string) (Method, string) {
	switch {
	case exe == "" || exe == "(unknown)":
		return MethodUnknown, ""
	case strings.Contains(exe, "/Cellar/"),
		strings.Contains(exe, "/homebrew/"),
		strings.Contains(exe, "/linuxbrew/"):
		return MethodHomebrew,
			"brew upgrade biumind/tap/biu"
	case strings.Contains(exe, "/go/bin/"),
		strings.HasSuffix(exe, "/biu") && strings.Contains(exe, "GOPATH"):
		return MethodGoInstall,
			"go install github.com/biumind/biumind/apps/cli/biu/cmd/biu@latest"
	case IsUnderLocalBin(exe) && updatecheck.ShouldSkipVersion(version):
		// ~/.local/bin is where the desktop client installs its biu
		// daemon (biu_daemon_manager) — that install plane is managed
		// by the client. Dev builds land there too via
		// `task cli:install`; both carry skip-eligible versions, so
		// they never self-update.
		return MethodClientManaged,
			"installed by the BiuMind desktop client (it auto-updates biu); " +
				"for a dev build re-run `task cli:install`"
	case strings.HasPrefix(exe, "/usr/local/bin/"):
		return MethodManual,
			"download the latest release from " +
				"https://github.com/biumind/biumind/releases " +
				"and replace " + exe
	case strings.HasPrefix(exe, "/snap/"):
		return MethodSnap,
			"sudo snap refresh biu"
	}
	return MethodUnknown, ""
}

// IsUnderLocalBin reports whether exe lives in the user's
// ~/.local/bin (the desktop client's biu install location).
func IsUnderLocalBin(exe string) bool {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return false
	}
	return strings.HasPrefix(exe, filepath.Join(home, ".local/bin")+string(os.PathSeparator))
}

// Info carries the build metadata (-ldflags vars) that the upgrade
// surfaces report.
type Info struct {
	Version   string
	Commit    string
	BuildDate string
}

// current is a function variable so cmd/biu can inject the build vars
// without exposing them as package-level globals here. Default returns
// "0.1.0-dev" placeholders so callers never crash when invoked from a
// test that didn't set up the wiring.
var current = func() Info {
	return Info{
		Version:   "0.1.0-dev",
		Commit:    "none",
		BuildDate: "unknown",
	}
}

// SetInfo lets the cobra wiring layer push the real build vars into
// this package. Called from cmd/biu/main.go's main(). Tests leave it
// alone or restore the defaults via SetInfo("0.1.0-dev", "none", "unknown").
func SetInfo(version, commit, buildDate string) {
	current = func() Info {
		return Info{
			Version:   version,
			Commit:    commit,
			BuildDate: buildDate,
		}
	}
}

// Current returns the build metadata for the running binary.
func Current() Info { return current() }

// RunDelegated executes the delegated hint command via /bin/sh -c
// so multi-word args parse correctly. Times out at 5 minutes —
// `go install` from a cold module cache can be slow, so we don't go
// shorter than that.
func RunDelegated(ctx context.Context, cmdline string) (string, error) {
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
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", cmdline)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}
