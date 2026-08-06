package session

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestWatchHookEvents_StatusAndSession(t *testing.T) {
	dir := t.TempDir()
	eventsFile := filepath.Join(dir, "events.jsonl")

	var mu sync.Mutex
	var statuses []string
	var sessionID, transcript string
	onStatus := func(s string) { mu.Lock(); statuses = append(statuses, s); mu.Unlock() }
	onSession := func(sid, tp string) { mu.Lock(); sessionID, transcript = sid, tp; mu.Unlock() }

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { WatchHookEvents(ctx, dir, onStatus, onSession); close(done) }()

	append := func(line string) {
		f, err := os.OpenFile(eventsFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = f.WriteString(line + "\n")
		_ = f.Close()
	}

	// SessionStart → onSessionStart;Stop → input_required;PostToolUse → running。
	append(`{"task_id":"t1","agent":"claude","event":"SessionStart","session_id":"sess-9","transcript_path":"/x/sess-9.jsonl"}`)
	append(`{"task_id":"t1","event":"Stop"}`)

	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return sessionID == "sess-9" && contains(statuses, "input_required")
	}, "SessionStart + Stop")

	append(`{"task_id":"t1","event":"PostToolUse"}`)
	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return contains(statuses, "running")
	}, "PostToolUse → running")

	mu.Lock()
	if transcript != "/x/sess-9.jsonl" {
		t.Errorf("transcript = %q", transcript)
	}
	mu.Unlock()

	cancel()
	<-done
}

func TestWatchHookEvents_StatusDedup(t *testing.T) {
	dir := t.TempDir()
	eventsFile := filepath.Join(dir, "events.jsonl")
	var mu sync.Mutex
	var n int
	onStatus := func(s string) {
		if s == "input_required" {
			mu.Lock()
			n++
			mu.Unlock()
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { WatchHookEvents(ctx, dir, onStatus, nil); close(done) }()

	// 连续两条 Stop → 去重,只回调一次 input_required。
	data := `{"task_id":"t1","event":"Stop"}` + "\n" + `{"task_id":"t1","event":"Notification"}` + "\n"
	if err := os.WriteFile(eventsFile, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { mu.Lock(); defer mu.Unlock(); return n >= 1 }, "first input_required")
	time.Sleep(300 * time.Millisecond)
	mu.Lock()
	got := n
	mu.Unlock()
	if got != 1 {
		t.Errorf("input_required emitted %d times, want 1 (dedup)", got)
	}
	cancel()
	<-done
}

func TestWatchCodexFile_KnownPath(t *testing.T) {
	dir := t.TempDir()
	rollout := filepath.Join(dir, "rollout-x.jsonl")
	if err := os.WriteFile(rollout,
		[]byte(`{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hi"}]}}`+"\n"),
		0o644); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var got []map[string]any
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		WatchCodexFile(ctx, rollout, dir, func(events []map[string]any) {
			mu.Lock()
			got = append(got, events...)
			mu.Unlock()
		})
		close(done)
	}()
	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return findEvent(got, "text_delta") != nil
	}, "codex text_delta from known path")
	cancel()
	<-done
}

// ─── helpers ───

func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", what)
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
