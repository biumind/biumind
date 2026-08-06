package biumindkit

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
	sdkproto "github.com/biumind/biumind/packages/go-sdk/biu/sdkproto/v1"
)

func TestNewRequiresAPIKey(t *testing.T) {
	if _, err := New(Options{}); err == nil {
		t.Errorf("missing APIKey should error")
	}
}

func TestNewBuildsAgentWithDefaults(t *testing.T) {
	// Use a junk API key — we never actually hit the network in
	// construction; the provider only fires on Submit.
	a, err := New(Options{
		APIKey: "sk-fake",
		// Skip real disk reads — keep tests hermetic.
		LoadProjectMemory:   NoMemory,
		LoadProjectSettings: NoSettings,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer a.Close()
	c := a.Cost()
	if c.Model == "" {
		t.Errorf("default model not picked")
	}
	if c.USD != 0 {
		t.Errorf("fresh agent should have 0 cost; got %f", c.USD)
	}
}

func TestSubmitErrorsBubbleViaEventChannel(t *testing.T) {
	// Real Anthropic call would fail with a 401; we just verify the
	// channel structure delivers the error and closes cleanly.
	a, err := New(Options{
		APIKey:              "sk-fake-401",
		Model:               "claude-haiku-4-5",
		LoadProjectMemory:   NoMemory,
		LoadProjectSettings: NoSettings,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var sawError bool
	for ev := range a.Submit(ctx, "hello") {
		if e, ok := ev.(Error); ok {
			sawError = true
			if e.Err == nil {
				t.Errorf("Error event missing err")
			}
			if !strings.Contains(e.Err.Error(), "anthropic") &&
				!strings.Contains(e.Err.Error(), "http") &&
				!strings.Contains(e.Err.Error(), "401") &&
				!strings.Contains(e.Err.Error(), "provider stream") {
				t.Errorf("error doesn't mention provider: %v", e.Err)
			}
		}
	}
	if !sawError {
		t.Errorf("expected an Error event")
	}
}

// Auto-memory primer must reach the engine's system prompt when
// memory loading is enabled (the SDK default). The model relies on
// the primer to know the directory exists and how to save / read.
func TestNewFoldsAutoMemoryPrimerIntoSystem(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	a, err := New(Options{
		APIKey: "sk-fake",
		// LoadProjectMemory default = enabled; LoadProjectSettings off
		// to keep the test hermetic from any user settings.json.
		LoadProjectSettings: NoSettings,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	got := a.eng.System()
	if !strings.Contains(got, "Auto-memory") {
		t.Errorf("system prompt should contain the auto-memory primer header; got %q",
			truncate(got, 200))
	}
	if !strings.Contains(got, filepath.Join(home, ".biumind", "memory")) {
		t.Errorf("primer should advertise the memory dir under HOME; got %q",
			truncate(got, 200))
	}
}

// When the user has actually written a MEMORY.md, its contents must
// flow into the system prompt verbatim (subject to the truncation
// caps). End-to-end proof that LoadAuto → SystemPrompt → engine.New
// actually plumbs the bytes.
func TestNewIncludesExistingMemoryIndex(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".biumind", "memory")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "- [Lang preference](language.md) — user wants Chinese replies\n"
	if err := os.WriteFile(filepath.Join(dir, "MEMORY.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	a, err := New(Options{
		APIKey:              "sk-fake",
		LoadProjectSettings: NoSettings,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	got := a.eng.System()
	if !strings.Contains(got, "Chinese replies") {
		t.Errorf("MEMORY.md content missing from engine system prompt; got %q",
			truncate(got, 300))
	}
}

// Opting out of memory loading must skip the auto-memory primer too —
// SDK embedders that pass NoMemory expect a clean slate.
func TestNewSkipsMemoryWhenOptedOut(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	a, err := New(Options{
		APIKey:              "sk-fake",
		LoadProjectMemory:   NoMemory,
		LoadProjectSettings: NoSettings,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	if got := a.eng.System(); strings.Contains(got, "Auto-memory") {
		t.Errorf("NoMemory should suppress auto-memory primer; got %q",
			truncate(got, 200))
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func TestRunReturnsAssistantText(t *testing.T) {
	// Run shares the same channel structure; this regression test
	// ensures Run drains correctly even on error paths (which is the
	// only path we can exercise without a live API).
	a, err := New(Options{
		APIKey:              "sk-fake",
		Model:               "claude-haiku-4-5",
		LoadProjectMemory:   NoMemory,
		LoadProjectSettings: NoSettings,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	text, stop, runErr := a.Run(context.Background(), "anything")
	// We expect runErr (HTTP) — but Run must still return cleanly.
	if runErr == nil {
		t.Errorf("expected provider error from fake key")
	}
	_, _ = text, stop
}

func TestPermissionPolicy_BasicVsExtFallback(t *testing.T) {
	// askSuggestionsToSDK + sdkDecisionToAnswer + sdkResponseToAnswer
	// composition without spinning up a full Agent.
	suggs := []engine.AskSuggestion{
		{
			Label: "add", HotKey: "w",
			Update: &sdkproto.AddDirectories{
				Type:        sdkproto.PermissionUpdateAddDirectories,
				Directories: []string{"/tmp/x"},
				Destination: sdkproto.PermissionDestSession,
			},
		},
	}
	got := askSuggestionsToSDK(suggs)
	if len(got) != 1 || got[0].HotKey != "w" || got[0].Kind != "addDirectories" {
		t.Fatalf("askSuggestionsToSDK: %+v", got)
	}

	// Decision-only path: Allow → no AppliedUpdates.
	if a := sdkDecisionToAnswer(PermAllow); a.Decision != engine.PermAllow ||
		len(a.AppliedUpdates) != 0 {
		t.Errorf("decision-only Allow: %+v", a)
	}
	if a := sdkDecisionToAnswer(PermAlways); !a.Remember {
		t.Errorf("PermAlways should set Remember; got %+v", a)
	}
	if a := sdkDecisionToAnswer(PermDeny); a.Decision != engine.PermDeny {
		t.Errorf("PermDeny: %+v", a)
	}
}

func TestPermissionPolicy_ExtPicksSuggestion(t *testing.T) {
	ask := &engine.PermissionAskEvent{
		Suggestions: []engine.AskSuggestion{
			{
				Label: "add", HotKey: "w",
				Update: &sdkproto.AddDirectories{
					Type:        sdkproto.PermissionUpdateAddDirectories,
					Directories: []string{"/tmp/x"},
					Destination: sdkproto.PermissionDestSession,
				},
			},
		},
	}
	resp := PermissionResponse{Decision: PermAllow, SelectedSuggestion: 0}
	ans := sdkResponseToAnswer(resp, ask)
	if ans.Decision != engine.PermAllow {
		t.Errorf("decision: %v", ans.Decision)
	}
	if len(ans.AppliedUpdates) != 1 {
		t.Errorf("expected 1 applied update; got %+v", ans.AppliedUpdates)
	}
}

func TestPermissionPolicy_ExtSelectionIgnoredOnDeny(t *testing.T) {
	ask := &engine.PermissionAskEvent{
		Suggestions: []engine.AskSuggestion{
			{HotKey: "w", Update: &sdkproto.AddDirectories{}},
		},
	}
	resp := PermissionResponse{Decision: PermDeny, SelectedSuggestion: 0}
	ans := sdkResponseToAnswer(resp, ask)
	if ans.Decision != engine.PermDeny {
		t.Errorf("deny should propagate; got %+v", ans)
	}
	if len(ans.AppliedUpdates) != 0 {
		t.Errorf("deny must not carry updates; got %+v", ans.AppliedUpdates)
	}
}

func TestPermissionPolicy_ExtSelectionOutOfRange(t *testing.T) {
	ask := &engine.PermissionAskEvent{
		Suggestions: []engine.AskSuggestion{
			{HotKey: "w", Update: &sdkproto.AddDirectories{}},
		},
	}
	resp := PermissionResponse{Decision: PermAllow, SelectedSuggestion: 5}
	ans := sdkResponseToAnswer(resp, ask)
	if len(ans.AppliedUpdates) != 0 {
		t.Errorf("out-of-range index → no apply; got %+v", ans.AppliedUpdates)
	}
}
