// /share slash — write a snapshot of the current session to a
// shareable file + (when the platform supports it) copy the path
// to the clipboard.
//
// The "share" intent is usually:
//
//   1. user wants to send the agent's transcript to a colleague
//   2. user wants a permalink-shaped artifact for a bug report
//
// (1) is local — write a .md to the OS tmp dir + copy that path
// to the clipboard so the user can drag it into Slack / email.
// (2) would need an upload service biu doesn't have; we surface
// the local path and suggest using `gh gist create` for an
// inline-shareable URL.

package repl

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/internal/session"
)

// handleShare exports the current session as markdown into the OS
// tmp dir under a unique name, then copies the resulting path to
// the clipboard when the OS provides a clipboard CLI.
//
// Args (all optional):
//
//	/share                  — markdown to /tmp + clipboard
//	/share json             — JSON instead of markdown
//	/share <abs-path>       — write to <abs-path> instead of tmp
func (m model) handleShare(parts []string) string {
	if m.sessionLog == nil {
		return "/share: no session writer attached — nothing to share"
	}

	format := session.FormatMarkdown
	ext := "md"
	dest := ""

	for _, arg := range parts[1:] {
		switch strings.ToLower(arg) {
		case "json":
			format = session.FormatJSON
			ext = "json"
		case "md", "markdown":
			format = session.FormatMarkdown
			ext = "md"
		case "anthropic-replay", "replay":
			format = session.FormatAnthropicReplay
			ext = "json"
		default:
			// Treat anything else as a target path. Most-specific
			// arg wins so `/share json /tmp/x.json` works.
			dest = arg
		}
	}

	if dest == "" {
		stamp := time.Now().UTC().Format("20060102-150405")
		dest = filepath.Join(os.TempDir(),
			fmt.Sprintf("biu-session-%s.%s", stamp, ext))
	}

	srcPath := m.sessionLog.Path()
	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return "/share: open output: " + err.Error()
	}
	defer out.Close()

	n, err := session.Export(srcPath, out, session.ExportOptions{
		Format:            format,
		IncludeToolOutput: true,
	})
	if err != nil {
		return "/share: " + err.Error()
	}

	var b strings.Builder
	fmt.Fprintf(&b, "/share: wrote %d bytes to %s\n", n, dest)
	if copied := copyToClipboard(dest); copied != "" {
		fmt.Fprintf(&b, "  ✓ path copied to clipboard via %s\n", copied)
	} else {
		b.WriteString("  (no clipboard tool detected — paste the path above)\n")
	}
	b.WriteString(`
For an inline-shareable link, pipe through gh gist:
  gh gist create ` + dest + ` --public --desc 'biu session'
That returns a URL you can paste anywhere.`)
	return strings.TrimRight(b.String(), "\n")
}

// copyToClipboard runs the platform's clipboard CLI with `path` on
// stdin. Returns the tool name on success, "" when no tool was
// found / the copy failed. We don't import a clipboard library —
// pkg-level deps for one feature aren't worth it.
func copyToClipboard(content string) string {
	candidates := clipboardCandidates()
	for _, c := range candidates {
		if _, err := exec.LookPath(c.cmd); err != nil {
			continue
		}
		cmd := exec.Command(c.cmd, c.args...)
		cmd.Stdin = strings.NewReader(content)
		if err := cmd.Run(); err == nil {
			return c.cmd
		}
	}
	return ""
}

type clipboardTool struct {
	cmd  string
	args []string
}

// clipboardCandidates returns OS-appropriate copy tools, most-
// preferred first. macOS pbcopy is in-box; Linux distros split
// across xclip / xsel / wl-copy depending on Wayland/X11; Windows
// uses PowerShell Set-Clipboard via clip.exe.
func clipboardCandidates() []clipboardTool {
	switch runtime.GOOS {
	case "darwin":
		return []clipboardTool{{cmd: "pbcopy"}}
	case "linux":
		return []clipboardTool{
			{cmd: "wl-copy"},
			{cmd: "xclip", args: []string{"-selection", "clipboard"}},
			{cmd: "xsel", args: []string{"--clipboard", "--input"}},
		}
	case "windows":
		return []clipboardTool{{cmd: "clip"}}
	}
	return nil
}
