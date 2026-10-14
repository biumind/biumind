// /install slash — biu install diagnostics + update guidance.
//
// biu upgrades via /upgrade run: delegated to brew / go install /
// snap when the install method owns the binary, or self-update
// (internal/selfupdate: download → SHA-256 verify → atomic swap) for
// manual installs. This slash is the diagnostic surface:
//
//   - Where the running binary lives + how it got there
//   - Build version / commit / date (from the version_cmd vars)
//   - Recommended update command for the detected install method
//
// /install + /doctor are complementary — /doctor verifies runtime
// integrity end-to-end; /install focuses on the binary itself.
//
// Detection + build-info plumbing live in internal/install, shared
// with the cobra `biu upgrade` subcommand.

package repl

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/biumind/biumind/apps/cli/biu/internal/install"
)

func (m model) handleInstall(parts []string) string {
	info := install.Current()

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

	method, hint := install.Detect(exe, info.Version)
	if method != install.MethodUnknown {
		fmt.Fprintf(&b, "\ninstall method: %s\n", method)
		if hint != "" {
			fmt.Fprintf(&b, "update with:\n  %s\n", hint)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}
