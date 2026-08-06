// Brief — structured user-facing message tool (P20.56).
//
// Semantic contract:
//
//   - The model uses Brief INSTEAD OF plain assistant text when it
//     wants the message to appear with structured metadata (status,
//     attachments, sentAt timestamp). Useful for headless / SDK
//     callers that consume tool results as JSON rather than scraping
//     a chat UI.
//   - status="proactive" pings the desktop notifier (same backend
//     PushNotification uses) so a user away from the terminal sees
//     the message land. status="normal" is a regular reply — no ping.
//   - attachments is a list of file paths the user should see
//     alongside the message. The tool resolves them to absolute
//     paths + size/exists metadata so the UI can render thumbnails
//     or "open file" affordances.
//
// biu doesn't ship Brief as the *primary* communication channel (no
// assistant-mode gate); it's an OPT-IN structured-output tool the
// model can pick when it has a status-bearing message. The SDK
// consumer reads the structured payload via the tool result; the REPL
// renders it like any other tool result with a "📨" prefix.

package interactive

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
	"github.com/biumind/biumind/apps/cli/biu/internal/state"
)

const BriefToolName = "Brief"

// BriefTool is the structured-message variant of plain assistant
// text. Same Notifier backend as PushNotification — proactive briefs
// fire a desktop ping in addition to surfacing in the conversation.
type BriefTool struct {
	Notifier Notifier
}

func (BriefTool) Name() string { return BriefToolName }

func (BriefTool) Description(_ map[string]any) string {
	return "Send a structured message to the user. Prefer Brief over " +
		"plain assistant text when:\n" +
		"  - the message has a status change (\"build done\", \"hit a blocker\")\n" +
		"  - the message references files the user should see (logs, diffs)\n" +
		"  - you're surfacing something the user hasn't asked for and may " +
		"have walked away from the terminal\n\n" +
		"status='normal' is a regular reply (no notification ping).\n" +
		"status='proactive' fires a desktop notification — use sparingly, " +
		"only when the message is worth pulling the user back to.\n\n" +
		"Markdown allowed in `message`. `attachments` are paths the user " +
		"should see alongside the message; relative paths resolve against " +
		"cwd. Missing files are noted but don't error."
}

func (BriefTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"message": map[string]any{
				"type":        "string",
				"description": "Markdown-formatted message body for the user.",
			},
			"status": map[string]any{
				"type":        "string",
				"description": "'normal' for a reply; 'proactive' to ping the user (desktop notification).",
				"enum":        []any{"normal", "proactive"},
			},
			"attachments": map[string]any{
				"type":        "array",
				"description": "Optional file paths (absolute or relative to cwd). Files the user should see alongside the message.",
				"items":       map[string]any{"type": "string"},
			},
		},
		"required": []string{"message", "status"},
	}
}

// Brief is read-only from the model's perspective: it doesn't change
// the filesystem or external state (the notification fire is the
// closest thing to a side effect; we model that as advisory rather
// than destructive so the runner doesn't ask permission for every
// brief — the user has already opted into receiving notifications by
// configuring a Notifier backend).
func (BriefTool) IsReadOnly(_ map[string]any) bool        { return true }
func (BriefTool) IsDestructive(_ map[string]any) bool     { return false }
func (BriefTool) IsConcurrencySafe(_ map[string]any) bool { return true }
func (BriefTool) InterruptBehavior() string               { return "cancel" }

func (b BriefTool) Call(ctx context.Context, input map[string]any, env *engine.ToolEnv) (*engine.ToolResultPayload, error) {
	msg, _ := input["message"].(string)
	if strings.TrimSpace(msg) == "" {
		return softErr(BriefToolName, "message is required"), nil
	}
	status, _ := input["status"].(string)
	switch status {
	case "normal", "proactive":
		// ok
	case "":
		return softErr(BriefToolName, "status is required ('normal' or 'proactive')"), nil
	default:
		return softErr(BriefToolName,
			fmt.Sprintf("unknown status %q (want normal | proactive)", status)), nil
	}

	cwd := ""
	if env != nil {
		cwd = env.Cwd
	}
	attachments := resolveAttachments(input["attachments"], cwd)

	// Fire the proactive ping. Best-effort: notifier failure is
	// surfaced in the result body but doesn't fail the tool.
	notifyResult := ""
	if status == "proactive" && b.Notifier != nil {
		if err := b.Notifier.Notify(ctx, msg); err != nil {
			notifyResult = fmt.Sprintf("\n[notification failed: %v]", err)
		} else {
			notifyResult = "\n[notification sent]"
		}
	}

	// Render the structured payload. The text body is what the model
	// sees on next turn (its own message echoed back). The REPL UI
	// can pull additional structure via env.OnProgress with a "brief"
	// kind — same pattern PushNotification uses.
	if env != nil && env.OnProgress != nil {
		env.OnProgress(engine.ProgressData{
			"kind":        "brief",
			"status":      status,
			"message":     msg,
			"attachments": attachments,
		})
	}

	body := fmt.Sprintf("Brief delivered (status=%s, %d attachment%s) at %s.%s",
		status, len(attachments), pluralS(len(attachments)),
		time.Now().UTC().Format(time.RFC3339), notifyResult)
	if len(attachments) > 0 {
		body += "\nAttachments:\n"
		for _, a := range attachments {
			body += fmt.Sprintf("  - %s (%s)\n", a.Path, a.Note)
		}
	}
	return &engine.ToolResultPayload{
		Content: []state.ContentBlock{{Type: state.ContentText, Text: body}},
	}, nil
}

type briefAttachment struct {
	Path string // resolved absolute path (or original if it can't be resolved)
	Note string // "ok, 1234 bytes" / "missing" / "is a directory"
}

func resolveAttachments(raw any, cwd string) []briefAttachment {
	arr, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]briefAttachment, 0, len(arr))
	for _, v := range arr {
		p, ok := v.(string)
		if !ok || p == "" {
			continue
		}
		abs := p
		if !filepath.IsAbs(p) && cwd != "" {
			abs = filepath.Join(cwd, p)
		}
		st, err := os.Stat(abs)
		switch {
		case err != nil:
			out = append(out, briefAttachment{Path: abs, Note: "missing"})
		case st.IsDir():
			out = append(out, briefAttachment{Path: abs, Note: "is a directory"})
		default:
			out = append(out, briefAttachment{
				Path: abs,
				Note: fmt.Sprintf("ok, %d bytes", st.Size()),
			})
		}
	}
	return out
}

func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
