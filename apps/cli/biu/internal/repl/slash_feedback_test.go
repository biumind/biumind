package repl

import (
	"errors"
	"strings"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/client"
)

func TestParseFeedbackFlags_titleOnly(t *testing.T) {
	f := parseFeedbackFlags([]string{"/feedback", "compact", "broke"})
	if f.title != "compact broke" {
		t.Errorf("title = %q", f.title)
	}
	if f.print {
		t.Error("print should be false")
	}
}

func TestParseFeedbackFlags_printOnly(t *testing.T) {
	f := parseFeedbackFlags([]string{"/feedback", "--print"})
	if !f.print || f.title != "" {
		t.Errorf("got %+v", f)
	}
}

func TestParseFeedbackFlags_titleAndPrint(t *testing.T) {
	f := parseFeedbackFlags([]string{"/feedback", "bug", "in", "/compact", "--print"})
	if !f.print {
		t.Error("print not set")
	}
	if !strings.Contains(f.title, "bug") {
		t.Errorf("title lost: %q", f.title)
	}
}

func TestBuildFeedbackBody_includesEssentials(t *testing.T) {
	m := model{
		modelID: "claude-opus-4-7",
		history: []client.Message{
			{Role: "user", Content: "hi"},
			{Role: "assistant", Content: "hello"},
		},
		lastErr: errors.New("api 500"),
	}
	body := buildFeedbackBody(m)
	for _, want := range []string{
		"biu version:", "os/arch:", "go runtime:",
		"claude-opus-4-7", "messages: 2 total",
		"api 500", "What happened", "Reproduction",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestBuildFeedbackBody_noLeakOfPromptText(t *testing.T) {
	m := model{
		history: []client.Message{
			{Role: "user", Content: "PRIVATE_API_KEY=secret123"},
		},
	}
	body := buildFeedbackBody(m)
	if strings.Contains(body, "PRIVATE_API_KEY") || strings.Contains(body, "secret123") {
		t.Errorf("feedback body leaked prompt content: %s", body)
	}
}

func TestHandleFeedback_printDoesNotShellOut(t *testing.T) {
	got := model{}.handleFeedback([]string{"/feedback", "--print"})
	if !strings.Contains(got, "biu feedback") {
		t.Errorf("missing default title: %s", got)
	}
	if !strings.Contains(got, "## Environment") {
		t.Errorf("body missing: %s", got)
	}
}
