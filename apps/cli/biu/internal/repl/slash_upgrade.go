// /upgrade slash — upgrade biu in-place using whatever install
// method shipped this binary.
//
// We don't ship our own updater; we just route the user to the
// right tool given how they got here. The detection logic is
// shared with /install via detectInstallMethod.
//
// Forms:
//
//	/upgrade            — show current version + the upgrade command
//	/upgrade run        — actually run it (printed first so the user
//	                      can see what's about to happen)
//	/upgrade check      — version-only, no command output
//
// Why not just run the upgrade silently? Because `brew upgrade`,
// `go install`, and `snap refresh` all have failure modes that are
// worth seeing — broken tap, network down, version conflict. /upgrade
// shows the command, the user picks `run`, biu echoes + execs it.

package repl

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

func (m model) handleUpgrade(parts []string) string {
	info := installInfoForREPL()
	exe, _ := os.Executable()
	method, hint := detectInstallMethod(exe)

	mode := "show"
	if len(parts) >= 2 {
		switch strings.ToLower(parts[1]) {
		case "run":
			mode = "run"
		case "check":
			mode = "check"
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "/upgrade: biu %s (%s)\n", info.Version, info.Commit)
	if method == "" {
		b.WriteString("install method: unknown — pull the latest binary from\n")
		b.WriteString("https://github.com/biumind/biumind/releases\n")
		return strings.TrimRight(b.String(), "\n")
	}
	fmt.Fprintf(&b, "install method: %s\n", method)
	fmt.Fprintf(&b, "command:        %s\n", hint)

	switch mode {
	case "check":
		// Version-only is the bare form output already; nothing else to do.
	case "run":
		b.WriteString("\nrunning…\n")
		out, err := runUpgradeCommand(hint)
		b.WriteString(out)
		if err != nil {
			b.WriteString("\nerror: " + err.Error())
		} else {
			b.WriteString("\nupgrade ran successfully — restart biu to load the new binary")
		}
	default:
		b.WriteString("\nrun `/upgrade run` to execute, or copy the command above")
	}
	return strings.TrimRight(b.String(), "\n")
}

// runUpgradeCommand executes the suggested command via /bin/sh -c so
// pipes / multi-word args parse correctly. Times out at 5 minutes —
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
