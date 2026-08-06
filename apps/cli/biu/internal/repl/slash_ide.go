// /ide slash — IDE bridge status + setup instructions.
//
// biu's bridge (internal/bridge) exposes
// a tiny HTTP/SSE surface so editor extensions (VS Code, JetBrains)
// can drive the engine out of process. The slash surfaces:
//
//   - Whether the bridge is running this session
//   - Where to point an IDE extension (host:port + optional auth)
//   - How to start it from a fresh shell when not running
//
// Read-only — actually starting the bridge happens via `biu bridge`
// CLI subcommand, since the REPL owns the TTY and can't trivially
// run a long-lived HTTP server in the same process. The slash is
// pure inspection + guidance.

package repl

import (
	"fmt"
	"os"
	"strings"
)

// handleIDE renders the IDE bridge state. Reads BIU_BRIDGE_URL /
// BIU_BRIDGE_TOKEN env vars to detect "bridge running and our REPL
// knows about it" — the bridge sets these when it spawns a child
// REPL, and IDE extensions read them too.
func (m model) handleIDE(parts []string) string {
	bridgeURL := strings.TrimSpace(os.Getenv("BIU_BRIDGE_URL"))
	if bridgeURL == "" {
		return renderIDENotRunning()
	}
	return renderIDERunning(bridgeURL)
}

func renderIDENotRunning() string {
	return strings.TrimSpace(`
/ide: bridge not running in this session.

Start it from a fresh shell so the bridge owns its own port + lifecycle:

  biu bridge --addr :7173                # bind locally only
  biu bridge --addr :7173 --token <hex>  # require Bearer auth

Then point your IDE extension at http://127.0.0.1:7173. The bridge speaks
the same /v1/code/sessions surface as Claude Code; existing Claude Code IDE
extensions ought to work without modification (file an issue if they
don't).

Inside this REPL, the slash is read-only — starting the bridge here
would tie its lifetime to your interactive session, which isn't what
you want for a daemon. Keep the bridge in its own terminal.
`)
}

func renderIDERunning(url string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "/ide: bridge endpoint is %s\n", url)
	if tok := os.Getenv("BIU_BRIDGE_TOKEN"); tok != "" {
		// Don't echo the full token — show prefix + length so users
		// can confirm IDE config without leaking the secret to the
		// transcript / clipboard.
		fmt.Fprintf(&b, "  auth:     bearer (length %d, starts with %q)\n",
			len(tok), tok[:minInt(8, len(tok))])
	} else {
		b.WriteString("  auth:     none — bridge accepts unauthenticated requests on this address\n")
	}
	fmt.Fprintln(&b, "  endpoints:")
	fmt.Fprintln(&b, "    POST /v1/code/sessions")
	fmt.Fprintln(&b, "    POST /v1/code/sessions/:id/messages")
	fmt.Fprintln(&b, "    GET  /v1/code/sessions/:id/events")
	fmt.Fprintln(&b, "    GET  /v1/code/sessions/:id/cost")
	fmt.Fprintln(&b, "    POST /v1/code/sessions/:id/compact")
	fmt.Fprintln(&b, "    DELETE /v1/code/sessions/:id")
	b.WriteString("\nPoint your IDE extension at the URL above.")
	return b.String()
}

// minInt returns the smaller of two ints. Defined here rather than
// pulling in the stdlib math package because we only need it once
// for the auth-token preview.
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
