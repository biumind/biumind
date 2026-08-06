// Pseudo-tool registered in place of an MCP server's real tools when
// the server has signalled needs-auth (401 + Bearer challenge).
// Surfaces a `mcp__<server>__authenticate` entry so the model knows
// the server exists and can prompt the user toward a manual or
// (P20.49b) automated OAuth flow.
//
// Phase 1 (this file): the tool returns a structured guidance message
// — the captured challenge plus precise instructions for the user to
// either supply a token in cfg.Headers["Authorization"] or wait for
// the automated PKCE flow that lands in P20.49b.
//
// Phase 2 (P20.49b, follow-up): the Call body kicks off oauth.Login
// in a background goroutine, returns the authorization URL the
// moment it's available, and on completion calls SetOAuthTokens +
// Reconnect — at which point the real tools list lands and this
// pseudo-tool gets evicted by replaceServerTools.

package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
	"github.com/biumind/biumind/apps/cli/biu/internal/state"
)

// authPseudoTool is the engine.Tool surfaced for a needs-auth MCP
// server. The receiver carries the server name + the live HTTPClient
// reference so the (future) Call body can read the challenge and
// drive the OAuth flow.
type authPseudoTool struct {
	serverName string
	client     *HTTPClient
}

func (t authPseudoTool) Name() string {
	// Use the same `mcp__<server>__<tool>` namespace the real tools
	// will land in once auth completes — keeps the model's mental
	// model of "tools from this server" consistent.
	return QualifyName(t.serverName, "authenticate")
}

func (t authPseudoTool) Description(_ map[string]any) string {
	return fmt.Sprintf(
		"The `%s` MCP server is configured but the upstream rejected "+
			"requests with HTTP 401, signalling OAuth authentication is "+
			"required. Call this tool to inspect the challenge and get "+
			"step-by-step instructions for unblocking the connection.",
		t.serverName)
}

func (t authPseudoTool) InputSchema() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}

func (t authPseudoTool) IsReadOnly(_ map[string]any) bool        { return true }
func (t authPseudoTool) IsDestructive(_ map[string]any) bool     { return false }
func (t authPseudoTool) IsConcurrencySafe(_ map[string]any) bool { return true }
func (t authPseudoTool) InterruptBehavior() string               { return "cancel" }

func (t authPseudoTool) Call(ctx context.Context, _ map[string]any, _ *engine.ToolEnv) (*engine.ToolResultPayload, error) {
	if t.client == nil || t.client.AuthChallenge() == nil {
		// Defensive — shouldn't happen since the engine_adapter only
		// registers this tool when the client is in needs-auth.
		return softErr(t.Name(),
			"server is no longer in needs-auth state — try the real tool you wanted; "+
				"if you still get an auth error, run `biu mcp probe "+t.serverName+"` to refresh"), nil
	}

	// P20.49b automated path: cfg.OAuth populated → drive a PKCE
	// flow, return the authorize URL synchronously, finish the
	// exchange + reconnect in a background goroutine.
	if t.client.cfg.OAuth != nil {
		// Use a fresh context for the background flow so the LLM tool
		// call's per-call cancellation doesn't kill the goroutine
		// while the user is still on the consent screen. ctx (the
		// per-call ctx) is only consulted for surfacing the URL —
		// never carries beyond this function.
		_ = ctx
		authURL, err := startOAuthFlow(context.Background(), t.client)
		if err != nil {
			// Bad config — fall back to the manual-token guidance
			// since the user can still unblock with a static token.
			return t.manualGuidance(fmt.Sprintf(
				"\nNote: automatic OAuth flow couldn't start — %v\n"+
					"Falling back to manual-token instructions:\n", err)), nil
		}
		var b strings.Builder
		fmt.Fprintf(&b,
			"OAuth flow started for `%s`.\n\n"+
				"Ask the user to open this URL in their browser to authorize:\n\n"+
				"  %s\n\n"+
				"After the user completes consent the browser redirects to "+
				"http://127.0.0.1/callback, biu catches the code, exchanges it "+
				"for a token, and reconnects the MCP server automatically. The "+
				"real tools (mcp__%s__*) become available within a couple of "+
				"seconds. If the user closes the tab without consenting, run "+
				"this `authenticate` tool again to retry.\n",
			t.serverName, authURL, t.serverName)
		return &engine.ToolResultPayload{
			Content: []state.ContentBlock{{Type: state.ContentText, Text: b.String()}},
		}, nil
	}

	// Phase 1 fallback: cfg.OAuth absent → tell the user how to get
	// a token manually + how to opt into the automatic flow.
	return t.manualGuidance(""), nil
}

// manualGuidance renders the phase-1 "manual token" + "how to opt in
// to PKCE" message. Optional prefix is for callers that want to tack
// on a context line (e.g. "automatic flow couldn't start because …").
func (t authPseudoTool) manualGuidance(prefix string) *engine.ToolResultPayload {
	ch := t.client.AuthChallenge()

	var b strings.Builder
	if prefix != "" {
		b.WriteString(prefix)
	}
	fmt.Fprintf(&b,
		"The `%s` MCP server requires OAuth authentication.\n\n",
		t.serverName)

	if ch != nil {
		if ch.Realm != "" {
			fmt.Fprintf(&b, "Realm: %s\n", ch.Realm)
		}
		if ch.Scope != "" {
			fmt.Fprintf(&b, "Required scope(s): %s\n", ch.Scope)
		}
		if ch.ResourceMetadata != "" {
			fmt.Fprintf(&b,
				"OAuth resource metadata: %s\n"+
					"  (per RFC 9728; fetch this URL to discover the authorization_endpoint + token_endpoint)\n",
				ch.ResourceMetadata)
		}
		if ch.Raw != "" && ch.ResourceMetadata == "" && ch.Realm == "" {
			fmt.Fprintf(&b, "Raw challenge: %s\n", ch.Raw)
		}
	}

	b.WriteString("\nUnblocking the connection (two options):\n\n")
	b.WriteString(
		"1. **Manual token** (works today): obtain a Bearer token for this " +
			"resource server (typically by running its OAuth flow yourself in a " +
			"browser, or via a CI service-account token), then add it to the " +
			"server's [[mcp_servers]] entry in `~/.biu/config.toml`:\n\n" +
			"      [[mcp_servers]]\n" +
			"      name = \"" + t.serverName + "\"\n" +
			"      transport = \"http\"\n" +
			"      url = \"…\"\n" +
			"      headers = { Authorization = \"Bearer <your-token>\" }\n\n" +
			"   biu picks the static header up on the next start; no automated " +
			"PKCE flow needed.\n\n")
	b.WriteString(
		"2. **Automated PKCE flow** (P20.49b — opt-in): add an `[mcp_servers.oauth]` " +
			"subtable next to the server entry:\n\n" +
			"      [mcp_servers.oauth]\n" +
			"      client_id     = \"<your OAuth app client_id>\"\n" +
			"      authorize_url = \"<provider's /authorize endpoint>\"\n" +
			"      token_url     = \"<provider's /token endpoint>\"\n" +
			"      scopes        = [\"…\"]\n" +
			"      callback_port = 0   # 0 = ephemeral; pin if your provider requires it\n\n" +
			"   Then re-run biu and call this `authenticate` tool — biu returns " +
			"an authorization URL the user opens in their browser; on consent, " +
			"biu reconnects the server automatically and the pseudo-tool is " +
			"replaced by the real tools list.\n\n")
	b.WriteString(
		"After authorisation lands, run `biu mcp probe " + t.serverName +
			"` to verify the real tools list.\n")

	if flowErr := t.client.auth.FlowError(); flowErr != nil {
		fmt.Fprintf(&b,
			"\nLast OAuth flow attempt error: %v\n"+
				"(Surface to the user — they may have closed the browser tab "+
				"before consenting, or the provider rejected the request.)\n",
			flowErr)
	}

	return &engine.ToolResultPayload{
		Content: []state.ContentBlock{{Type: state.ContentText, Text: b.String()}},
	}
}
