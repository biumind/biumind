package files

import (
	"strings"
	"testing"
)

func TestUnifiedDiffEmptyForIdentical(t *testing.T) {
	if got := UnifiedDiff("x", "abc\n", "abc\n", 3); got != "" {
		t.Errorf("identical inputs should yield empty diff; got %q", got)
	}
}

func TestUnifiedDiffSimpleReplace(t *testing.T) {
	before := "line one\nold middle\nline three\n"
	after := "line one\nnew middle\nline three\n"
	got := UnifiedDiff("x.go", before, after, 1)
	if !strings.Contains(got, "--- a/x.go") || !strings.Contains(got, "+++ b/x.go") {
		t.Errorf("missing header: %s", got)
	}
	if !strings.Contains(got, "-old middle") {
		t.Errorf("missing removal: %s", got)
	}
	if !strings.Contains(got, "+new middle") {
		t.Errorf("missing addition: %s", got)
	}
	if !strings.Contains(got, "@@") {
		t.Errorf("missing hunk header: %s", got)
	}
}

func TestUnifiedDiffPureAddition(t *testing.T) {
	got := UnifiedDiff("x", "alpha\nbeta\n", "alpha\nbeta\ngamma\n", 1)
	if !strings.Contains(got, "+gamma") {
		t.Errorf("addition missing: %s", got)
	}
	if strings.Contains(got, "-") && !strings.Contains(got, "--- a/") {
		t.Errorf("unexpected removal in addition-only diff")
	}
}

func TestUnifiedDiffPureDeletion(t *testing.T) {
	got := UnifiedDiff("x", "alpha\nbeta\ngamma\n", "alpha\ngamma\n", 1)
	if !strings.Contains(got, "-beta") {
		t.Errorf("deletion missing: %s", got)
	}
}
