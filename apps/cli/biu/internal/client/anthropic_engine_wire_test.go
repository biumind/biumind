package client

import (
	"encoding/json"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/state"
)

// TestContentBlockToWire_NilToolUseInputIsObject verifies the malformed-
// input replay guard: a tool_use block with nil ToolUseInput (left nil
// after a streamed-input JSON parse failure) must serialise as
// "input":{} — an object — not "input":null. Anthropic requires
// tool_use.input to be an object; null 400s the next request when the
// malformed block is replayed in history.
func TestContentBlockToWire_NilToolUseInputIsObject(t *testing.T) {
	got := contentBlockToWire(state.ContentBlock{
		Type:         state.ContentToolUse,
		ToolUseID:    "tu_1",
		ToolUseName:  "Echo",
		ToolUseInput: nil,
	})
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var wire struct {
		Input any `json:"input"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	obj, ok := wire.Input.(map[string]any)
	if !ok {
		t.Fatalf("nil tool_use input must serialise as JSON object {}, got %T: %s", wire.Input, raw)
	}
	if len(obj) != 0 {
		t.Errorf("expected empty object {}, got %v: %s", obj, raw)
	}
}
