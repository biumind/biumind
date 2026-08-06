package plans

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writePlanFile(t *testing.T, dir, id, body string, age time.Duration) string {
	t.Helper()
	path := filepath.Join(dir, id+".md")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	mt := time.Now().Add(-age)
	_ = os.Chtimes(path, mt, mt)
	return path
}

func TestListPlansSortsByMTime(t *testing.T) {
	dir := t.TempDir()
	writePlanFile(t, dir, "20260101-120000-aaaa", "# old", 48*time.Hour)
	writePlanFile(t, dir, "20260102-120000-bbbb", "# newer", 24*time.Hour)
	writePlanFile(t, dir, "20260103-120000-cccc", "# newest", 0)

	plans, err := ListPlans(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 3 {
		t.Fatalf("expected 3 plans; got %d", len(plans))
	}
	if !strings.HasPrefix(plans[0].ID, "20260103") {
		t.Errorf("newest first; got %s", plans[0].ID)
	}
	if !strings.HasPrefix(plans[2].ID, "20260101") {
		t.Errorf("oldest last; got %s", plans[2].ID)
	}
}

func TestListPlansMissingDir(t *testing.T) {
	plans, err := ListPlans(filepath.Join(t.TempDir(), "nope"))
	if err != nil {
		t.Errorf("missing dir should not error; got %v", err)
	}
	if len(plans) != 0 {
		t.Errorf("expected empty; got %d", len(plans))
	}
}

func TestFindByIDLatestPrefixExact(t *testing.T) {
	dir := t.TempDir()
	writePlanFile(t, dir, "20260101-120000-aaaa", "# a", 48*time.Hour)
	writePlanFile(t, dir, "20260102-120000-bbbb", "# b", 24*time.Hour)
	writePlanFile(t, dir, "20260103-120000-cccc", "# c", 0)

	// latest
	got, ok := FindByID(dir, "latest")
	if !ok || !strings.HasPrefix(got.ID, "20260103") {
		t.Errorf("latest mismatch: %v ok=%v", got, ok)
	}
	// empty ref defaults to latest
	got, ok = FindByID(dir, "")
	if !ok || !strings.HasPrefix(got.ID, "20260103") {
		t.Errorf("empty ref mismatch: %v ok=%v", got, ok)
	}
	// exact
	got, ok = FindByID(dir, "20260102-120000-bbbb")
	if !ok || got.ID != "20260102-120000-bbbb" {
		t.Errorf("exact match failed: %+v", got)
	}
	// unambiguous prefix
	got, ok = FindByID(dir, "20260101")
	if !ok {
		t.Errorf("prefix should match; got nothing")
	}
	// ambiguous prefix
	_, ok = FindByID(dir, "2026")
	if ok {
		t.Errorf("ambiguous prefix must not resolve")
	}
}

func TestReadStripsHeader(t *testing.T) {
	dir := t.TempDir()
	body := "<!-- biu plan, written 2026-01-01T12:00:00Z -->\n\n## Steps\n1. read\n2. propose\n"
	writePlanFile(t, dir, "x", body, 0)
	p, _ := FindByID(dir, "x")
	got, err := Read(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "<!--") {
		t.Errorf("header should be stripped: %q", got)
	}
	if !strings.Contains(got, "## Steps") {
		t.Errorf("body lost: %q", got)
	}
}

func TestRemoveOlderThan(t *testing.T) {
	dir := t.TempDir()
	writePlanFile(t, dir, "old", "# old", 30*24*time.Hour)
	writePlanFile(t, dir, "fresh", "# fresh", 1*time.Hour)
	n, err := RemoveOlderThan(dir, 7*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("expected 1 removed; got %d", n)
	}
	rows, _ := ListPlans(dir)
	if len(rows) != 1 || rows[0].ID != "fresh" {
		t.Errorf("wrong file kept: %+v", rows)
	}
}

func TestParseDuration(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"30d", 30 * 24 * time.Hour},
		{"2w", 14 * 24 * time.Hour},
		{"4h", 4 * time.Hour},
		{"15m", 15 * time.Minute},
	}
	for _, c := range cases {
		got, err := ParseDuration(c.in)
		if err != nil {
			t.Errorf("ParseDuration(%q) errored: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ParseDuration(%q) = %v, want %v", c.in, got, c.want)
		}
	}
	if _, err := ParseDuration("garbage"); err == nil {
		t.Errorf("garbage input should error")
	}
}

func TestFirstSignificantLineSkipsCommentAndMarkers(t *testing.T) {
	dir := t.TempDir()
	body := "<!-- biu plan -->\n\n# Plan\n## Steps\n- do thing\n"
	writePlanFile(t, dir, "x", body, 0)
	rows, _ := ListPlans(dir)
	if len(rows) != 1 {
		t.Fatal("expected 1 plan")
	}
	if rows[0].FirstLine != "Plan" {
		t.Errorf("first line should strip comment + markdown marker; got %q", rows[0].FirstLine)
	}
}
