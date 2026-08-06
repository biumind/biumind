package gitassist

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeRunner records args + returns canned outputs keyed on the
// joined arg string.
type fakeRunner struct {
	calls   [][]string
	out     map[string]string
	errOn   map[string]error
}

func (f *fakeRunner) run(ctx context.Context, args ...string) (string, error) {
	f.calls = append(f.calls, append([]string(nil), args...))
	key := strings.Join(args, " ")
	if err, ok := f.errOn[key]; ok {
		return "", err
	}
	return f.out[key], nil
}

func newFakeRunner() *fakeRunner {
	return &fakeRunner{out: map[string]string{}, errOn: map[string]error{}}
}

// ─── Status parser ───────────────────────────────────────────

func TestParseStatus_modifiedAndStaged(t *testing.T) {
	raw := " M foo.go\nM  bar.go\nMM baz.go\n?? new.go\nA  added.go\n"
	st := ParseStatus(raw)
	if len(st.Modified) != 2 || st.Modified[0] != "foo.go" || st.Modified[1] != "baz.go" {
		t.Errorf("modified = %v", st.Modified)
	}
	if len(st.Staged) != 3 {
		t.Errorf("staged = %v", st.Staged)
	}
	if len(st.Untracked) != 1 || st.Untracked[0] != "new.go" {
		t.Errorf("untracked = %v", st.Untracked)
	}
}

func TestParseStatus_conflicts(t *testing.T) {
	raw := "UU merge.go\nAA both.go\n M plain.go\n"
	st := ParseStatus(raw)
	if len(st.Conflicts) != 2 {
		t.Errorf("conflicts = %v", st.Conflicts)
	}
}

func TestParseStatus_renames(t *testing.T) {
	raw := "R  old.go -> new.go\n"
	st := ParseStatus(raw)
	if len(st.Staged) != 1 || st.Staged[0] != "new.go" {
		t.Errorf("rename should keep new path: %v", st.Staged)
	}
}

func TestStatus_empty(t *testing.T) {
	if !ParseStatus("").Empty() {
		t.Error("empty input → Empty() true")
	}
	if ParseStatus(" M f\n").Empty() {
		t.Error("non-empty status → Empty() false")
	}
}

func TestGetStatus_runnerError(t *testing.T) {
	r := newFakeRunner()
	r.errOn["status --porcelain=v1"] = errors.New("not a repo")
	_, err := GetStatus(context.Background(), r.run)
	if err == nil {
		t.Error("runner error should propagate")
	}
}

// ─── Diff ───────────────────────────────────────────────────

func TestDiff_truncates(t *testing.T) {
	r := newFakeRunner()
	long := strings.Repeat("x", 10_000)
	r.out["diff --no-color"] = long
	out, _ := Diff(context.Background(), r.run, false, 100)
	if len(out) > 200 {
		t.Errorf("not truncated: len=%d", len(out))
	}
	if !strings.Contains(out, "truncated") {
		t.Errorf("missing truncation marker: %s", out[len(out)-30:])
	}
}

func TestDiff_stagedFlag(t *testing.T) {
	r := newFakeRunner()
	r.out["diff --no-color --staged"] = "diff body"
	out, err := Diff(context.Background(), r.run, true, 0)
	if err != nil {
		t.Fatal(err)
	}
	if out != "diff body" {
		t.Errorf("got %q", out)
	}
	last := r.calls[len(r.calls)-1]
	if last[2] != "--staged" {
		t.Errorf("missing --staged flag: %v", last)
	}
}

// ─── Branch ─────────────────────────────────────────────────

func TestCurrentBranch_normal(t *testing.T) {
	r := newFakeRunner()
	r.out["rev-parse --abbrev-ref HEAD"] = "feature/x\n"
	got, _ := CurrentBranch(context.Background(), r.run)
	if got != "feature/x" {
		t.Errorf("got %q", got)
	}
}

func TestCurrentBranch_detached(t *testing.T) {
	r := newFakeRunner()
	r.out["rev-parse --abbrev-ref HEAD"] = "HEAD\n"
	got, _ := CurrentBranch(context.Background(), r.run)
	if got != "(detached)" {
		t.Errorf("got %q", got)
	}
}

// ─── Prompt + Generator ─────────────────────────────────────

func TestCommitMessagePrompt_includesAllParts(t *testing.T) {
	got := CommitMessagePrompt("DIFF_BODY", "fix: a\nfeat: b")
	for _, want := range []string{
		"Conventional Commits", "≤72 characters",
		"DIFF_BODY", "fix: a", "feat: b",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestCommitMessagePrompt_omitsLogWhenEmpty(t *testing.T) {
	got := CommitMessagePrompt("DIFF", "")
	if strings.Contains(got, "Recent commit subjects") {
		t.Errorf("empty log should suppress section: %s", got)
	}
}

func TestGenerateCommitMessage_callsGenerator(t *testing.T) {
	var seen string
	gen := func(ctx context.Context, prompt string) (string, error) {
		seen = prompt
		return "feat(x): hello\n\nbody here", nil
	}
	got, err := GenerateCommitMessage(context.Background(), gen, "DIFF", "log")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(seen, "DIFF") {
		t.Error("generator should receive prompt with diff")
	}
	if !strings.HasPrefix(got, "feat(x):") {
		t.Errorf("got %q", got)
	}
}

func TestGenerateCommitMessage_stripsCodeFences(t *testing.T) {
	gen := func(ctx context.Context, prompt string) (string, error) {
		return "```text\nfeat: x\n```\n", nil
	}
	got, _ := GenerateCommitMessage(context.Background(), gen, "DIFF", "")
	if got != "feat: x" {
		t.Errorf("got %q, want 'feat: x'", got)
	}
}

func TestGenerateCommitMessage_emptyDiff(t *testing.T) {
	gen := func(ctx context.Context, prompt string) (string, error) {
		t.Error("generator should not be called on empty diff")
		return "", nil
	}
	_, err := GenerateCommitMessage(context.Background(), gen, "   ", "")
	if err == nil {
		t.Error("empty diff should error")
	}
}

func TestGenerateCommitMessage_nilGenerator(t *testing.T) {
	_, err := GenerateCommitMessage(context.Background(), nil, "DIFF", "")
	if err == nil {
		t.Error("nil generator should error")
	}
}

func TestGenerateCommitMessage_propagatesGenErr(t *testing.T) {
	gen := func(ctx context.Context, prompt string) (string, error) {
		return "", errors.New("api 500")
	}
	_, err := GenerateCommitMessage(context.Background(), gen, "DIFF", "")
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Errorf("err = %v", err)
	}
}

// ─── CleanLLMText ───────────────────────────────────────────

func TestCleanLLMText(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"hello", "hello"},
		{"  hello  \n", "hello"},
		{"```\nbody\n```", "body"},
		{"```text\nfoo bar\n```", "foo bar"},
		{"`single backtick`", "`single backtick`"},
	}
	for _, tc := range cases {
		if got := CleanLLMText(tc.in); got != tc.want {
			t.Errorf("CleanLLMText(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
