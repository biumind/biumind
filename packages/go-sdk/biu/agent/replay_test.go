package agent

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestRecorderTeeRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")

	rec, err := NewRecorder(path)
	if err != nil {
		t.Fatalf("recorder: %v", err)
	}

	in := make(chan Event, 4)
	live := rec.Tee(in)

	// Push 3 events through and close.
	go func() {
		base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		in <- Event{Type: EventSystem, Timestamp: base, Content: "boot"}
		in <- Event{Type: EventText, Timestamp: base.Add(100 * time.Millisecond), Content: "hello"}
		in <- Event{Type: EventDone, Timestamp: base.Add(200 * time.Millisecond)}
		close(in)
	}()

	// Drain the live side.
	got := collect(live)
	if len(got) != 3 {
		t.Fatalf("want 3 live events, got %d", len(got))
	}
	if err := rec.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Replay (non-realtime) and compare.
	ch, err := Replay(context.Background(), path, false)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	rep := collect(ch)
	if len(rep) != 3 {
		t.Fatalf("want 3 replay events, got %d", len(rep))
	}
	for i := range got {
		if got[i].Type != rep[i].Type || got[i].Content != rep[i].Content {
			t.Errorf("mismatch [%d]: live=%+v replay=%+v", i, got[i], rep[i])
		}
		if !got[i].Timestamp.Equal(rep[i].Timestamp) {
			t.Errorf("timestamp drift [%d]: live=%v replay=%v",
				i, got[i].Timestamp, rep[i].Timestamp)
		}
	}
}

func TestReplayRespectsContextCancel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	rec, _ := NewRecorder(path)
	for i := 0; i < 5; i++ {
		_ = rec.WriteEvent(Event{
			Type: EventText, Content: "x",
			Timestamp: time.Now().Add(time.Duration(i) * time.Second),
		})
	}
	rec.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately
	ch, _ := Replay(ctx, path, true)
	// Channel should close fast even though realtime=true would normally
	// sleep. Drain with a generous timeout to detect blocked goroutine.
	done := make(chan struct{})
	go func() {
		for range ch {
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("replay didn't honour ctx cancel")
	}
}

func TestRecorderWriteEventDirect(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	rec, _ := NewRecorder(path)
	defer rec.Close()
	if err := rec.WriteEvent(Event{Type: EventSystem}); err != nil {
		t.Fatalf("write: %v", err)
	}
	// Auto-stamp when caller didn't set Timestamp.
	rec.Close()

	ch, _ := Replay(context.Background(), path, false)
	events := collect(ch)
	if len(events) != 1 || events[0].Type != EventSystem || events[0].Timestamp.IsZero() {
		t.Errorf("auto-stamp failed: %+v", events)
	}
}
