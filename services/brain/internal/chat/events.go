// Cross-device sync events (transactional outbox).
//
// Every chat write other devices must observe inserts a brain.events
// row IN THE SAME TRANSACTION as the business write — the same outbox
// pattern as wiki (internal/wiki/store). The brain_events LISTEN
// trigger + events.Listener/poller forward the row to the realtime
// service, which fans out per topic (= scope).
//
// Scope convention: chat:user:<user_id> — three-part colon form
// (dot-form topics fail topic parsing → 403, see the aigc precedent).
// Subscribe-side authz is Cedar self-only (00-system.cedar:
// resource.kind == "chat:user" && resource.id == principal.id).
//
// Privacy: threads with sync_enabled=false NEVER emit — their content
// must not travel any server-side channel (§3.5.3 toggle).
package chat

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Event types written to brain.events.event_type.
const (
	EventMessageCreated = "chat.message_created"
	EventThreadUpdated  = "chat.thread_updated"
	EventThreadDeleted  = "chat.thread_deleted"
)

// emitEvent inserts a brain.events row inside tx. Mirrors wiki
// store's emitEvent; kept chat-local so each domain owns its scope.
// Payloads carry ids only — never message content.
func emitEvent(ctx context.Context, tx pgx.Tx, userID uuid.UUID,
	eventType string, payload map[string]any,
) error {
	pl, _ := json.Marshal(payload)
	scope := fmt.Sprintf("chat:user:%s", userID)
	_, err := tx.Exec(ctx, `
		INSERT INTO brain.events (scope, actor_type, actor_id, event_type, payload)
		VALUES ($1, 'user', $2, $3, $4)
	`, scope, userID.String(), eventType, pl)
	return err
}

// terminalStatus reports whether a message status is a final,
// user-visible state. Mid-stream states (pending/processing/
// streaming) stay silent on the bus — the terminal INSERT/UPDATE is
// what other devices sync from.
func terminalStatus(s string) bool {
	switch s {
	case StatusSuccess, StatusError, StatusPaused:
		return true
	}
	return false
}

// emitMessageCreatedTx writes the chat.message_created outbox row for
// a message that just became visible to other devices: a terminal-
// status user/assistant row. tool/system roles and mid-stream
// placeholders stay silent (the terminal UPDATE announces those).
// sync_enabled=false threads never emit.
func emitMessageCreatedTx(ctx context.Context, tx pgx.Tx, m *Message) error {
	if m.Role != RoleUser && m.Role != RoleAssistant {
		return nil
	}
	if !terminalStatus(m.Status) {
		return nil
	}
	var syncEnabled bool
	if err := tx.QueryRow(ctx,
		`SELECT sync_enabled FROM chat.threads WHERE id = $1`,
		m.ThreadID).Scan(&syncEnabled); err != nil {
		return err
	}
	if !syncEnabled {
		return nil
	}
	return emitEvent(ctx, tx, m.UserID, EventMessageCreated, map[string]any{
		"thread_id":  m.ThreadID.String(),
		"message_id": m.ID.String(),
		"position":   m.Position,
		"role":       m.Role,
	})
}
