// OS-native Notifier implementations.
//
// Each platform binds to whatever's already on the user's machine
// (no extra deps shipped with biu). Failures are silent — desktop
// notifications are best-effort. The terminal bell always rings as a
// fallback so users get *some* signal even on bare SSH.
//
//   darwin  → osascript display notification
//   linux   → notify-send (GNOME/KDE/Cinnamon all install this)
//   windows → PowerShell Toast via BurntToast (when present) else
//             a Windows MessageBeep + console title flash
//   other   → terminal bell only

package interactive

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// SystemNotifier returns a Notifier that targets the host platform's
// standard notification stack. Always also prints `\a` to stderr so
// terminal users hear the bell even when the desktop layer fails.
//
// Title controls the notification's first line ("biu" by default).
// Bell can be disabled by setting it to false (some teams ban
// terminal bells in shared envs).
func SystemNotifier(title string) Notifier {
	return &osNotifier{title: title, bell: true, stderr: os.Stderr}
}

// osNotifier holds tunable knobs. Exposed indirectly via
// SystemNotifier so callers don't poke at internals.
type osNotifier struct {
	title  string
	bell   bool
	stderr io.Writer
}

func (n *osNotifier) Notify(ctx context.Context, message string) error {
	if message == "" {
		return nil
	}
	// Always ring the bell first — it's free and works on every
	// terminal. Failures here mean stderr is closed, which is
	// already a worse problem.
	if n.bell {
		_, _ = io.WriteString(n.stderr, "\a")
	}

	title := n.title
	if title == "" {
		title = "biu"
	}

	switch runtime.GOOS {
	case "darwin":
		return runOSAScript(ctx, title, message)
	case "linux":
		return runNotifySend(ctx, title, message)
	case "windows":
		return runWindowsToast(ctx, title, message)
	}
	// Unknown OS: bell only is enough.
	return nil
}

// runOSAScript invokes Apple's `osascript` to fire a banner. We
// quote the title + body so embedded apostrophes / double quotes
// don't break the AppleScript string literal.
func runOSAScript(ctx context.Context, title, message string) error {
	cmd := exec.CommandContext(ctx, "osascript", "-e",
		fmt.Sprintf(`display notification %s with title %s`,
			osascriptString(message), osascriptString(title)))
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("osascript: %w", err)
	}
	return nil
}

// osascriptString single-quotes the value the AppleScript way:
// escape backslashes + double quotes, then wrap in double quotes.
// Single quotes inside AppleScript strings are literal.
func osascriptString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

// runNotifySend uses libnotify's notify-send. Most distros ship it;
// if missing we silently fall back to bell-only.
func runNotifySend(ctx context.Context, title, message string) error {
	if _, err := exec.LookPath("notify-send"); err != nil {
		return nil // not installed — bell already rang
	}
	cmd := exec.CommandContext(ctx, "notify-send",
		"--app-name=biu",
		"--expire-time=8000",
		title, message)
	return cmd.Run()
}

// runWindowsToast tries BurntToast first (modern Win10/11 toasts);
// falls back to console-bell-only when not present. We deliberately
// skip the PowerShell `[Reflection.Assembly]::LoadWithPartialName`
// path because it's flaky on PowerShell 7+ and confuses AV.
func runWindowsToast(ctx context.Context, title, message string) error {
	psPath, err := exec.LookPath("pwsh")
	if err != nil {
		psPath, err = exec.LookPath("powershell")
		if err != nil {
			return nil
		}
	}
	script := fmt.Sprintf(
		`if (Get-Module -ListAvailable -Name BurntToast) { Import-Module BurntToast; New-BurntToastNotification -Text "%s","%s" }`,
		psEscape(title), psEscape(message))
	cmd := exec.CommandContext(ctx, psPath, "-NoProfile", "-Command", script)
	return cmd.Run()
}

// psEscape doubles double-quotes for embedding in a PowerShell
// double-quoted string. Backslashes are literal in PowerShell.
func psEscape(s string) string {
	return strings.ReplaceAll(s, `"`, `""`)
}
