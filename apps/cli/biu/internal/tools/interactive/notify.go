// PushNotification — best-effort desktop / mobile notification.
//
// Backend is pluggable so the REPL can wire its own (terminal bell,
// macOS osascript, notify-send on Linux, BiuMind mobile push, etc.).
// When no backend is configured we just record the message into the
// engine's progress stream so the REPL can render an inline banner.

package interactive

import (
	"context"
	"strings"

	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
)

// Notifier is the contract a desktop/mobile notification backend
// implements. ctx cancellation is honoured.
type Notifier interface {
	Notify(ctx context.Context, message string) error
}

type PushNotificationTool struct {
	Notifier Notifier
}

func (PushNotificationTool) Name() string { return "PushNotification" }

func (PushNotificationTool) Description(_ map[string]any) string {
	return "Send a brief desktop / mobile notification to the user. " +
		"Use sparingly — only for status changes the user genuinely " +
		"needs to know now (build done, prompt waiting). Keep messages " +
		"under 200 chars."
}

func (PushNotificationTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"message": map[string]any{"type": "string"},
		},
		"required": []string{"message"},
	}
}

func (PushNotificationTool) IsReadOnly(_ map[string]any) bool        { return true }
func (PushNotificationTool) IsDestructive(_ map[string]any) bool     { return false }
func (PushNotificationTool) IsConcurrencySafe(_ map[string]any) bool { return true }
func (PushNotificationTool) InterruptBehavior() string               { return "cancel" }

func (p PushNotificationTool) Call(ctx context.Context, input map[string]any, env *engine.ToolEnv) (*engine.ToolResultPayload, error) {
	msg, _ := input["message"].(string)
	if strings.TrimSpace(msg) == "" {
		return softErr("PushNotification", "message required"), nil
	}
	if len(msg) > 240 {
		msg = msg[:240]
	}
	if env != nil && env.OnProgress != nil {
		env.OnProgress(engine.ProgressData{"kind": "notification", "message": msg})
	}
	if p.Notifier != nil {
		if err := p.Notifier.Notify(ctx, msg); err != nil {
			return softErr("PushNotification", err.Error()), nil
		}
		return text("Notification sent."), nil
	}
	return text("Notification queued (no desktop backend; in-app banner only)."), nil
}
