package chat

import (
	"encoding/json"
	"testing"
)

func TestPartsHaveMultimodal(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want bool
	}{
		{"empty", "", false},
		{"empty array", "[]", false},
		{"single text", `[{"type":"text","text":"hi"}]`, false},
		{"text + image", `[{"type":"text","text":"hi"},{"type":"image","source":{}}]`, true},
		{"image only", `[{"type":"image","source":{}}]`, true},
		{"file block", `[{"type":"file","name":"x.pdf"}]`, true},
		{"malformed json", `not json`, false},
		{"non-array", `{"type":"text"}`, false},
	}
	for _, c := range cases {
		got := partsHaveMultimodal([]byte(c.raw))
		if got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestBuildHubMessagesForwardsMultimodalParts(t *testing.T) {
	imgParts := []byte(
		`[{"type":"text","text":"look"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"deadbeef"}}]`)
	textParts := []byte(`[{"type":"text","text":"hi"}]`)

	history := []*Message{
		{Role: RoleUser, Content: "hi", Parts: textParts, Status: StatusSuccess},
		{Role: RoleUser, Content: "look", Parts: imgParts, Status: StatusSuccess},
		{Role: RoleAssistant, Content: "ok", Status: StatusSuccess},
	}
	out := buildHubMessages(history, 0, 1<<30)
	if len(out) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(out))
	}
	// First message: text-only parts → no Parts forwarded
	if len(out[0].Parts) != 0 {
		t.Errorf("text-only parts should NOT forward; got %s", out[0].Parts)
	}
	// Second message: multimodal parts forwarded
	if len(out[1].Parts) == 0 {
		t.Errorf("multimodal parts should forward")
	}
	// Verify the forwarded shape parses back to original
	var blocks []map[string]any
	if err := json.Unmarshal(out[1].Parts, &blocks); err != nil {
		t.Fatalf("parts not valid JSON: %v", err)
	}
	if len(blocks) != 2 || blocks[1]["type"] != "image" {
		t.Errorf("unexpected shape: %+v", blocks)
	}
	// Third (assistant): no parts
	if len(out[2].Parts) != 0 {
		t.Errorf("assistant should not have parts forwarded")
	}
}
