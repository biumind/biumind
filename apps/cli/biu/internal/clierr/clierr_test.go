package clierr

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewfShape(t *testing.T) {
	err := Newf("auth", "login failed: %d retries", 3)
	want := "auth: login failed: 3 retries"
	if err.Error() != want {
		t.Errorf("Newf wrong: %q want %q", err.Error(), want)
	}
}

func TestWrapfPreservesCause(t *testing.T) {
	cause := errors.New("connection refused")
	err := Wrapf("config", cause, "load %s", "/tmp/x")
	if !errors.Is(err, cause) {
		t.Errorf("Wrapf must preserve cause for errors.Is")
	}
	if !strings.HasPrefix(err.Error(), "config: load /tmp/x:") {
		t.Errorf("Wrapf prefix wrong: %q", err.Error())
	}
}

func TestWrapfNilReturnsNil(t *testing.T) {
	if got := Wrapf("x", nil, "y"); got != nil {
		t.Errorf("Wrapf(nil) should return nil, got %v", got)
	}
}

func TestWithHintAppendsAndPreservesCause(t *testing.T) {
	cause := errors.New("oh no")
	err := WithHint(cause, "run `biu doctor`")
	want := "oh no — run `biu doctor`"
	if err.Error() != want {
		t.Errorf("WithHint wrong: %q want %q", err.Error(), want)
	}
	if !errors.Is(err, cause) {
		t.Errorf("WithHint must preserve cause for errors.Is")
	}
	if !IsHinted(err) {
		t.Errorf("IsHinted should return true after WithHint")
	}
}

func TestWithHintNilReturnsNil(t *testing.T) {
	if got := WithHint(nil, "hint"); got != nil {
		t.Errorf("WithHint(nil, _) should be nil; got %v", got)
	}
}

func TestWithHintEmptyHintNoop(t *testing.T) {
	cause := errors.New("err")
	got := WithHint(cause, "")
	if got != cause {
		t.Errorf("empty hint should return original cause, got %v", got)
	}
}

func TestDisplayPathCollapsesHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("home not resolvable on this host")
	}
	cases := []struct {
		in, want string
	}{
		{filepath.Join(home, ".biu", "auth.json"), "~/.biu/auth.json"},
		{home, "~"},
		{"/tmp/elsewhere", "/tmp/elsewhere"},
		{"", ""},
	}
	for _, c := range cases {
		if got := DisplayPath(c.in); got != c.want {
			t.Errorf("DisplayPath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestDisplayPathDoesNotMatchPrefix(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("home not resolvable")
	}
	// Sibling directory whose name starts with the home basename
	// should not be collapsed.
	sibling := home + "x/file"
	if got := DisplayPath(sibling); got != sibling {
		t.Errorf("DisplayPath leaked across boundary: %q -> %q", sibling, got)
	}
}
