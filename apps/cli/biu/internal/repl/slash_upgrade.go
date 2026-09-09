// /upgrade slash — upgrade biu in-place using whatever install
// method shipped this binary.
//
// `run` and `check` are dispatched async in model.go (see
// upgrade_run.go / update_check.go); handleUpgrade here is the
// synchronous "show" surface: current version, detected install
// method, and the command a delegated upgrade would run.
//
// Forms:
//
//	/upgrade            — show current version + the upgrade command
//	/upgrade run        — execute (async): delegated command for
//	                      brew/go-install/snap, self-update for manual
//	/upgrade check      — live version check against GitHub (async)
//	/upgrade check skip — stop notifying about the latest version

package repl

import (
	"fmt"
	"os"
	"strings"

	"github.com/biumind/biumind/apps/cli/biu/internal/updatecheck"
)

func (m model) handleUpgrade(parts []string) string {
	info := installInfoForREPL()
	exe, _ := os.Executable()
	method, hint := detectInstallMethod(exe)

	var b strings.Builder
	fmt.Fprintf(&b, "/upgrade: biu %s (%s)\n", info.Version, info.Commit)
	if method == "" {
		b.WriteString("install method: unknown — /upgrade run self-updates via the release archive,\n")
		b.WriteString("or grab a binary from https://github.com/biumind/biumind/releases\n")
		return strings.TrimRight(b.String(), "\n")
	}
	fmt.Fprintf(&b, "install method: %s\n", method)
	fmt.Fprintf(&b, "command:        %s\n", hint)
	b.WriteString("\nrun `/upgrade run` to execute, or copy the command above")
	return strings.TrimRight(b.String(), "\n")
}

// handleUpgradeCheckSkip implements `/upgrade check skip [version]`.
// No argument → skip the latest version seen by the last check.
func (m model) handleUpgradeCheckSkip(args []string) string {
	v := ""
	if len(args) > 0 {
		v = strings.TrimSpace(args[0])
	}
	if v == "" {
		state, err := updatecheck.LoadState("")
		if err != nil {
			return "/upgrade check skip: " + err.Error()
		}
		v = state.LatestVersion
		if v == "" {
			return "/upgrade check skip: nothing to skip — no version known yet (run /upgrade check first)"
		}
	}
	if err := updatecheck.SkipVersion(v); err != nil {
		return "/upgrade check skip: " + err.Error()
	}
	return fmt.Sprintf("/upgrade check skip: biu %s will not be notified about again (a newer release clears this)",
		v)
}
