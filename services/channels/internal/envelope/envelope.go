// Package envelope defines the canonical cross-platform message shape.
//
// Every channel driver normalizes its native payload onto Envelope before
// the router sees it. Outbound flow is the same in reverse: the router
// hands an Envelope to a driver's Send(), and the driver translates it
// to the platform's wire format.
//
// Design goal: the router and any agent that reads/writes Envelopes
// should NEVER need to know which platform a message came from. If they
// do, that's a leak — file a bug, add a missing field here, don't fork
// the router.
package envelope

import "time"

// Direction tells the receiver what side of the conversation this is.
const (
	DirectionInbound  = "inbound"  // from platform → us
	DirectionOutbound = "outbound" // from us → platform
)

// Envelope is the canonical message. Treat fields as additive: never
// repurpose, only extend. Persisted forms (logs, replays) must round-trip
// unknown fields untouched.
type Envelope struct {
	// MessageID is platform-issued where possible; otherwise driver-issued.
	// Stable identity for de-dupe and reply threading.
	MessageID string `json:"message_id"`

	// Channel — platform identifier ("telegram", "discord", "slack",
	// "feishu", "email", …). Used by the router to pick a driver for
	// outbound replies.
	Channel string `json:"channel"`

	// Direction — inbound or outbound (see constants above).
	Direction string `json:"direction"`

	// ConversationID — driver-defined opaque handle that identifies the
	// thread/chat/channel. The driver MUST be able to reply to this id
	// without any additional state.
	ConversationID string `json:"conversation_id"`

	// Sender — who composed this message. Empty for outbound messages
	// whose sender is the BiuMind agent itself.
	Sender Sender `json:"sender,omitempty"`

	// Text — primary user-visible content. May be empty if the
	// message is purely attachments.
	Text string `json:"text,omitempty"`

	// Attachments — files / images / inline media keyed by mime type.
	// Drivers stuff platform-specific URLs into URL; downloading is the
	// caller's responsibility.
	Attachments []Attachment `json:"attachments,omitempty"`

	// ReplyTo — message id this is in reply to (threading).
	ReplyTo string `json:"reply_to,omitempty"`

	// SentAt — when the message was authored, in UTC.
	SentAt time.Time `json:"sent_at"`

	// Raw — driver's native payload, preserved verbatim so we never
	// lose information just because we haven't taught the router about
	// a new field.
	Raw map[string]any `json:"raw,omitempty"`
}

type Sender struct {
	// PlatformID — channel-scoped user id (telegram numeric uid,
	// slack U…, discord snowflake, email address).
	PlatformID string `json:"platform_id"`
	// DisplayName — human-readable label.
	DisplayName string `json:"display_name,omitempty"`
	// Bot — true when sender is a bot account on the source platform.
	Bot bool `json:"bot,omitempty"`
}

type Attachment struct {
	URL      string `json:"url"`
	MimeType string `json:"mime_type,omitempty"`
	Bytes    int64  `json:"bytes,omitempty"`
	Filename string `json:"filename,omitempty"`
}
