package repl

import (
	"strings"
	"testing"
)

func TestParseTagFlags_message(t *testing.T) {
	f := parseTagFlags([]string{"-m", "release", "v1.0"})
	if f.message != "release v1.0" {
		t.Errorf("message = %q", f.message)
	}
}

func TestParseTagFlags_messageStripsQuotes(t *testing.T) {
	f := parseTagFlags([]string{"-m", "\"release", "v1.0\""})
	if f.message != "release v1.0" {
		t.Errorf("message = %q", f.message)
	}
}

func TestParseTagFlags_auto(t *testing.T) {
	f := parseTagFlags([]string{"--auto"})
	if !f.auto {
		t.Error("auto not set")
	}
}

func TestParseTagFlags_autoFrom(t *testing.T) {
	f := parseTagFlags([]string{"--auto", "--from", "v0.9"})
	if !f.auto || f.from != "v0.9" {
		t.Errorf("got %+v", f)
	}
}

func TestHandleTag_autoNeedsProvider(t *testing.T) {
	got := model{}.handleTag([]string{"/tag", "v1", "--auto"})
	if !strings.Contains(got, "needs provider/model") {
		t.Errorf("unexpected: %s", got)
	}
}
