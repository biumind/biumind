// Package client wires biu CLI's three Provider implementations
// (model-relay / AnthropicDirect / OpenAIDirect) onto the canonical Provider
// interface from packages/go-sdk/biu/llm.
//
// Old API (Message / ChatRequest / Frame / Provider) is preserved via
// type aliases so existing call sites compile unchanged.
package client

import (
	"github.com/biumind/biumind/packages/go-sdk/biu/llm"
)

// Re-export SDK types — zero churn for existing callers.
type (
	Message     = llm.Message
	ChatRequest = llm.ChatRequest
	Frame       = llm.Frame
	FrameKind   = llm.FrameKind
	Provider    = llm.Provider
)

// Re-export constants.
const (
	KindDelta = llm.KindDelta
	KindStop  = llm.KindStop
	KindEnd   = llm.KindEnd
	KindError = llm.KindError
)

// Mode is the CLI-only operating mode picked from config.
type Mode string

const (
	ModeCloud  Mode = "cloud"        // through model-relay, default
	ModeBYOE   Mode = "byo_endpoint" // through model-relay, with endpoint_override
	ModeDirect Mode = "direct"       // bypass model-relay entirely
)

func (m Mode) IsValid() bool {
	switch m {
	case "", ModeCloud, ModeBYOE, ModeDirect:
		return true
	}
	return false
}
