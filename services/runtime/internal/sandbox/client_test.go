package sandbox

import (
	"context"
	"errors"
	"strings"
	"testing"

	skillsreg "github.com/biumind/biumind/services/runtime/internal/skills"
)

// buildPrep is the test-side wrapper for the now method-form
// buildPrepCommand. We default to a Client with no Files fetcher,
// matching the original free-function semantics — Sha256-only
// resources skip silently.
func buildPrep(t *testing.T, s *skillsreg.Skill) string {
	t.Helper()
	c := &Client{}
	prep, err := c.buildPrepCommand(context.Background(), s)
	if err != nil {
		t.Fatalf("buildPrepCommand: %v", err)
	}
	return prep
}

// buildPrepCommand encodes skill.Resources into a single shell
// script. Tests pin the shape so a future regression (a missing
// mkdir, broken quoting, leaked plaintext) is caught at the unit
// level, not at the "why is the sandbox missing my file" level.

func TestBuildPrep_EmptyResourcesYieldsEmpty(t *testing.T) {
	cases := []*skillsreg.Skill{
		nil,
		{},
		{Resources: nil},
		{Resources: map[string]skillsreg.ResourceMeta{}},
		// Only CAS-backed entries (no Inline) — prep skips them; no
		// shell command needed at all.
		{Resources: map[string]skillsreg.ResourceMeta{
			"references/big.bin": {Sha256: "abc123", SizeBytes: 1 << 20},
		}},
	}
	for _, s := range cases {
		if got := buildPrep(t, s); got != "" {
			t.Errorf("expected empty prep; got %q", got)
		}
	}
}

func TestBuildPrep_InlineResourceIsBase64Decoded(t *testing.T) {
	s := &skillsreg.Skill{
		Resources: map[string]skillsreg.ResourceMeta{
			"references/checklist.md": {Inline: "- item 1\n- item 2\n"},
		},
	}
	prep := buildPrep(t, s)
	if prep == "" {
		t.Fatal("expected non-empty prep")
	}
	// set -e first.
	if !strings.HasPrefix(prep, "set -e\n") {
		t.Errorf("prep should start with `set -e`; got %q", prep[:20])
	}
	// mkdir for the parent dir.
	if !strings.Contains(prep, "mkdir -p /skill/references") {
		t.Errorf("missing mkdir; got %q", prep)
	}
	// base64 -d pipeline writing the file.
	if !strings.Contains(prep, "base64 -d > /skill/references/checklist.md") {
		t.Errorf("missing base64 pipeline; got %q", prep)
	}
	// Plaintext content must NOT appear — base64 only.
	if strings.Contains(prep, "item 1") {
		t.Errorf("plaintext leaked into prep; quoting bug? got %q", prep)
	}
}

func TestBuildPrep_RejectsTraversal(t *testing.T) {
	// Defensive: even though installer.go already filters these,
	// don't write to absolute / parent paths on data read back
	// from DB.
	s := &skillsreg.Skill{
		Resources: map[string]skillsreg.ResourceMeta{
			"../etc/passwd": {Inline: "evil"},
			"/absolute/x":   {Inline: "evil"},
		},
	}
	prep := buildPrep(t, s)
	if prep != "" {
		t.Errorf("traversal entries should yield empty prep; got %q", prep)
	}
}

func TestBuildPrep_QuotesPathsWithSpecialChars(t *testing.T) {
	s := &skillsreg.Skill{
		Resources: map[string]skillsreg.ResourceMeta{
			"weird path/spaces & stuff.md": {Inline: "x"},
		},
	}
	prep := buildPrep(t, s)
	// The path with spaces / & must be single-quoted in the shell
	// command — otherwise sh would split argv mid-word.
	if !strings.Contains(prep, `'weird path/spaces & stuff.md'`) &&
		!strings.Contains(prep, `'/skill/weird path/spaces & stuff.md'`) {
		t.Errorf("special-char path must be quoted; got %q", prep)
	}
}

func TestShellQuote(t *testing.T) {
	cases := map[string]string{
		"safe":           "safe",
		"with space":     "'with space'",
		"with'quote":     `'with'\''quote'`,
		"empty content":  "'empty content'",
		"normal-name.md": "normal-name.md",
		"a$b":            "'a$b'",
	}
	for in, want := range cases {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBuildPrep_MultipleFilesAllAppear(t *testing.T) {
	s := &skillsreg.Skill{
		Resources: map[string]skillsreg.ResourceMeta{
			"a.md":           {Inline: "a"},
			"refs/b.md":      {Inline: "b"},
			"scripts/run.sh": {Inline: "#!/bin/sh\necho hi\n"},
		},
	}
	prep := buildPrep(t, s)
	for _, want := range []string{
		"/skill/a.md", "/skill/refs/b.md", "/skill/scripts/run.sh",
	} {
		if !strings.Contains(prep, want) {
			t.Errorf("missing file %q in prep; got %q", want, prep)
		}
	}
}

// ─── CAS fetcher integration ──────────────────────────────

// stubFetcher records every FetchByHash call; tests use it to assert
// the prep command actually went through the CAS path for sha-only
// resources.
type stubFetcher struct {
	bodies map[string][]byte
	err    error
	calls  []string
}

func (s *stubFetcher) FetchByHash(_ context.Context, hash string) ([]byte, error) {
	s.calls = append(s.calls, hash)
	if s.err != nil {
		return nil, s.err
	}
	body, ok := s.bodies[hash]
	if !ok {
		return nil, errors.New("not found")
	}
	return body, nil
}

func TestBuildPrep_FetchesCASWhenInlineEmpty(t *testing.T) {
	body := []byte("LARGE_RESOURCE_CONTENT_BEYOND_4KB_OR_BINARY_BLOB")
	stub := &stubFetcher{bodies: map[string][]byte{
		"abc123def": body,
	}}
	c := &Client{Files: stub}
	skill := &skillsreg.Skill{Resources: map[string]skillsreg.ResourceMeta{
		"references/big.bin": {Sha256: "abc123def", SizeBytes: int64(len(body))},
	}}
	prep, err := c.buildPrepCommand(context.Background(), skill)
	if err != nil {
		t.Fatalf("buildPrepCommand: %v", err)
	}
	if prep == "" {
		t.Fatal("CAS-backed resource should yield non-empty prep when fetcher wired")
	}
	if len(stub.calls) != 1 || stub.calls[0] != "abc123def" {
		t.Errorf("fetcher called incorrectly: %v", stub.calls)
	}
	if !strings.Contains(prep, "/skill/references/big.bin") {
		t.Errorf("missing target path: %q", prep)
	}
}

func TestBuildPrep_FetchErrorPropagates(t *testing.T) {
	stub := &stubFetcher{err: errors.New("upstream 503")}
	c := &Client{Files: stub}
	skill := &skillsreg.Skill{Resources: map[string]skillsreg.ResourceMeta{
		"x.bin": {Sha256: "deadbeef", SizeBytes: 1},
	}}
	_, err := c.buildPrepCommand(context.Background(), skill)
	if err == nil {
		t.Fatal("fetcher error must surface — silent skip would leak missing file into the run")
	}
	if !strings.Contains(err.Error(), "upstream 503") {
		t.Errorf("wrapped error should carry upstream cause; got %v", err)
	}
}

func TestBuildPrep_MixedInlineAndCAS(t *testing.T) {
	stub := &stubFetcher{bodies: map[string][]byte{
		"hash1": []byte("from-cas"),
	}}
	c := &Client{Files: stub}
	skill := &skillsreg.Skill{Resources: map[string]skillsreg.ResourceMeta{
		"inline.md":   {Inline: "inlined-body"},
		"large.bin":   {Sha256: "hash1", SizeBytes: 1024 * 100},
	}}
	prep, err := c.buildPrepCommand(context.Background(), skill)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"/skill/inline.md", "/skill/large.bin"} {
		if !strings.Contains(prep, want) {
			t.Errorf("missing %q in prep: %s", want, prep)
		}
	}
}
