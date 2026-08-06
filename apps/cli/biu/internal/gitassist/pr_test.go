package gitassist

import (
	"context"
	"strings"
	"testing"
)

func TestMainBranch_fromOriginHEAD(t *testing.T) {
	r := newFakeRunner()
	r.out["symbolic-ref refs/remotes/origin/HEAD"] = "refs/remotes/origin/main\n"
	got, err := MainBranch(context.Background(), r.run)
	if err != nil {
		t.Fatal(err)
	}
	if got != "main" {
		t.Errorf("got %q, want main", got)
	}
}

func TestMainBranch_fallbackMaster(t *testing.T) {
	r := newFakeRunner()
	r.errOn["symbolic-ref refs/remotes/origin/HEAD"] = errStub
	r.errOn["rev-parse --verify main"] = errStub
	r.out["rev-parse --verify master"] = "abcd\n"
	got, _ := MainBranch(context.Background(), r.run)
	if got != "master" {
		t.Errorf("got %q, want master", got)
	}
}

func TestMainBranch_noneFound(t *testing.T) {
	r := newFakeRunner()
	r.errOn["symbolic-ref refs/remotes/origin/HEAD"] = errStub
	r.errOn["rev-parse --verify main"] = errStub
	r.errOn["rev-parse --verify master"] = errStub
	got, _ := MainBranch(context.Background(), r.run)
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestBranchDiff_emptyBaseErrors(t *testing.T) {
	_, err := BranchDiff(context.Background(), nil, "", 0)
	if err == nil {
		t.Error("empty base should error")
	}
}

func TestBranchDiff_passes3DotForm(t *testing.T) {
	r := newFakeRunner()
	r.out["diff --no-color main...HEAD"] = "diff body"
	got, _ := BranchDiff(context.Background(), r.run, "main", 0)
	if got != "diff body" {
		t.Errorf("got %q", got)
	}
}

func TestPRPrompt_includesAllSections(t *testing.T) {
	got := PRPrompt("feat/x", "main", "fix: a\nfeat: b", "DIFF_BODY")
	for _, want := range []string{
		"feat/x", "main",
		"## Summary", "## Test plan",
		"DIFF_BODY", "fix: a",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestSplitTitleBody_basic(t *testing.T) {
	in := "Add auth middleware\n\n## Summary\n- did stuff"
	title, body := SplitTitleBody(in)
	if title != "Add auth middleware" {
		t.Errorf("title = %q", title)
	}
	if !strings.Contains(body, "## Summary") {
		t.Errorf("body lost summary section: %q", body)
	}
}

func TestSplitTitleBody_stripsTitlePrefix(t *testing.T) {
	in := "Title: Add foo\n\nbody"
	title, _ := SplitTitleBody(in)
	if title != "Add foo" {
		t.Errorf("got %q, want 'Add foo'", title)
	}
}

func TestSplitTitleBody_handlesCodeFence(t *testing.T) {
	in := "```\nFix bug\n\nbody\n```"
	title, body := SplitTitleBody(in)
	if title != "Fix bug" {
		t.Errorf("title = %q", title)
	}
	if !strings.Contains(body, "body") {
		t.Errorf("body = %q", body)
	}
}

func TestSplitTitleBody_titleOnly(t *testing.T) {
	title, body := SplitTitleBody("just a title")
	if title != "just a title" {
		t.Errorf("title = %q", title)
	}
	if body != "" {
		t.Errorf("body = %q", body)
	}
}

// errStub is a sentinel for fakeRunner.errOn entries.
var errStub = stubError("stub")

type stubError string

func (s stubError) Error() string { return string(s) }
