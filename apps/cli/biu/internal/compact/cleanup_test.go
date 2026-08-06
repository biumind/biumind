package compact

import (
	"testing"
)

func TestRegisterPostCleanup_fires(t *testing.T) {
	t.Cleanup(resetPostCleanup)
	resetPostCleanup()

	hits := 0
	RegisterPostCleanup(CleanupOptions{Name: "test:hit"}, func(s CleanupScope) {
		hits++
	})
	RunPostCleanup(ScopeMain)
	if hits != 1 {
		t.Errorf("hits = %d, want 1", hits)
	}
}

func TestRegisterPostCleanup_subagentSkipsMainOnly(t *testing.T) {
	t.Cleanup(resetPostCleanup)
	resetPostCleanup()

	mainOnlyHits := 0
	safeHits := 0
	RegisterPostCleanup(CleanupOptions{Name: "main-only"}, func(s CleanupScope) {
		mainOnlyHits++
	})
	RegisterPostCleanup(CleanupOptions{Name: "safe", SubagentSafe: true}, func(s CleanupScope) {
		safeHits++
	})

	RunPostCleanup(ScopeSubagent)
	if mainOnlyHits != 0 {
		t.Errorf("main-only should not fire on subagent scope, got %d", mainOnlyHits)
	}
	if safeHits != 1 {
		t.Errorf("safe should fire on subagent scope, got %d", safeHits)
	}

	RunPostCleanup(ScopeMain)
	if mainOnlyHits != 1 || safeHits != 2 {
		t.Errorf("after main scope: mainOnly=%d safe=%d (want 1, 2)", mainOnlyHits, safeHits)
	}
}

func TestRegisterPostCleanup_emptyNameIgnored(t *testing.T) {
	t.Cleanup(resetPostCleanup)
	resetPostCleanup()

	calls := 0
	RegisterPostCleanup(CleanupOptions{Name: ""}, func(s CleanupScope) { calls++ })
	RegisterPostCleanup(CleanupOptions{Name: "ok"}, nil)
	RegisterPostCleanup(CleanupOptions{Name: "ok"}, func(s CleanupScope) { calls++ })

	RunPostCleanup(ScopeMain)
	if calls != 1 {
		t.Errorf("only the well-formed registration should fire, got %d", calls)
	}
}

func TestRegisterPostCleanup_panicIsolated(t *testing.T) {
	t.Cleanup(resetPostCleanup)
	resetPostCleanup()

	survivor := 0
	RegisterPostCleanup(CleanupOptions{Name: "boom"}, func(s CleanupScope) {
		panic("intentional")
	})
	RegisterPostCleanup(CleanupOptions{Name: "ok"}, func(s CleanupScope) {
		survivor++
	})

	// Must NOT panic.
	RunPostCleanup(ScopeMain)
	if survivor != 1 {
		t.Errorf("survivor should still fire after sibling panic, got %d", survivor)
	}
}

func TestRegisteredCleanupNames(t *testing.T) {
	t.Cleanup(resetPostCleanup)
	resetPostCleanup()

	for _, n := range []string{"a", "b", "c"} {
		RegisterPostCleanup(CleanupOptions{Name: n}, func(s CleanupScope) {})
	}
	got := RegisteredCleanupNames()
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	for i, w := range []string{"a", "b", "c"} {
		if got[i] != w {
			t.Errorf("got[%d] = %q, want %q", i, got[i], w)
		}
	}
}

func TestRegisterPostCleanup_iterationOrderStable(t *testing.T) {
	t.Cleanup(resetPostCleanup)
	resetPostCleanup()

	var order []string
	for _, n := range []string{"first", "second", "third"} {
		name := n
		RegisterPostCleanup(CleanupOptions{Name: name}, func(s CleanupScope) {
			order = append(order, name)
		})
	}
	RunPostCleanup(ScopeMain)
	if len(order) != 3 || order[0] != "first" || order[2] != "third" {
		t.Errorf("order = %v", order)
	}
}
