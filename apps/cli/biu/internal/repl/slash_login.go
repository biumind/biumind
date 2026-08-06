// /login + /logout slash — surface OAuth state from inside the REPL.
//
// biu's OAuth backend (internal/oauth) already handles the actual
// flow via
// `biu auth login` / `biu auth logout` CLI subcommands; the slash
// wraps state inspection + nudges the user toward the CLI when
// they need to do the OAuth dance.
//
// Why not run the OAuth flow inside the REPL: the flow needs to
// open a browser, listen on a local port, and pin a TTY for
// callback display — none of which mix well with bubbletea's
// owned-screen model. The CLI subcommand is the ergonomic surface;
// the slash exists so users don't have to /quit + run a separate
// command just to check who they're logged in as.

package repl

import (
	"fmt"
	"strings"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/internal/oauth"
)

// handleLogin surfaces the active OAuth token state. Read-only —
// actually starting a login flow happens via `biu auth login`.
func (m model) handleLogin(parts []string) string {
	store, err := oauth.NewStore("")
	if err != nil {
		return "/login: " + err.Error()
	}
	tokens, err := store.Load()
	if err != nil {
		return fmt.Sprintf("/login: load tokens: %v", err)
	}

	if tokens.AccessToken == "" {
		return "/login: not signed in.\n\n" +
			"Run from your shell:\n" +
			"  biu auth login                    # interactive OAuth\n" +
			"  biu auth login --manual           # paste-flow for headless boxes\n\n" +
			"After login, return to this REPL — the new tokens are picked up automatically."
	}

	var b strings.Builder
	b.WriteString("/login: signed in.\n")
	if tokens.Provider != "" {
		fmt.Fprintf(&b, "  provider:    %s\n", tokens.Provider)
	}
	if tokens.Scope != "" {
		fmt.Fprintf(&b, "  scope:       %s\n", tokens.Scope)
	}
	if tokens.TokenType != "" {
		fmt.Fprintf(&b, "  token type:  %s\n", tokens.TokenType)
	}
	if !tokens.ExpiresAt.IsZero() {
		left := time.Until(tokens.ExpiresAt).Round(time.Minute)
		if left < 0 {
			fmt.Fprintf(&b, "  expires:     %s ago — refresh required\n", -left)
		} else {
			fmt.Fprintf(&b, "  expires in:  %s (%s)\n",
				left, tokens.ExpiresAt.Local().Format(time.RFC3339))
		}
	}
	if tokens.Expired() {
		b.WriteString("\n  ! token expired or about to expire — biu refreshes lazily " +
			"on the next API call. Force re-login with: /logout && biu auth login.")
	}
	if tokens.RefreshToken == "" {
		b.WriteString("\n  ! no refresh token — when access expires you'll need to /logout " +
			"+ biu auth login again.")
	}
	return strings.TrimRight(b.String(), "\n")
}

// handleLogout deletes the stored token. The user has to re-run
// `biu auth login` to get back in. We don't try to revoke the
// token upstream — biu has no way to know which IdPs honour
// revocation, and most don't.
func (m model) handleLogout(parts []string) string {
	store, err := oauth.NewStore("")
	if err != nil {
		return "/logout: " + err.Error()
	}
	tokens, _ := store.Load()
	if tokens.AccessToken == "" {
		return "/logout: not signed in (nothing to do)"
	}
	if err := store.Delete(); err != nil {
		return "/logout: delete tokens: " + err.Error()
	}
	return "/logout: tokens deleted from local store. " +
		"Run `biu auth login` to sign in again. " +
		"Note: this does NOT revoke the token upstream — if the " +
		"refresh token was leaked, rotate it on the IdP side."
}
