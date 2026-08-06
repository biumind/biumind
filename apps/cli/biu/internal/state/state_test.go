package state

import (
	"sync"
	"testing"
)

func TestAppendAndSnapshot(t *testing.T) {
	s := New()
	s.AppendMessage(Message{Role: RoleUser, Content: []ContentBlock{{Type: ContentText, Text: "hi"}}})
	s.AppendMessage(Message{Role: RoleAssistant, Content: []ContentBlock{{Type: ContentText, Text: "hello"}}})

	snap := s.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("snapshot len: %d", len(snap))
	}
	if snap[0].Role != RoleUser || snap[1].Role != RoleAssistant {
		t.Errorf("role mismatch: %+v", snap)
	}
	if snap[0].ID == "" || snap[0].CreatedAt.IsZero() {
		t.Errorf("auto-fill missing")
	}
}

func TestLastAssistant(t *testing.T) {
	s := New()
	if _, ok := s.LastAssistant(); ok {
		t.Errorf("empty state should not have last assistant")
	}
	s.AppendMessage(Message{Role: RoleUser})
	s.AppendMessage(Message{Role: RoleAssistant, Content: []ContentBlock{{Type: ContentText, Text: "first"}}})
	s.AppendMessage(Message{Role: RoleUser})
	s.AppendMessage(Message{Role: RoleAssistant, Content: []ContentBlock{{Type: ContentText, Text: "second"}}})
	last, ok := s.LastAssistant()
	if !ok || last.Content[0].Text != "second" {
		t.Errorf("got %+v", last)
	}
}

func TestFileCache(t *testing.T) {
	s := New()
	if _, ok := s.FileSnapshot("/x/y"); ok {
		t.Errorf("nothing cached, should miss")
	}
	s.PutFile(FileState{Path: "/x/y", Content: "abc", NumLines: 1, Sha256: "deadbeef"})
	got, ok := s.FileSnapshot("/x/y")
	if !ok || got.Content != "abc" {
		t.Errorf("got %+v ok=%v", got, ok)
	}
}

func TestCostAccumulates(t *testing.T) {
	s := New()
	s.AddCost(100, 50, 10, 30000)
	s.AddCost(100, 50, 0, 30000)
	c := s.CostNow()
	if c.TotalInputTokens != 200 || c.TotalOutputTokens != 100 ||
		c.TotalCacheTokens != 10 || c.TotalUSDMicros != 60000 {
		t.Errorf("got %+v", c)
	}
}

func TestTaskCRUD(t *testing.T) {
	s := New()
	t1 := &Task{Description: "compile"}
	s.PutTask(t1)
	if t1.ID == "" {
		t.Errorf("PutTask should auto-assign ID")
	}
	ok := s.UpdateTask(t1.ID, func(tt *Task) { tt.Status = "running" })
	if !ok {
		t.Errorf("UpdateTask should find task")
	}
	if !s.UpdateTask(t1.ID, func(tt *Task) {
		if tt.Status != "running" {
			t.Errorf("status not preserved: %q", tt.Status)
		}
	}) {
		t.Errorf("second update lost task")
	}
	if s.UpdateTask(TaskID("nonexistent"), func(*Task) {}) {
		t.Errorf("missing task should report false")
	}
}

func TestPermissionGrant(t *testing.T) {
	s := New()
	if s.HasPermission("Bash:rm") {
		t.Errorf("nothing granted, should be false")
	}
	s.GrantPermission("Bash:rm")
	if !s.HasPermission("Bash:rm") {
		t.Errorf("grant lost")
	}
}

func TestConcurrentAppend(t *testing.T) {
	s := New()
	const goroutines = 50
	const perG = 100
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < perG; j++ {
				s.AppendMessage(Message{Role: RoleUser})
			}
		}()
	}
	wg.Wait()
	if got := len(s.Snapshot()); got != goroutines*perG {
		t.Errorf("expected %d messages, got %d", goroutines*perG, got)
	}
}

func TestResetMessagesForCompact(t *testing.T) {
	s := New()
	for i := 0; i < 10; i++ {
		s.AppendMessage(Message{Role: RoleUser})
	}
	s.ResetMessages([]Message{
		{Role: RoleSystem, Content: []ContentBlock{{Type: ContentText, Text: "summary"}}},
	})
	if got := len(s.Snapshot()); got != 1 {
		t.Errorf("reset failed, %d messages remain", got)
	}
}
