// /install slash — biu install diagnostics + update guidance.
//
// biu doesn't ship a self-updater (binary updates flow through
// `go install` / brew / apt), so the slash repurposes the name as a
// diagnostic surface:
//
//   - Where the running binary lives + how it got there
//   - Build version / commit / date (from the version_cmd vars)
//   - Recommended update command for the detected install method
//
// /install + /doctor are complementary — /doctor verifies runtime
// integrity end-to-end; /install focuses on the binary itself.

package repl

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// installInfo is filled by main.go via the same -ldflags variables
// the version_cmd uses. Defaults are fine for tests / unbuilt
// binaries.
type installInfo struct {
	Version   string
	Commit    string
	BuildDate string
}

// installInfoForREPL is a function variable so cmd/biu can inject
// the build vars without exposing them as package-level globals
// in internal/repl. Default returns "0.1.0-dev" placeholders so
// the slash never crashes when called from a test that didn't set
// up the wiring.
var installInfoForREPL = func() installInfo {
	return installInfo{
		Version:   "0.1.0-dev",
		Commit:    "none",
		BuildDate: "unknown",
	}
}

// SetInstallInfo lets the cobra wiring layer push the real build
// vars into this package. Called from cmd/biu/main.go's init or
// the version_cmd setup. Tests leave it alone.
func SetInstallInfo(version, commit, buildDate string) {
	installInfoForREPL = func() installInfo {
		return installInfo{
			Version:   version,
			Commit:    commit,
			BuildDate: buildDate,
		}
	}
}

func (m model) handleInstall(parts []string) string {
	info := installInfoForREPL()

	exe, err := os.Executable()
	if err != nil {
		exe = "(unknown)"
	}
	if abs, err := filepath.EvalSymlinks(exe); err == nil {
		exe = abs
	}

	var b strings.Builder
	b.WriteString("/install: biu binary diagnostics\n")
	fmt.Fprintf(&b, "  binary:    %s\n", exe)
	fmt.Fprintf(&b, "  version:   %s\n", info.Version)
	fmt.Fprintf(&b, "  commit:    %s\n", info.Commit)
	fmt.Fprintf(&b, "  built:     %s\n", info.BuildDate)
	fmt.Fprintf(&b, "  go:        %s\n", runtime.Version())
	fmt.Fprintf(&b, "  os/arch:   %s/%s\n", runtime.GOOS, runtime.GOARCH)

	method, hint := detectInstallMethod(exe)
	if method != "" {
		fmt.Fprintf(&b, "\ninstall method: %s\n", method)
		if hint != "" {
			fmt.Fprintf(&b, "update with:\n  %s\n", hint)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// detectInstallMethod inspects the binary path for tell-tales of
// each common install pathway. Best-effort — when unsure, returns
// empty + no hint rather than guessing wrong.
func detectInstallMethod(exe string) (method, updateHint string) {
	switch {
	case exe == "" || exe == "(unknown)":
		return "", ""
	case strings.Contains(exe, "/Cellar/"),
		strings.Contains(exe, "/homebrew/"),
		strings.Contains(exe, "/linuxbrew/"):
		return "homebrew",
			"brew upgrade biumind/tap/biu"
	case strings.Contains(exe, "/go/bin/"),
		strings.HasSuffix(exe, "/biu") && strings.Contains(exe, "GOPATH"):
		return "go install",
			"go install github.com/biumind/biumind/apps/cli/biu/cmd/biu@latest"
	case strings.HasPrefix(exe, "/usr/local/bin/"):
		return "manual install (/usr/local/bin)",
			"download the latest release from " +
				"https://github.com/biumind/biumind/releases " +
				"and replace " + exe
	case strings.HasPrefix(exe, "/snap/"):
		return "snap",
			"sudo snap refresh biu"
	}
	return "", ""
}
