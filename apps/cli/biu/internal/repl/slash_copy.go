// /copy slash — copy the most recent assistant message to the
// clipboard.
//
// Common pattern: the model produced a
// useful snippet (PR description, commit message, code) and the
// user wants it in their clipboard for paste-elsewhere. Without
// this slash, the user manually selects + copies in the terminal,
// which loses formatting + has to dodge the prompt characters.
//
// Sub-forms:
//
//	/copy             — copy the last assistant text turn
//	/copy code        — copy the last fenced code block in the last
//	                    assistant turn (most-relevant code is
//	                    usually the last block emitted)
//	/copy <pattern>   — substring search through recent assistant
//	                    turns, copy the first match's full body

package repl

import (
	"fmt"
	"strings"

	"github.com/biumind/biumind/apps/cli/biu/internal/client"
)

func (m model) handleCopy(parts []string) string {
	body, label := m.findCopyBody(parts)
	if body == "" {
		return "/copy: nothing to copy — " + label
	}

	if used := copyToClipboard(body); used != "" {
		// Show a preview of what was copied so the user can confirm
		// without checking the clipboard.
		preview := strings.SplitN(body, "\n", 2)[0]
		if len(preview) > 80 {
			preview = preview[:79] + "…"
		}
		return fmt.Sprintf("/copy: %d chars → clipboard via %s\n  preview: %s",
			len(body), used, preview)
	}
	// No clipboard tool — fall back to printing so the user can
	// terminal-select it.
	if len(body) > 4000 {
		body = body[:4000] + "\n\n[…truncated; use /export <path> for the full content]"
	}
	return "/copy: no clipboard tool detected — body below:\n\n" + body
}

// findCopyBody returns the text the user wants copied + a human
// label describing the selection. Empty body means "no match".
func (m model) findCopyBody(parts []string) (string, string) {
	mode := ""
	pattern := ""
	if len(parts) > 1 {
		mode = strings.ToLower(parts[1])
	}
	if mode == "" {
		// Default: last assistant text.
		if t := lastAssistantText(m.history); t != "" {
			return t, ""
		}
		return "", "no assistant message in current history"
	}
	if mode == "code" {
		if t := lastAssistantText(m.history); t != "" {
			if code := lastFencedCode(t); code != "" {
				return code, ""
			}
			return "", "the last assistant message has no fenced code block"
		}
		return "", "no assistant message in current history"
	}
	pattern = strings.Join(parts[1:], " ")
	if t := findAssistantContaining(m.history, pattern); t != "" {
		return t, ""
	}
	return "", fmt.Sprintf("no assistant message contains %q", pattern)
}

// lastAssistantText scans history newest-first for the most recent
// assistant body. The REPL's history is the LLM-shaped flat
// {Role,Content} — no per-block discrimination — so we just
// return the Content directly.
func lastAssistantText(history []client.Message) string {
	for i := len(history) - 1; i >= 0; i-- {
		m := history[i]
		if m.Role != "assistant" {
			continue
		}
		if t := strings.TrimSpace(m.Content); t != "" {
			return t
		}
	}
	return ""
}

// findAssistantContaining returns the first assistant body
// (newest-first) whose contents include needle (case-insensitive).
func findAssistantContaining(history []client.Message, needle string) string {
	low := strings.ToLower(needle)
	for i := len(history) - 1; i >= 0; i-- {
		m := history[i]
		if m.Role != "assistant" {
			continue
		}
		if strings.Contains(strings.ToLower(m.Content), low) {
			return strings.TrimSpace(m.Content)
		}
	}
	return ""
}

// lastFencedCode extracts the last ```…``` block from text. Picks
// the LAST block because the model usually closes a turn with the
// answer code; earlier blocks tend to be discussion / examples.
//
// Returns the inner content (no fences). Empty string when no
// well-formed block is found.
func lastFencedCode(text string) string {
	const fence = "```"
	idx := strings.LastIndex(text, fence)
	if idx < 0 {
		return ""
	}
	openIdx := strings.LastIndex(text[:idx], fence)
	if openIdx < 0 {
		return ""
	}
	body := text[openIdx+len(fence) : idx]
	// Strip the language tag that follows the opening fence
	// ("```go\n…"). Identify the first newline; everything before
	// it on the same line is the tag.
	if nl := strings.IndexByte(body, '\n'); nl > 0 {
		body = body[nl+1:]
	}
	return strings.TrimRight(body, "\n")
}
