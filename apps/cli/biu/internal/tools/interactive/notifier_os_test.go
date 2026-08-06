package interactive

import (
	"strings"
	"testing"
)

func TestOsascriptStringEscapes(t *testing.T) {
	cases := map[string]string{
		`hello`:        `"hello"`,
		`he said "hi"`: `"he said \"hi\""`,
		`back\slash`:   `"back\\slash"`,
		"line\nbreak":  "\"line\nbreak\"",
	}
	for in, want := range cases {
		if got := osascriptString(in); got != want {
			t.Errorf("osascriptString(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPsEscape(t *testing.T) {
	if got := psEscape(`a "b" c`); got != `a ""b"" c` {
		t.Errorf("psEscape doubling failed: %q", got)
	}
}

// TestSystemNotifierAlwaysReturnsImpl ensures the platform switch
// doesn't return nil for any GOOS the build targets.
func TestSystemNotifierAlwaysReturnsImpl(t *testing.T) {
	n := SystemNotifier("test-title")
	if n == nil {
		t.Fatal("SystemNotifier returned nil")
	}
	// Internals: title should round-trip.
	if osn, ok := n.(*osNotifier); !ok || osn.title != "test-title" {
		t.Errorf("title not preserved: %+v", n)
	}
}

// TestNotifierEmptyMessageNoOps verifies short-circuit on blank.
func TestNotifierEmptyMessageNoOps(t *testing.T) {
	var sb strings.Builder
	n := &osNotifier{title: "biu", bell: true, stderr: &sb}
	if err := n.Notify(nil, ""); err != nil {
		t.Errorf("empty message should be a no-op: %v", err)
	}
	if sb.Len() != 0 {
		t.Errorf("empty message must not ring bell: %q", sb.String())
	}
}
