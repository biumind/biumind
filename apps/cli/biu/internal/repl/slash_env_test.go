package repl

import (
	"strings"
	"testing"
)

func TestRedactSecret_keyHidden(t *testing.T) {
	got := redactSecret("ANTHROPIC_API_KEY", "sk-ant-abcdefghij-XYZ9")
	if strings.Contains(got, "sk-ant-abcdefghij") {
		t.Errorf("full key leaked: %s", got)
	}
	if !strings.Contains(got, "XYZ9") {
		t.Errorf("last4 missing: %s", got)
	}
}

func TestRedactSecret_tokenHidden(t *testing.T) {
	got := redactSecret("BIU_BRIDGE_TOKEN", "deadbeef-very-long")
	if strings.Contains(got, "deadbeef") {
		t.Errorf("token leaked: %s", got)
	}
}

func TestRedactSecret_shortKey(t *testing.T) {
	got := redactSecret("BIU_API_KEY", "abc")
	if got != "<set>" {
		t.Errorf("short key should be plain set marker: %s", got)
	}
}

func TestRedactSecret_pathTruncated(t *testing.T) {
	long := strings.Repeat("/usr/local/bin:", 20)
	got := redactSecret("PATH", long)
	if len(got) > 100 {
		t.Errorf("PATH not truncated: len=%d", len(got))
	}
}

func TestRedactSecret_normalPassthrough(t *testing.T) {
	got := redactSecret("BIU_MODEL", "claude-opus-4-7")
	if got != "claude-opus-4-7" {
		t.Errorf("non-sensitive should pass through: %s", got)
	}
}

func TestHandleEnv_groupHeaders(t *testing.T) {
	got := model{}.handleEnv([]string{"/env"})
	for _, want := range []string{"[provider]", "[config]", "[shell]"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q: %s", want, got)
		}
	}
}

func TestHandleEnv_filter(t *testing.T) {
	got := model{}.handleEnv([]string{"/env", "bridge"})
	if !strings.Contains(got, "[bridge]") {
		t.Errorf("bridge group missing: %s", got)
	}
	// Other groups should be filtered out.
	if strings.Contains(got, "[shell]") {
		t.Errorf("filter should hide unrelated groups: %s", got)
	}
}

func TestHandleEnv_setKeyShowsRedacted(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-redactme-LAST")
	got := model{}.handleEnv([]string{"/env"})
	if strings.Contains(got, "redactme") {
		t.Errorf("key body leaked: %s", got)
	}
	if !strings.Contains(got, "LAST") {
		t.Errorf("last4 missing: %s", got)
	}
}
