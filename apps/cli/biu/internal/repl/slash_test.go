package repl

import "testing"

func TestSlashTrigger(t *testing.T) {
	cases := map[string]bool{
		"":          false,
		"hello":     false,
		"/":         true,
		"/help":     true,
		"/he":       true,
		"/model x":  false, // arg present, hide menu
		"/quit ":    false,
	}
	for in, want := range cases {
		if got := isSlashTrigger(in); got != want {
			t.Errorf("isSlashTrigger(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestMatchSlashByPrefix(t *testing.T) {
	if got := matchSlash("/c", nil); len(got) != 5 {
		t.Errorf("expected 5 matches for /c (clear, commit, compact, copy, cost), got %d: %+v",
			len(got), got)
	}
	// Sanity: full list comes back for "/" or empty.
	if got := matchSlash("/", nil); len(got) != len(slashCmds) {
		t.Errorf("expected full list, got %d", len(got))
	}
}

func TestMatchSlashEmpty(t *testing.T) {
	if got := matchSlash("/zzznoexist", nil); len(got) != 0 {
		t.Errorf("expected 0 matches, got %d", len(got))
	}
}
