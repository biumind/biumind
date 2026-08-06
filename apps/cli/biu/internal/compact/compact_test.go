package compact

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/state"
)

type stubProvider struct {
	got      []state.Message
	gotInstr string
	body     string
	err      error
}

func (s *stubProvider) Summarise(_ context.Context, msgs []state.Message, instr string) (string, error) {
	s.got = msgs
	s.gotInstr = instr
	return s.body, s.err
}

func makeMsgs() []state.Message {
	return []state.Message{
		{Role: state.RoleUser, Content: []state.ContentBlock{{Type: state.ContentText, Text: "first prompt"}}},
		{Role: state.RoleAssistant, Content: []state.ContentBlock{{Type: state.ContentText, Text: "long stuff"}}},
		{Role: state.RoleUser, Content: []state.ContentBlock{{Type: state.ContentText, Text: "more"}}},
		{Role: state.RoleAssistant, Content: []state.ContentBlock{{Type: state.ContentText, Text: "more reply"}}},
		{Role: state.RoleUser, Content: []state.ContentBlock{{Type: state.ContentText, Text: "WHAT IS THE LATEST"}}},
	}
}

func TestShouldFireThreshold(t *testing.T) {
	a := New(Options{MaxTokens: 1000, ThresholdRatio: 0.7})
	if a.ShouldFire(699) {
		t.Errorf("under threshold should not fire")
	}
	if !a.ShouldFire(700) {
		t.Errorf("at threshold should fire")
	}
	if !a.ShouldFire(950) {
		t.Errorf("over threshold should fire")
	}
}

func TestRunWrapsSummaryAndKeepsLastUser(t *testing.T) {
	prov := &stubProvider{body: "<summary>tight digest</summary>"}
	a := New(Options{Provider: prov})
	res, err := a.Run(context.Background(), makeMsgs())
	if err != nil {
		t.Fatal(err)
	}
	if res.Summary != "tight digest" {
		t.Errorf("summary extract: %q", res.Summary)
	}
	if res.OriginalCount != 5 || res.NewCount != 2 {
		t.Errorf("counts: %+v", res)
	}
	if res.Replaced[0].Role != state.RoleSystem {
		t.Errorf("first replaced should be system: %v", res.Replaced[0].Role)
	}
	if !strings.Contains(res.Replaced[0].Content[0].Text, "tight digest") {
		t.Errorf("system body missing summary")
	}
	last := res.Replaced[1]
	if last.Role != state.RoleUser ||
		last.Content[0].Text != "WHAT IS THE LATEST" {
		t.Errorf("last user not preserved: %+v", last)
	}
	if !strings.Contains(prov.gotInstr, "Primary Request") {
		t.Errorf("instruction missing summary sections: %q", prov.gotInstr)
	}
}

func TestRunErrorsBubble(t *testing.T) {
	prov := &stubProvider{err: errors.New("rate limit")}
	a := New(Options{Provider: prov})
	if _, err := a.Run(context.Background(), makeMsgs()); err == nil {
		t.Errorf("expected provider error to bubble")
	}
}

func TestRunRequiresProvider(t *testing.T) {
	a := New(Options{})
	if _, err := a.Run(context.Background(), makeMsgs()); err == nil {
		t.Errorf("nil provider should error")
	}
}

func TestRunEmptyMessages(t *testing.T) {
	a := New(Options{Provider: &stubProvider{}})
	if _, err := a.Run(context.Background(), nil); err == nil {
		t.Errorf("empty messages should error")
	}
}

func TestExtractSummaryFallsBackOnUntagged(t *testing.T) {
	if extractSummary("plain text") != "plain text" {
		t.Errorf("untagged fallback failed")
	}
	if got := extractSummary("<summary>x</summary>"); got != "x" {
		t.Errorf("tagged extract failed: %q", got)
	}
	if got := extractSummary("<summary>open only"); got != "open only" {
		t.Errorf("missing close tag fallback: %q", got)
	}
}

func TestExtraInstructionAppended(t *testing.T) {
	prov := &stubProvider{body: "<summary>x</summary>"}
	a := New(Options{Provider: prov, Instruction: "Focus on Go code."})
	if _, err := a.Run(context.Background(), makeMsgs()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prov.gotInstr, "Focus on Go code.") {
		t.Errorf("custom instruction missing")
	}
}

func TestRunInjectsAttachmentBetweenSummaryAndUser(t *testing.T) {
	prov := &stubProvider{body: "<summary>x</summary>"}
	called := 0
	a := New(Options{
		Provider: prov,
		Attachments: func() []state.Message {
			called++
			return []state.Message{{
				Role: state.RoleSystem,
				Content: []state.ContentBlock{{
					Type: state.ContentText,
					Text: "<approved-plan>\n## Plan\nstep 1\n</approved-plan>",
				}},
			}}
		},
	})
	res, err := a.Run(context.Background(), makeMsgs())
	if err != nil {
		t.Fatal(err)
	}
	if called != 1 {
		t.Errorf("Attachments closure called %d times, want 1", called)
	}
	// Expected ordering: summary, attachment, lastUser.
	if len(res.Replaced) != 3 {
		t.Fatalf("expected 3 messages; got %d (%+v)", len(res.Replaced), res.Replaced)
	}
	if !strings.Contains(res.Replaced[0].Content[0].Text, "<summary>") {
		t.Errorf("[0] should be the summary message; got %+v", res.Replaced[0])
	}
	if !strings.Contains(res.Replaced[1].Content[0].Text, "approved-plan") {
		t.Errorf("[1] should be the attachment; got %+v", res.Replaced[1])
	}
	if res.Replaced[2].Role != state.RoleUser {
		t.Errorf("[2] should be the last user message; got %v", res.Replaced[2].Role)
	}
}

func TestRunNoAttachmentsCloserKeepsLegacyShape(t *testing.T) {
	prov := &stubProvider{body: "<summary>x</summary>"}
	a := New(Options{Provider: prov, Attachments: func() []state.Message { return nil }})
	res, err := a.Run(context.Background(), makeMsgs())
	if err != nil {
		t.Fatal(err)
	}
	// nil attachments → 2 messages, same as no closure at all.
	if len(res.Replaced) != 2 {
		t.Errorf("expected 2 messages; got %d", len(res.Replaced))
	}
}

// Just to silence the unused-import linter when only this test
// references errors.As.
var _ = errors.As
