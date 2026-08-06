// StructuredOutput — final structured response tool for SDK / headless
// callers.
//
// 
// (called SyntheticOutputTool internally; surfaced as
// "StructuredOutput" to the model). When biu runs as a library
// (pkg/biumindkit) or via `biu --json`, the caller often wants the
// agent's final answer in a specific JSON shape so downstream code
// can consume it without parsing prose. The model gets prompted
// with the schema, calls this tool exactly once at the end, and
// the SDK consumer reads the JSON from the tool's result.
//
// PP scope: schema validation is deferred. The tool accepts any
// object and round-trips it through the result. SDK callers that
// need strict validation can do their own json.Unmarshal +
// reflection-based check downstream. Adding ajv-equivalent here
// would pull in a heavy schema validator we don't need elsewhere.
//
// Registration: only registered for non-interactive sessions
// (headless mode, SDK callers). Interactive REPL users finish via
// natural language; forcing a tool call there would feel weird.

package interactive

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
)

// StructuredOutputTool — engine-facing tool. Schema is dynamic per
// call (the model picks fields from its instructions + the schema
// the SDK passed via system prompt). We accept any object.
type StructuredOutputTool struct {
	// Schema, when non-nil, is shown to the model in the tool's
	// description so it knows what shape to emit. Optional — without
	// it, we describe the tool generically and trust the surrounding
	// system prompt to constrain the shape.
	Schema map[string]any
}

func (StructuredOutputTool) Name() string { return "StructuredOutput" }

func (s StructuredOutputTool) Description(_ map[string]any) string {
	base := "Return your final response in the requested structured format. " +
		"Call this exactly once at the end of your turn. " +
		"The input becomes the structured result the calling SDK reads."
	if len(s.Schema) > 0 {
		// Embed a compact schema preview so the model has the shape
		// reference even when the system prompt is long.
		if buf, err := json.Marshal(s.Schema); err == nil {
			base += "\n\nExpected schema:\n" + string(buf)
		}
	}
	return base
}

func (s StructuredOutputTool) InputSchema() map[string]any {
	if len(s.Schema) > 0 {
		// When the caller supplied a schema, surface it directly so
		// the model's tool-call validator constrains accordingly.
		// Anthropic's tool-use schema check is permissive about
		// extra keys, so callers that want strict validation should
		// also wrap the JSON downstream.
		return s.Schema
	}
	return map[string]any{
		"type":                 "object",
		"additionalProperties": true,
		"description":          "Any JSON object. The exact shape is dictated by the caller's instructions.",
	}
}

func (StructuredOutputTool) IsReadOnly(_ map[string]any) bool        { return true }
func (StructuredOutputTool) IsDestructive(_ map[string]any) bool     { return false }
func (StructuredOutputTool) IsConcurrencySafe(_ map[string]any) bool { return true }
func (StructuredOutputTool) InterruptBehavior() string               { return "complete" }

func (StructuredOutputTool) Call(ctx context.Context, input map[string]any, env *engine.ToolEnv) (*engine.ToolResultPayload, error) {
	if input == nil {
		return softErr("StructuredOutput", "input object required"), nil
	}
	// Round-trip through JSON so the result is canonical (sorted keys,
	// no trailing commas) and downstream parsers see deterministic
	// output across invocations.
	buf, err := json.Marshal(input)
	if err != nil {
		return softErr("StructuredOutput",
			fmt.Sprintf("marshal: %v", err)), nil
	}
	// Surface a small "data" envelope so SDK users can rely on a
	// stable parse pattern. The structured payload sits under
	// `structured_output`.
	envelope := map[string]any{
		"data":              "Structured output captured.",
		"structured_output": json.RawMessage(buf),
	}
	envBytes, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		return softErr("StructuredOutput",
			fmt.Sprintf("envelope marshal: %v", err)), nil
	}
	return text(string(envBytes)), nil
}
