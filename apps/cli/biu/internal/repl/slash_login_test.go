package repl

import (
	"strings"
	"testing"
)

// /login on a fresh home (no stored tokens) prompts the user to
// run `biu auth login`.
func TestSlashLogin_notSignedIn(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	got := model{}.handleLogin([]string{"/login"})
	for _, want := range []string{"not signed in", "biu auth login"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in output: %s", want, got)
		}
	}
}

// /logout when not signed in is a no-op with a friendly message.
func TestSlashLogout_notSignedIn(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	got := model{}.handleLogout([]string{"/logout"})
	if !strings.Contains(got, "not signed in") {
		t.Errorf("logout-without-login should be a no-op note, got %q", got)
	}
}

// /login never panics with a fresh / unwritable home.
func TestSlashLogin_neverPanics(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("/login panicked: %v", r)
		}
	}()
	_ = model{}.handleLogin([]string{"/login"})
	_ = model{}.handleLogout([]string{"/logout"})
}
