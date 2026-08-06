package compact

import (
	"strings"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/state"
)

func TestDedupeReadsKeepsLatestOnly(t *testing.T) {
	msgs := []state.Message{
		{Role: state.RoleAssistant, Content: []state.ContentBlock{{
			Type:         state.ContentToolUse,
			ToolUseID:    "u1",
			ToolUseName:  "Read",
			ToolUseInput: map[string]any{"file_path": "/x.go"},
		}}},
		{Role: state.RoleUser, Content: []state.ContentBlock{{
			Type:         state.ContentToolResult,
			ToolResultID: "u1",
			ToolResultContent: []state.ContentBlock{{
				Type: state.ContentText,
				Text: strings.Repeat("a", 5000),
			}},
		}}},
		{Role: state.RoleAssistant, Content: []state.ContentBlock{{
			Type:         state.ContentToolUse,
			ToolUseID:    "u2",
			ToolUseName:  "Read",
			ToolUseInput: map[string]any{"file_path": "/x.go"},
		}}},
		{Role: state.RoleUser, Content: []state.ContentBlock{{
			Type:         state.ContentToolResult,
			ToolResultID: "u2",
			ToolResultContent: []state.ContentBlock{{
				Type: state.ContentText,
				Text: strings.Repeat("b", 5000),
			}},
		}}},
	}
	saved := Apply(msgs, MicroOptions{DedupeReads: true})
	if saved == 0 {
		t.Errorf("expected non-zero savings")
	}

	// First Read should be replaced with placeholder.
	first := msgs[1].Content[0].ToolResultContent[0].Text
	if !strings.Contains(first, "superseded") {
		t.Errorf("first read not deduped: %q", first)
	}
	// Second (latest) Read content untouched.
	second := msgs[3].Content[0].ToolResultContent[0].Text
	if len(second) != 5000 {
		t.Errorf("latest read should be untouched; len=%d", len(second))
	}
}

func TestTruncateLongResults(t *testing.T) {
	body := strings.Repeat("x", 20_000)
	msgs := []state.Message{
		{Role: state.RoleUser, Content: []state.ContentBlock{{
			Type: state.ContentToolResult,
			ToolResultID: "u1",
			ToolResultContent: []state.ContentBlock{{
				Type: state.ContentText, Text: body,
			}},
		}}},
	}
	saved := Apply(msgs, MicroOptions{MaxToolResultChars: 1000})
	if saved <= 0 {
		t.Errorf("expected truncation savings; got %d", saved)
	}
	got := msgs[0].Content[0].ToolResultContent[0].Text
	if !strings.Contains(got, "truncated") {
		t.Errorf("missing truncation marker: %q", got[:200])
	}
	if len(got) >= len(body) {
		t.Errorf("text not shorter: %d vs %d", len(got), len(body))
	}
}

func TestApplyNoOpOnEmpty(t *testing.T) {
	if got := Apply(nil, Default()); got != 0 {
		t.Errorf("nil input should yield 0 savings; got %d", got)
	}
}

func TestDedupeIgnoresSingleRead(t *testing.T) {
	msgs := []state.Message{
		{Role: state.RoleAssistant, Content: []state.ContentBlock{{
			Type: state.ContentToolUse,
			ToolUseID: "u1", ToolUseName: "Read",
			ToolUseInput: map[string]any{"file_path": "/x.go"},
		}}},
	}
	if got := Apply(msgs, MicroOptions{DedupeReads: true}); got != 0 {
		t.Errorf("single read shouldn't trigger savings")
	}
}
