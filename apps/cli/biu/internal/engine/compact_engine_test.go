package engine

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/compact"
	"github.com/biumind/biumind/apps/cli/biu/internal/state"
)

// summariseProvider is a scriptedProvider variant that also satisfies
// the implicit role of compact summariser — when called with a final
// user message containing "Primary Request and Intent" it returns a
// canned summary instead of the regular scripted turn.
type summariseProvider struct {
	scripts [][]StreamFrame
	calls   int
	summary string
}

func (p *summariseProvider) Stream(ctx context.Context, req StreamRequest) (<-chan StreamFrame, error) {
	// Detect the compact summariser call: last user message body
	// contains the BASE_COMPACT_PROMPT marker.
	if isSummariseCall(req) {
		return scriptText("<summary>" + p.summary + "</summary>"), nil
	}
	if p.calls >= len(p.scripts) {
		return scriptText("done"), nil
	}
	frames := p.scripts[p.calls]
	p.calls++
	ch := make(chan StreamFrame, len(frames))
	for _, f := range frames {
		ch <- f
	}
	close(ch)
	return ch, nil
}

func isSummariseCall(req StreamRequest) bool {
	if len(req.Messages) == 0 {
		return false
	}
	last := req.Messages[len(req.Messages)-1]
	if last.Role != state.RoleUser {
		return false
	}
	for _, b := range last.Content {
		if strings.Contains(b.Text, "Primary Request and Intent") {
			return true
		}
	}
	return false
}

// scriptText wraps a single text turn into a frame channel.
func scriptText(text string) <-chan StreamFrame {
	frames := textTurn(text)
	ch := make(chan StreamFrame, len(frames))
	for _, f := range frames {
		ch <- f
	}
	close(ch)
	return ch
}

func TestEngineCompactReplacesHistory(t *testing.T) {
	prov := &summariseProvider{
		scripts: [][]StreamFrame{textTurn("first answer")},
		summary: "everything important fits here",
	}
	st := state.New()
	reg := NewRegistry()
	eng, err := New(Options{
		State: st, Tools: reg, Provider: prov, Model: "test",
		BypassPermissions: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Run a normal turn first so state has user + assistant messages.
	drainAll(eng.Submit(context.Background(), "hello"))
	if got := len(st.Snapshot()); got < 2 {
		t.Fatalf("expected ≥2 messages, got %d", got)
	}

	// Manual compact.
	out := make(chan Event, 16)
	go func() {
		defer close(out)
		if err := eng.Compact(context.Background(), out); err != nil {
			t.Errorf("compact returned error: %v", err)
		}
	}()
	var sawStart, sawDone bool
	for ev := range out {
		switch ev.(type) {
		case *CompactStartEvent:
			sawStart = true
		case *CompactDoneEvent:
			sawDone = true
		}
	}
	if !sawStart || !sawDone {
		t.Errorf("missing compact events: start=%v done=%v", sawStart, sawDone)
	}

	// State should now be {system summary}; the original assistant
	// turn should be gone.
	snap := st.Snapshot()
	if len(snap) == 0 {
		t.Fatal("compact emptied state entirely")
	}
	if snap[0].Role != state.RoleSystem {
		t.Errorf("first message after compact should be system: %v", snap[0].Role)
	}
	body := ""
	for _, b := range snap[0].Content {
		body += b.Text
	}
	if !strings.Contains(body, "everything important fits here") {
		t.Errorf("compact summary not stored: %q", body)
	}
}

// MC5: file attachments — compact re-injects current file contents
// into the post-compact history so the model has ground truth for
// files it was working on, not just the summary's mention of them.
func TestEngineCompactReInjectsFileAttachments(t *testing.T) {
	dir := t.TempDir()
	target := dir + "/main.go"
	if err := os.WriteFile(target, []byte("package main // current"), 0o644); err != nil {
		t.Fatal(err)
	}

	prov := &summariseProvider{
		scripts: [][]StreamFrame{textTurn("ok")},
		summary: "summarised",
	}
	st := state.New()
	// Simulate an earlier Read having tracked this file.
	st.PutFile(state.FileState{Path: target, Sha256: "stale", Content: "before"})

	eng, err := New(Options{
		State: st, Tools: NewRegistry(), Provider: prov, Model: "test",
		BypassPermissions: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	drainAll(eng.Submit(context.Background(), "hi"))

	out := make(chan Event, 16)
	go func() {
		defer close(out)
		_ = eng.Compact(context.Background(), out)
	}()
	for range out {
	}

	// Post-compact history must contain a system message holding
	// the CURRENT on-disk file content (not the stale "before").
	snap := st.Snapshot()
	hit := false
	for _, m := range snap {
		for _, b := range m.Content {
			if strings.Contains(b.Text, target) &&
				strings.Contains(b.Text, "package main // current") {
				hit = true
			}
		}
	}
	if !hit {
		t.Errorf("post-compact history missing file attachment for %s; snap=%+v",
			target, snap)
	}
}

// MC6: SessionMemory writer receives the summary after a successful
// compact. The bridge in compact_run.go must invoke
// PushSummaryToSessionMemory so cross-restart continuity works.
func TestEngineCompactWritesSessionMemory(t *testing.T) {
	prov := &summariseProvider{
		scripts: [][]StreamFrame{textTurn("ok")},
		summary: strings.Repeat("important context. ", 30),
	}
	captured := &captureSessionMem{}
	eng, err := New(Options{
		State: state.New(), Tools: NewRegistry(), Provider: prov, Model: "test",
		BypassPermissions: true,
		SessionMemory:     captured,
	})
	if err != nil {
		t.Fatal(err)
	}
	drainAll(eng.Submit(context.Background(), "hi"))

	out := make(chan Event, 16)
	go func() {
		defer close(out)
		_ = eng.Compact(context.Background(), out)
	}()
	for range out {
	}

	if !captured.saved {
		t.Error("SessionMemory.Save not called after compact")
	}
	if !strings.Contains(captured.current, "important context") {
		t.Errorf("Current State body wrong: %q", captured.current[:50])
	}
}

type captureSessionMem struct {
	current string
	saved   bool
}

func (c *captureSessionMem) SetCurrentState(s string) { c.current = s }
func (c *captureSessionMem) Save() error              { c.saved = true; return nil }
func (c *captureSessionMem) Truncate()                {}

// MC2 regression: AppState's file freshness ledger must reset after
// compact. Without the reset, post-compact Edit calls would think
// the model still has fresh reads of files whose Read results were
// summarised away, which is exactly the kind of subtle staleness
// bug the post-compact cleanup pipeline guards against.
func TestEngineCompactClearsFileSnapshots(t *testing.T) {
	prov := &summariseProvider{
		scripts: [][]StreamFrame{textTurn("ok")},
		summary: "summarised",
	}
	st := state.New()
	// Seed the AppState file ledger as if Read had captured a file.
	st.PutFile(state.FileState{
		Path: "/tmp/x.go", Sha256: "abc", Content: "// pre-compact",
	})
	if _, ok := st.FileSnapshot("/tmp/x.go"); !ok {
		t.Fatal("setup: snapshot should be present pre-compact")
	}

	eng, err := New(Options{
		State: st, Tools: NewRegistry(), Provider: prov, Model: "test",
		BypassPermissions: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	drainAll(eng.Submit(context.Background(), "hi"))

	out := make(chan Event, 16)
	go func() {
		defer close(out)
		_ = eng.Compact(context.Background(), out)
	}()
	for range out {
	}

	if _, ok := st.FileSnapshot("/tmp/x.go"); ok {
		t.Error("file snapshot should be cleared after compact")
	}
}

// MC2 regression: package-level cleanup callbacks fire after a
// successful compact. We register a counting callback, run a
// compact, and assert the count incremented.
func TestEngineCompactRunsPostCleanupCallbacks(t *testing.T) {
	prov := &summariseProvider{
		scripts: [][]StreamFrame{textTurn("ok")},
		summary: "x",
	}
	eng, err := New(Options{
		State: state.New(), Tools: NewRegistry(), Provider: prov, Model: "test",
		BypassPermissions: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	hits := 0
	compact.RegisterPostCleanup(
		compact.CleanupOptions{Name: "test:counter"},
		func(s compact.CleanupScope) { hits++ },
	)
	t.Cleanup(func() {
		// No public reset; rely on test isolation. Subsequent tests
		// see this callback too, but since they don't read `hits`
		// it's harmless. Production callbacks register at init() in
		// real subsystems and never unregister.
	})

	drainAll(eng.Submit(context.Background(), "hi"))
	out := make(chan Event, 16)
	go func() {
		defer close(out)
		_ = eng.Compact(context.Background(), out)
	}()
	for range out {
	}
	if hits == 0 {
		t.Error("post-cleanup callback never fired")
	}
}

func TestEngineAutoCompactDisabledByNegativeMax(t *testing.T) {
	prov := &scriptedProvider{scripts: [][]StreamFrame{textTurn("ok")}}
	eng, err := New(Options{
		State: state.New(), Tools: NewRegistry(), Provider: prov, Model: "test",
		BypassPermissions: true,
		CompactMaxTokens:  -1, // disable
	})
	if err != nil {
		t.Fatal(err)
	}
	if eng.compact != nil {
		t.Errorf("compact should be nil when CompactMaxTokens=-1")
	}
	out := make(chan Event, 4)
	if err := eng.Compact(context.Background(), out); err == nil {
		t.Errorf("manual Compact must error when disabled")
	}
}
