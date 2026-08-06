package engine

import (
	"strings"
	"testing"
)

// TestTeamRegistry_CreateGetDelete — basic CRUD round-trip with the
// duplicate-create / unknown-delete edge cases.
func TestTeamRegistry_CreateGetDelete(t *testing.T) {
	r := NewTeamRegistry()
	team, err := r.Create("auth-rewrite", "auth refactor team")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if team.Name != "auth-rewrite" || team.Description != "auth refactor team" {
		t.Errorf("team fields: %+v", team)
	}
	// Duplicate create should fail.
	if _, err := r.Create("auth-rewrite", "x"); err == nil {
		t.Errorf("duplicate create should fail")
	}

	got, ok := r.Get("auth-rewrite")
	if !ok || got.Name != "auth-rewrite" {
		t.Errorf("Get failed: %v %+v", ok, got)
	}

	// Get returns a copy: mutating Members shouldn't leak back.
	got.Members["sneaky"] = "agent-bad"
	if back, _ := r.Get("auth-rewrite"); len(back.Members) != 0 {
		t.Errorf("Get should return defensive copy; leak: %v", back.Members)
	}

	deleted, ok := r.Delete("auth-rewrite")
	if !ok || deleted.Name != "auth-rewrite" {
		t.Errorf("Delete failed")
	}
	if _, ok := r.Get("auth-rewrite"); ok {
		t.Errorf("post-delete Get should miss")
	}
	if _, ok := r.Delete("ghost"); ok {
		t.Errorf("Delete of unknown team should miss")
	}
}

// TestTeamRegistry_Members — AddMember + ResolveMember happy path
// + duplicate name + unknown team.
func TestTeamRegistry_Members(t *testing.T) {
	r := NewTeamRegistry()
	_, _ = r.Create("squad", "")
	if err := r.AddMember("squad", "lead", "agent-1"); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := r.AddMember("squad", "researcher", "agent-2"); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := r.AddMember("squad", "lead", "agent-99"); err == nil {
		t.Errorf("duplicate member name should fail")
	}
	if err := r.AddMember("ghost-team", "x", "agent-1"); err == nil {
		t.Errorf("unknown team should fail")
	}
	if id, ok := r.ResolveMember("squad", "lead"); !ok || id != "agent-1" {
		t.Errorf("resolve: %v %v", ok, id)
	}
	if _, ok := r.ResolveMember("squad", "nobody"); ok {
		t.Errorf("unknown member should miss")
	}
}

// TestTeamRegistry_AllTeams_Sorted — name-sorted snapshot for stable
// rendering in TeamList / diagnostic output.
func TestTeamRegistry_AllTeams_Sorted(t *testing.T) {
	r := NewTeamRegistry()
	_, _ = r.Create("zebra", "")
	_, _ = r.Create("alpha", "")
	_, _ = r.Create("mike", "")
	teams := r.AllTeams()
	got := make([]string, len(teams))
	for i, t := range teams {
		got[i] = t.Name
	}
	if got[0] != "alpha" || got[1] != "mike" || got[2] != "zebra" {
		t.Errorf("not sorted: %v", got)
	}
}

// TestMessageInbox_FIFO — Enqueue / Dequeue preserve order, Depth
// reports without consuming.
func TestMessageInbox_FIFO(t *testing.T) {
	i := NewMessageInbox()
	for n := 1; n <= 3; n++ {
		body := strings.Repeat("x", n)
		_ = i.Enqueue("agent-1", PendingMessage{Body: body, From: "lead"})
	}
	if got := i.Depth("agent-1"); got != 3 {
		t.Errorf("depth = %d, want 3", got)
	}
	for want := 1; want <= 3; want++ {
		msg, ok := i.Dequeue("agent-1")
		if !ok || len(msg.Body) != want {
			t.Errorf("dequeue %d: msg=%+v ok=%v", want, msg, ok)
		}
	}
	if _, ok := i.Dequeue("agent-1"); ok {
		t.Errorf("empty dequeue should fail")
	}
}

// TestMessageInbox_PerHandleIsolation — messages for agent-1 don't
// leak into agent-2's queue.
func TestMessageInbox_PerHandleIsolation(t *testing.T) {
	i := NewMessageInbox()
	_ = i.Enqueue("agent-1", PendingMessage{Body: "for-1"})
	_ = i.Enqueue("agent-2", PendingMessage{Body: "for-2"})
	if i.Depth("agent-1") != 1 || i.Depth("agent-2") != 1 {
		t.Errorf("isolation broken: %d / %d", i.Depth("agent-1"), i.Depth("agent-2"))
	}
	msg, _ := i.Dequeue("agent-1")
	if msg.Body != "for-1" {
		t.Errorf("agent-1 dequeue got %q", msg.Body)
	}
	if i.Depth("agent-2") != 1 {
		t.Errorf("agent-2 still has its message; got %d", i.Depth("agent-2"))
	}
}

// TestMessageInbox_NilSafety — nil receiver / empty handle don't
// crash so call sites can stay terse.
func TestMessageInbox_NilSafety(t *testing.T) {
	var i *MessageInbox
	if d := i.Depth("x"); d != 0 {
		t.Errorf("nil Depth should be 0; got %d", d)
	}
	if i.Enqueue("x", PendingMessage{}) != 0 {
		t.Errorf("nil Enqueue should yield 0")
	}
	if _, ok := i.Dequeue("x"); ok {
		t.Errorf("nil Dequeue should miss")
	}
	good := NewMessageInbox()
	if good.Enqueue("", PendingMessage{Body: "x"}) != 0 {
		t.Errorf("empty handle Enqueue should yield 0")
	}
}
