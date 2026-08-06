package interactive

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestStructuredOutputTool_basicRoundTrip(t *testing.T) {
	tool := StructuredOutputTool{}
	in := map[string]any{
		"summary": "did the thing",
		"items":   []any{"a", "b"},
		"score":   0.8,
	}
	res, err := tool.Call(context.Background(), in, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Errorf("expected success: %+v", res)
	}
	body := textOf(res)
	// The structured payload nests under "structured_output" — verify
	// each key from input shows up there.
	for _, want := range []string{`"summary"`, `"items"`, `"score"`} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q: %s", want, body)
		}
	}
	// Round-trip the body to confirm it's valid JSON.
	var parsed map[string]any
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		t.Errorf("body not valid JSON: %v\n%s", err, body)
	}
	if _, ok := parsed["structured_output"]; !ok {
		t.Errorf("envelope missing structured_output: %s", body)
	}
}

func TestStructuredOutputTool_nilInput(t *testing.T) {
	res, _ := StructuredOutputTool{}.Call(context.Background(), nil, nil)
	if !res.IsError {
		t.Error("nil input should soft-error")
	}
}

func TestStructuredOutputTool_descriptionEmbedsSchema(t *testing.T) {
	schema := map[string]any{
		"type":     "object",
		"required": []string{"summary"},
	}
	tool := StructuredOutputTool{Schema: schema}
	desc := tool.Description(nil)
	if !strings.Contains(desc, "Expected schema") {
		t.Errorf("description should mention schema: %s", desc)
	}
	if !strings.Contains(desc, "summary") {
		t.Errorf("description should embed schema content: %s", desc)
	}
}

func TestStructuredOutputTool_inputSchemaUsesSuppliedShape(t *testing.T) {
	schema := map[string]any{"type": "object", "x-marker": "yes"}
	tool := StructuredOutputTool{Schema: schema}
	got := tool.InputSchema()
	if got["x-marker"] != "yes" {
		t.Errorf("supplied schema should be returned verbatim: %+v", got)
	}
}

func TestStructuredOutputTool_inputSchemaFallback(t *testing.T) {
	got := StructuredOutputTool{}.InputSchema()
	if got["additionalProperties"] != true {
		t.Errorf("default schema should accept anything: %+v", got)
	}
}

func TestStructuredOutputTool_metadataFlags(t *testing.T) {
	tool := StructuredOutputTool{}
	if !tool.IsReadOnly(nil) {
		t.Error("StructuredOutput should be read-only (no side effects)")
	}
	if !tool.IsConcurrencySafe(nil) {
		t.Error("StructuredOutput should be concurrency-safe")
	}
	if tool.IsDestructive(nil) {
		t.Error("StructuredOutput is not destructive")
	}
}
