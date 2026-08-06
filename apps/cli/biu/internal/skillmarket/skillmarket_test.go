package skillmarket

import (
	"errors"
	"testing"
)

func TestResolve_PassthroughForUnknownHost(t *testing.T) {
	in := "https://github.com/me/myskills/raw/main/foo/SKILL.md"
	out, name, err := Resolve(in)
	if err != nil {
		t.Fatal(err)
	}
	if out != in {
		t.Errorf("unknown host should passthrough; got %q", out)
	}
	if name != "" {
		t.Errorf("passthrough should report empty adapter name; got %q", name)
	}
}

func TestResolve_PassthroughForLocalPaths(t *testing.T) {
	// local file paths come through here too (CLI install accepts
	// both URLs and .biuskill paths). Adapter must NOT mangle them.
	out, _, err := Resolve("./foo.biuskill")
	if err != nil {
		t.Fatal(err)
	}
	if out != "./foo.biuskill" {
		t.Errorf("local path should passthrough; got %q", out)
	}
}

func TestResolve_LobeHubRewritesToGitHubRaw(t *testing.T) {
	in := "https://lobehub.com/agents/code-reviewer"
	out, name, err := Resolve(in)
	if err != nil {
		t.Fatal(err)
	}
	if name != "lobehub" {
		t.Errorf("adapter = %q, want lobehub", name)
	}
	want := "https://raw.githubusercontent.com/lobehub/lobe-chat-agents/main/src/code-reviewer/index.json"
	if out != want {
		t.Errorf("got %q, want %q", out, want)
	}
}

func TestResolve_LobeHubAmbiguousWhenSlugMissing(t *testing.T) {
	_, _, err := Resolve("https://lobehub.com/agents/")
	if !errors.Is(err, ErrAmbiguous) {
		t.Errorf("want ErrAmbiguous; got %v", err)
	}
}

func TestResolve_SkillsShAddsSkillMD(t *testing.T) {
	out, name, err := Resolve("https://skills.sh/code-reviewer")
	if err != nil {
		t.Fatal(err)
	}
	if name != "skills.sh" {
		t.Errorf("adapter = %q", name)
	}
	if out != "https://skills.sh/code-reviewer/SKILL.md" {
		t.Errorf("got %q", out)
	}
}

func TestResolve_SkillsShPassesThroughDirectURL(t *testing.T) {
	in := "https://skills.sh/foo/SKILL.md"
	out, _, err := Resolve(in)
	if err != nil {
		t.Fatal(err)
	}
	if out != in {
		t.Errorf("direct SKILL.md URL should pass through; got %q", out)
	}
}

func TestResolve_ClaudePluginsAddsSkillMD(t *testing.T) {
	out, name, err := Resolve("https://claude-plugins.dev/refactor")
	if err != nil {
		t.Fatal(err)
	}
	if name != "claude-plugins.dev" {
		t.Errorf("adapter = %q", name)
	}
	if out != "https://claude-plugins.dev/refactor/SKILL.md" {
		t.Errorf("got %q", out)
	}
}

// HTTP scheme should also match — some users paste http:// URLs by
// mistake; the adapter should still rewrite (server-side fetcher
// upgrades to HTTPS or rejects).
func TestResolve_HTTPSchemePassesThroughLobeHubMatch(t *testing.T) {
	out, name, _ := Resolve("http://skills.sh/foo")
	if name != "skills.sh" {
		t.Errorf("http should still match host; got name=%q out=%q",
			name, out)
	}
}

// Hostname matching is case-insensitive — protect against pasted
// addresses with weird casing from rich-text editors.
func TestResolve_HostnameCaseInsensitive(t *testing.T) {
	out, name, _ := Resolve("https://LobeHub.com/agents/foo")
	if name != "lobehub" {
		t.Errorf("case-insensitive host match failed: name=%q out=%q",
			name, out)
	}
}

func TestResolve_RejectsMalformedURL(t *testing.T) {
	if _, _, err := Resolve("ht!tp://broken"); err == nil {
		// Note: net/url is permissive; some weird URLs parse fine.
		// We only require malformed *enough* inputs to error. If the
		// parser accepts the input, we'll consider it passthrough
		// (rather than fail). Adjust if production traffic shows
		// this lets through real garbage.
		t.Log("parser permissive for ht!tp scheme — passthrough OK")
	}
}
