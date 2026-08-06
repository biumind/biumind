// Package llm defines the canonical Provider interface used by every BiuMind
// component that talks to an LLM:
//
//	biu CLI       — RelayProvider / AnthropicDirect / OpenAIDirect
//	Brain ingest  — same Provider, runs server-side (typically model-relay)
//	Runtime agent — same Provider (typically model-relay via virtual key)
//
// Keeping this in shared SDK means the same parser + two-step CoT code
// runs both server-side (via model-relay) and client-side (via Mode C direct).
package llm

import "context"

// Message is the canonical chat-style message.
type Message struct {
	Role    string `json:"role"` // user / assistant / system / tool
	Content string `json:"content"`
}

// ChatRequest is the canonical request shape every Provider accepts.
type ChatRequest struct {
	Model     string
	System    string
	Messages  []Message
	MaxTokens int
}

// FrameKind classifies streaming chunks.
type FrameKind int

const (
	KindDelta FrameKind = iota
	KindStop
	KindEnd
	KindError
)

// Frame is one chunk in a stream.
type Frame struct {
	Kind FrameKind
	Text string
	Stop string
	Err  error
}

// Provider is the contract every LLM client implements.
type Provider interface {
	// Name returns a human-readable provider identifier (for logs / UI).
	Name() string

	// ChatStream initiates a streaming chat completion. The returned channel
	// is closed when the stream terminates (success / error / context cancel).
	ChatStream(ctx context.Context, req ChatRequest) (<-chan Frame, error)
}

// CollectText drains a Frame channel into a single string.
// Convenience for callers who don't need streaming UI (e.g. ingest pipelines).
func CollectText(ch <-chan Frame) (string, error) {
	var sb []byte
	for f := range ch {
		switch f.Kind {
		case KindDelta:
			sb = append(sb, f.Text...)
		case KindError:
			return string(sb), f.Err
		}
	}
	return string(sb), nil
}
