// Permission approval dialog — numbered option list with arrow +
// digit-key navigation, full (never truncated) command display,
// danger annotations, and deny-with-feedback.
//
// Design contract:
//
//   - The user must see EXACTLY what they're approving: Bash commands
//     render in full (multi-line preserved); truncation is for the
//     transcript, never for the approval dialog.
//   - Every option states its scope in plain words ("this exact
//     command", "this session") — no bare "always" whose granularity
//     the user has to guess.
//   - Three ways to pick: ↑/↓ + Enter, digit keys 1..N, and the
//     legacy hotkeys (a / shift+a / d / q, plus suggestion hotkeys).
//   - Esc denies the current ask (the turn continues — the model
//     adapts). q denies AND aborts the stream.
//   - "Deny with feedback" opens an inline input (the main textarea,
//     same pattern as the question panel); the text rides along in
//     PermissionAnswer.Feedback so the model learns why.

package repl

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
	sdkproto "github.com/biumind/biumind/packages/go-sdk/biu/sdkproto/v1"
)

// ─── Options ──────────────────────────────────────────────────

type permOptionKind int

const (
	permOptAllowOnce permOptionKind = iota
	permOptAllowSession
	permOptSuggestion
	permOptDeny
	permOptDenyFeedback
)

type permOption struct {
	kind  permOptionKind
	label string
	sg    *engine.AskSuggestion // non-nil only for permOptSuggestion
}

// permissionOptions builds the numbered list in display order:
// allow-once, allow-for-session, runner-computed suggestions, deny,
// deny-with-feedback.
func permissionOptions(ask *engine.PermissionAskEvent) []permOption {
	opts := []permOption{
		{kind: permOptAllowOnce, label: "Allow once"},
		{kind: permOptAllowSession, label: alwaysAllowLabel(ask)},
	}
	for i := range ask.Suggestions {
		sg := ask.Suggestions[i]
		if sg.HotKey == "" || sg.Label == "" {
			continue
		}
		opts = append(opts, permOption{kind: permOptSuggestion, label: sg.Label, sg: &ask.Suggestions[i]})
	}
	return append(opts,
		permOption{kind: permOptDeny, label: "Deny"},
		permOption{kind: permOptDenyFeedback, label: "Deny with feedback"},
	)
}

// alwaysAllowLabel states the remember-granularity honestly, matching
// what permissions.SessionGrantKey actually records: Bash → the exact
// command string; Edit/Write → the exact path; anything else → the
// whole tool. Grants are session-scoped (perms.Grant), never
// persisted, so the label says so.
func alwaysAllowLabel(ask *engine.PermissionAskEvent) string {
	switch strings.ToLower(ask.ToolName) {
	case "bash":
		if cmd, _ := ask.Input["command"].(string); cmd != "" {
			return "Always allow this exact command (this session)"
		}
	case "edit", "write":
		for _, k := range []string{"file_path", "path"} {
			if p, _ := ask.Input[k].(string); p != "" {
				return fmt.Sprintf("Always allow %s on %s (this session)",
					strings.ToLower(ask.ToolName)+"ing", filepath.Base(p))
			}
		}
	}
	return fmt.Sprintf("Always allow %s (this session)", ask.ToolName)
}

// ─── Danger annotation ────────────────────────────────────────

// dangerPatterns flags shell commands that deserve an explicit
// warning line above the options. Presentation-only — the permission
// decision itself is unaffected.
var dangerPatterns = []struct {
	re    *regexp.Regexp
	label string
}{
	{regexp.MustCompile(`\brm\s+[^|;&]*-[a-zA-Z]*[rf][a-zA-Z]*[rf]`), "recursive forced delete"},
	{regexp.MustCompile(`\brm\s+[^|;&]*-[a-zA-Z]*r`), "recursive delete"},
	{regexp.MustCompile(`\bsudo\b`), "runs as root (sudo)"},
	{regexp.MustCompile(`\b(curl|wget)\b[^|]*\|\s*(sudo\s+)?(bash|sh|zsh)\b`), "pipes remote script into a shell"},
	{regexp.MustCompile(`\bdd\b[^|;&]*\bof=/dev/`), "writes to a raw device"},
	{regexp.MustCompile(`\bmkfs\b`), "formats a filesystem"},
	{regexp.MustCompile(`\bchmod\s+(-[a-zA-Z]+\s+)*777\b`), "world-writable permissions (chmod 777)"},
	{regexp.MustCompile(`:\(\)\s*\{`), "fork bomb"},
	{regexp.MustCompile(`\bgit\s+push\b[^|;&]*(--force|\s-f\b)`), "force push"},
}

// dangerWarning returns a short label when a Bash command matches a
// dangerous pattern, "" otherwise.
func dangerWarning(ask *engine.PermissionAskEvent) string {
	if !strings.EqualFold(ask.ToolName, "bash") {
		return ""
	}
	cmd, _ := ask.Input["command"].(string)
	if cmd == "" {
		return ""
	}
	for _, d := range dangerPatterns {
		if d.re.MatchString(cmd) {
			return d.label
		}
	}
	return ""
}

// ─── Rendering ────────────────────────────────────────────────

// renderPermissionAsk produces the approval dialog shown above the
// input box while m.permissionAsk is non-nil.
//
// Layout:
//
//	⚠ Permission required (sub-agent)
//	  Bash
//	    $ <full command, every line>
//	    · continuation line
//	  ⚠ Dangerous: <label>            (only when matched)
//	  <reason>
//	  ▶ 1. Allow once
//	    2. Always allow this exact command (this session)
//	    3. Deny
//	    4. Deny with feedback
//	  ↑/↓ select · 1-4 choose · enter confirm · esc deny · q abort · +N queued
func renderPermissionAsk(m model) string {
	ask := m.permissionAsk
	if ask == nil {
		return ""
	}
	var b strings.Builder

	header := "⚠ Permission required"
	if ask.ParentToolUseID() != "" {
		header += " (sub-agent)"
	}
	b.WriteString(styleErrorPrefix.Render(header))
	b.WriteString("\n  ")
	b.WriteString(ask.ToolName)

	// Full input — never truncated. Bash commands get a "$" prompt
	// line per source line; other tools fall back to their most
	// interesting arg (path/url/query) at full length.
	if cmd, _ := ask.Input["command"].(string); cmd != "" {
		for i, line := range strings.Split(cmd, "\n") {
			prefix := "$ "
			if i > 0 {
				prefix = "· "
			}
			b.WriteString("\n    ")
			b.WriteString(styleToolDesc.Render(prefix))
			b.WriteString(line)
		}
		if desc, _ := ask.Input["description"].(string); desc != "" {
			b.WriteString("\n    ")
			b.WriteString(styleToolDesc.Render(desc))
		}
	} else if s := fullInputSummary(ask.Input); s != "" {
		b.WriteString("\n    ")
		b.WriteString(styleToolDesc.Render(s))
	}

	if warn := dangerWarning(ask); warn != "" {
		b.WriteString("\n  ")
		b.WriteString(styleWarn.Render("⚠ Dangerous: " + warn))
	}

	b.WriteString("\n  ")
	b.WriteString(styleHint.Render(ask.Reason))

	opts := permissionOptions(ask)
	for i, opt := range opts {
		b.WriteString("\n  ")
		row := fmt.Sprintf("%d. %s", i+1, opt.label)
		if opt.sg != nil {
			row += fmt.Sprintf("  [%s]", opt.sg.HotKey)
		}
		if i == m.permissionCursor {
			b.WriteString(stylePermSelected.Render("▶ " + row))
		} else {
			b.WriteString("  " + row)
		}
	}

	b.WriteString("\n  ")
	switch {
	case m.permissionTyping:
		b.WriteString(styleHint.Render("Type feedback · enter submit · esc back"))
	default:
		hint := fmt.Sprintf("↑/↓ select · 1-%d choose · enter confirm · esc deny · q abort", len(opts))
		if n := len(m.permissionQueue); n > 0 {
			hint += fmt.Sprintf(" · +%d queued", n)
		}
		b.WriteString(styleHint.Render(hint))
	}
	return b.String()
}

// fullInputSummary is summariseInput without the 80-char cut: the
// approval dialog must show the full path/url/query.
func fullInputSummary(input map[string]any) string {
	for _, k := range []string{"file_path", "path", "pattern", "url", "query"} {
		if v, ok := input[k]; ok {
			return fmt.Sprintf("%v", v)
		}
	}
	return summariseInput(input)
}

// ─── Key handling ─────────────────────────────────────────────

// handlePermissionKey routes keys while the permission dialog is up.
// Reached from model.handleKey; the dialog steals ALL keys until the
// ask is answered.
func (m model) handlePermissionKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	ask := m.permissionAsk
	if ask == nil {
		return m, nil
	}

	// Feedback typing mode: textarea captures the deny reason.
	if m.permissionTyping {
		switch msg.String() {
		case "esc":
			m.permissionTyping = false
			m.textarea.Reset()
			m.textarea.Placeholder = ""
			m.refreshBody()
			return m, nil
		case "enter":
			text := strings.TrimSpace(m.textarea.Value())
			m.permissionTyping = false
			m.textarea.Reset()
			m.textarea.Placeholder = ""
			ask.Decision <- engine.PermissionAnswer{Decision: engine.PermDeny, Feedback: text}
			m.advancePermissionAsk()
			m.refreshBody()
			return m, nil
		}
		var cmd tea.Cmd
		m.textarea, cmd = m.textarea.Update(msg)
		return m, cmd
	}

	opts := permissionOptions(ask)
	key := msg.String()

	// Legacy suggestion hotkeys (e.g. "w" for add-working-dir) fire
	// their option directly, ahead of navigation.
	for _, opt := range opts {
		if opt.kind == permOptSuggestion && opt.sg.HotKey == key {
			return m.pickPermissionOption(opt)
		}
	}

	switch key {
	case "up", "ctrl+p", "k":
		m.permissionCursor = (m.permissionCursor - 1 + len(opts)) % len(opts)
		m.refreshBody()
		return m, nil
	case "down", "ctrl+n", "j":
		m.permissionCursor = (m.permissionCursor + 1) % len(opts)
		m.refreshBody()
		return m, nil
	case "enter":
		return m.pickPermissionOption(opts[m.permissionCursor])
	case "a":
		return m.pickPermissionOption(opts[0])
	case "A", "shift+a", "s":
		return m.pickPermissionOption(opts[1])
	case "d", "esc":
		// Deny the current ask; the turn continues and the model
		// adapts. (Esc matches the deny idiom; q below aborts.)
		ask.Decision <- engine.PermissionAnswer{Decision: engine.PermDeny}
		m.advancePermissionAsk()
		m.refreshBody()
		return m, nil
	case "q":
		ask.Decision <- engine.PermissionAnswer{Decision: engine.PermDeny}
		m.permissionAsk = nil
		m.denyQueuedAsks()
		if m.streamCancel != nil {
			m.streamCancel()
		}
		m.refreshBody()
		return m, nil
	default:
		// Digit keys 1..N pick directly.
		if len(key) == 1 && key[0] >= '1' && key[0] <= '9' {
			if idx := int(key[0] - '1'); idx < len(opts) {
				return m.pickPermissionOption(opts[idx])
			}
		}
		// Everything else: swallow until decided.
		return m, nil
	}
}

// pickPermissionOption answers the current ask per the chosen option.
func (m model) pickPermissionOption(opt permOption) (tea.Model, tea.Cmd) {
	ask := m.permissionAsk
	switch opt.kind {
	case permOptAllowOnce:
		ask.Decision <- engine.PermissionAnswer{Decision: engine.PermAllow}
		m.advancePermissionAsk()
	case permOptAllowSession:
		ask.Decision <- engine.PermissionAnswer{
			Decision: engine.PermAllow, Remember: true,
		}
		m.advancePermissionAsk()
	case permOptSuggestion:
		ask.Decision <- engine.PermissionAnswer{
			Decision:       engine.PermAllow,
			AppliedUpdates: []sdkproto.PermissionUpdate{opt.sg.Update},
		}
		m.advancePermissionAsk()
	case permOptDeny:
		ask.Decision <- engine.PermissionAnswer{Decision: engine.PermDeny}
		m.advancePermissionAsk()
	case permOptDenyFeedback:
		m.permissionTyping = true
		m.textarea.Reset()
		m.textarea.Placeholder = "Tell the model what to do instead · Enter submits · Esc goes back"
		m.textarea.Focus()
	}
	m.refreshBody()
	return m, nil
}
