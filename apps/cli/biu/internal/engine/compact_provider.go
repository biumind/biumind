// Engine-side glue between QueryEngine and the compact package.
//
// engineSummariser implements compact.SummaryProvider by reusing the
// engine's existing Provider — no separate API key, same model. The
// summariser submits a one-shot non-tool request whose body is the
// summary instruction.

package engine

import (
	"context"
	"strings"

	"github.com/biumind/biumind/apps/cli/biu/internal/state"
)

// engineSummariser asks the parent engine's LLM to summarise a slice
// of messages. We can't reuse Submit() (it wants a fresh user turn);
// instead we issue a direct provider.Stream() with the messages
// concatenated + the compact prompt as the final user message.
type engineSummariser struct {
	provider Provider
	model    string
}

func newEngineSummariser(p Provider, model string) *engineSummariser {
	return &engineSummariser{provider: p, model: model}
}

func (s *engineSummariser) Summarise(
	ctx context.Context,
	msgs []state.Message,
	instruction string,
) (string, error) {
	// Append a user-role instruction message asking for the summary.
	hist := make([]state.Message, 0, len(msgs)+1)
	hist = append(hist, msgs...)
	hist = append(hist, state.Message{
		Role: state.RoleUser,
		Content: []state.ContentBlock{{
			Type: state.ContentText,
			Text: instruction,
		}},
	})

	frames, err := s.provider.Stream(ctx, StreamRequest{
		Model:     s.model,
		Messages:  hist,
		MaxTokens: 4096,
	})
	if err != nil {
		return "", err
	}
	// Drain frames into a string. We don't care about tool_use blocks
	// here — the prompt explicitly disallows tools.
	var b strings.Builder
	for f := range frames {
		if f.Type != FrameContentBlockDelta || f.Delta == nil {
			continue
		}
		if f.Delta.Type == "text_delta" {
			b.WriteString(f.Delta.Text)
		}
	}
	return b.String(), nil
}
