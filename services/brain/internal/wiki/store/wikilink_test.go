// RewriteWikilinks unit tests — pure function, no DB.
// The merge-rewrite integration test lives in merge_rewrite_test.go
// (DATABASE_URL gated).

package store

import (
	"strings"
	"testing"
)

func TestRewriteWikilinks_PlainTarget(t *testing.T) {
	body := "see [[Beta]] for details"
	got, n := RewriteWikilinks(body, "Beta", "Alpha")
	if n != 1 {
		t.Fatalf("want 1 replacement, got %d", n)
	}
	if got != "see [[Alpha]] for details" {
		t.Errorf("unexpected body: %q", got)
	}
}

func TestRewriteWikilinks_PreservesAlias(t *testing.T) {
	body := "see [[Beta|B 的别名]] here"
	got, n := RewriteWikilinks(body, "Beta", "Alpha")
	if n != 1 {
		t.Fatalf("want 1 replacement, got %d", n)
	}
	if got != "see [[Alpha|B 的别名]] here" {
		t.Errorf("alias not preserved: %q", got)
	}
}

func TestRewriteWikilinks_MultipleOccurrences(t *testing.T) {
	body := "[[Beta]] and [[Beta|x]] and [[Beta]]"
	got, n := RewriteWikilinks(body, "Beta", "Alpha")
	if n != 3 {
		t.Fatalf("want 3 replacements, got %d (body=%q)", n, got)
	}
	if strings.Contains(got, "Beta") {
		t.Errorf("old target still present: %q", got)
	}
}

// The source-delete-decision lesson: a naive substring replace rewrites
// `[[Beta2]]` when renaming "Beta". Exact-target matching must not.
func TestRewriteWikilinks_DoesNotTouchSubstringTargets(t *testing.T) {
	body := "[[Beta2]] and [[Beta]] and [[BetaX|alias]]"
	got, n := RewriteWikilinks(body, "Beta", "Alpha")
	if n != 1 {
		t.Fatalf("want exactly 1 replacement, got %d (body=%q)", n, got)
	}
	if !strings.Contains(got, "[[Beta2]]") || !strings.Contains(got, "[[BetaX|alias]]") {
		t.Errorf("substring targets corrupted: %q", got)
	}
	if !strings.Contains(got, "[[Alpha]]") {
		t.Errorf("exact target not rewritten: %q", got)
	}
}

func TestRewriteWikilinks_CaseInsensitive(t *testing.T) {
	// Wikilink resolution is case-insensitive (lint normalises to
	// lowercase), so the rewrite must fold case too.
	body := "[[beta]] and [[BETA|label]]"
	got, n := RewriteWikilinks(body, "Beta", "Alpha")
	if n != 2 {
		t.Fatalf("want 2 replacements, got %d (body=%q)", n, got)
	}
	if got != "[[Alpha]] and [[Alpha|label]]" {
		t.Errorf("unexpected body: %q", got)
	}
}

func TestRewriteWikilinks_WhitespaceTolerant(t *testing.T) {
	body := "[[ Beta ]] and [[Beta | alias]]"
	got, n := RewriteWikilinks(body, "Beta", "Alpha")
	if n != 2 {
		t.Fatalf("want 2 replacements, got %d (body=%q)", n, got)
	}
	if got != "[[Alpha]] and [[Alpha| alias]]" {
		t.Errorf("unexpected body: %q", got)
	}
}

func TestRewriteWikilinks_CJK(t *testing.T) {
	body := "参考 [[旧标题]] 与 [[旧标题|说明]]，以及 [[旧标题2]]"
	got, n := RewriteWikilinks(body, "旧标题", "新标题")
	if n != 2 {
		t.Fatalf("want 2 replacements, got %d (body=%q)", n, got)
	}
	want := "参考 [[新标题]] 与 [[新标题|说明]]，以及 [[旧标题2]]"
	if got != want {
		t.Errorf("want %q, got %q", want, got)
	}
}

func TestRewriteWikilinks_RegexMetacharsInTarget(t *testing.T) {
	body := "[[C++ (语言)]] and [[C++ (语言)2]]"
	got, n := RewriteWikilinks(body, "C++ (语言)", "Go (语言)")
	if n != 1 {
		t.Fatalf("want 1 replacement, got %d (body=%q)", n, got)
	}
	if got != "[[Go (语言)]] and [[C++ (语言)2]]" {
		t.Errorf("unexpected body: %q", got)
	}
}

func TestRewriteWikilinks_DollarInNewTargetIsLiteral(t *testing.T) {
	body := "[[old]]"
	got, n := RewriteWikilinks(body, "old", "new$1")
	if n != 1 || got != "[[new$1]]" {
		t.Errorf("$ must be literal in replacement: n=%d body=%q", n, got)
	}
}

func TestRewriteWikilinks_EmptyInputs(t *testing.T) {
	for _, tc := range [][3]string{
		{"", "a", "b"},
		{"[[a]]", "", "b"},
		{"[[a]]", "a", ""},
		{"[[a]]", "  ", "b"},
	} {
		if got, n := RewriteWikilinks(tc[0], tc[1], tc[2]); n != 0 || got != tc[0] {
			t.Errorf("RewriteWikilinks(%q,%q,%q) = (%q,%d), want unchanged/0",
				tc[0], tc[1], tc[2], got, n)
		}
	}
}

func TestRewriteWikilinks_NoMatch(t *testing.T) {
	body := "nothing to rewrite [[Other]]"
	got, n := RewriteWikilinks(body, "Beta", "Alpha")
	if n != 0 || got != body {
		t.Errorf("want unchanged body, got n=%d body=%q", n, got)
	}
}
