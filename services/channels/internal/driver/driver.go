// Package driver — pluggable channel adapters.
//
// Each driver translates between its platform and the canonical
// [envelope.Envelope]. The interface is split so we can mount inbound
// webhook routes per-driver (signature verification varies) while still
// having one Send() flow.
package driver

import (
	"context"
	"errors"
	"net/http"

	"github.com/biumind/biumind/services/channels/internal/envelope"
)

type Driver interface {
	// Name — channel identifier matching envelope.Channel.
	Name() string

	// VerifyAndParse takes the raw inbound HTTP request, verifies any
	// platform signature header, parses the body, and returns one or
	// more Envelopes ready for the Router. Returns ErrUnsigned /
	// ErrSignatureInvalid for auth failures so the API layer can map
	// to the right HTTP status.
	VerifyAndParse(r *http.Request) ([]envelope.Envelope, error)

	// Send delivers an outbound Envelope through the platform's API.
	// Drivers populate the returned Envelope's MessageID with whatever
	// the platform issues (the caller uses it for ReplyTo threading).
	Send(ctx context.Context, e envelope.Envelope) (envelope.Envelope, error)
}

var (
	ErrUnsigned         = errors.New("channels: missing signature header")
	ErrSignatureInvalid = errors.New("channels: signature mismatch")
	ErrUnsupported      = errors.New("channels: unsupported channel")
)
