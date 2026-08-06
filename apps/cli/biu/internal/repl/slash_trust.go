// /trust slash. Manages persistent + session-only directory
// grants the trust gate consults.

package repl

import (
	"fmt"
	"os"
	"strings"

	"github.com/biumind/biumind/apps/cli/biu/internal/trust"
)

// handleTrust dispatches /trust subcommands.
//
//   /trust                — show current cwd state + persisted list
//   /trust here           — persist trust for the current cwd
//   /trust session        — trust cwd in-memory (this session only)
//   /trust add <path>     — persist trust for an explicit path
//   /trust remove <path>  — revoke a persistent grant
//
// Untrusted directories block status-line scripts from running. We
// don't auto-prompt on first launch (would need modal UI work) —
// the user runs `/trust here` when they're ready to grant.
func (m model) handleTrust(parts []string) string {
	if m.trust == nil {
		return "/trust: trust gate not enabled (legacy mode trusts every directory)"
	}
	sub := ""
	if len(parts) > 1 {
		sub = parts[1]
	}
	cwd, _ := os.Getwd()

	switch sub {
	case "":
		return renderTrustStatus(m.trust, cwd)
	case "here":
		stored, err := m.trust.Trust(cwd)
		if err != nil {
			return "/trust: " + err.Error()
		}
		return "/trust: persisted " + stored
	case "session":
		stored, err := m.trust.TrustForSession(cwd)
		if err != nil {
			return "/trust: " + err.Error()
		}
		return "/trust: trusted " + stored + " for this session only"
	case "add":
		if len(parts) < 3 {
			return "/trust: usage: /trust add <path>"
		}
		stored, err := m.trust.Trust(parts[2])
		if err != nil {
			return "/trust: " + err.Error()
		}
		return "/trust: persisted " + stored
	case "remove":
		if len(parts) < 3 {
			return "/trust: usage: /trust remove <path>"
		}
		if err := m.trust.Untrust(parts[2]); err != nil {
			return "/trust: " + err.Error()
		}
		return "/trust: revoked " + parts[2]
	default:
		return "/trust: usage: /trust [here|session|add <path>|remove <path>]"
	}
}

// renderTrustStatus is the bare-/trust output: current cwd state +
// persistent + session lists. Designed so a user diagnosing "why
// isn't my status-line script running" sees the gate explicitly.
func renderTrustStatus(s *trust.Store, cwd string) string {
	var b strings.Builder
	state := "untrusted"
	if s.IsTrusted(cwd) {
		state = "trusted"
	}
	fmt.Fprintf(&b, "current directory: %s\n  → %s\n", cwd, state)
	if state == "untrusted" {
		b.WriteString("  (status-line scripts and shell hooks are blocked here.\n" +
			"   Run `/trust here` to persist; `/trust session` for this session only.)\n")
	}
	persistent := s.List()
	session := s.SessionList()
	if len(persistent) == 0 && len(session) == 0 {
		b.WriteString("\nno persisted or session grants.")
		return b.String()
	}
	if len(persistent) > 0 {
		fmt.Fprintf(&b, "\npersistent grants (%d):\n", len(persistent))
		for _, p := range persistent {
			fmt.Fprintf(&b, "  - %s\n", p)
		}
	}
	if len(session) > 0 {
		fmt.Fprintf(&b, "\nsession-only grants (%d):\n", len(session))
		for _, p := range session {
			fmt.Fprintf(&b, "  - %s\n", p)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}
