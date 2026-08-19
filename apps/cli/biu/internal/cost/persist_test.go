package cost

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoggerAppendAndLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "usage.jsonl")
	l, err := NewLogger(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := l.Append(UsageRecord{
			SessionID: "s-1", Model: "claude-opus-4-7",
			Input: 10, Output: 5,
		}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := LoadAll(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Errorf("expected 3 records, got %d", len(got))
	}
	for _, r := range got {
		if r.TS.IsZero() {
			t.Errorf("TS not auto-populated: %+v", r)
		}
	}
}

func TestLoadAllMissingFile(t *testing.T) {
	got, err := LoadAll(filepath.Join(t.TempDir(), "nope.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("missing file should yield empty; got %d", len(got))
	}
}

func TestAggregateByDay(t *testing.T) {
	// 锚定到当天 UTC 正午：直接取 time.Now() 时，若测试在 UTC 日界
	// 后一小时内运行，now.Add(-time.Hour) 会落到前一天，today 桶只有
	// 1 条记录导致失败（2026-08-19 CI 实测复现）。
	now := time.Now().UTC().Truncate(24*time.Hour).Add(12 * time.Hour)
	records := []UsageRecord{
		{TS: now, Model: "opus", Input: 100, Output: 10, USD: 0.1},
		{TS: now.Add(-time.Hour), Model: "opus", Input: 200, Output: 20, USD: 0.2},
		{TS: now.AddDate(0, 0, -1), Model: "opus", Input: 50, Output: 5, USD: 0.05},
	}
	rows := Aggregate(records, BucketDay, time.Time{})
	if len(rows) != 2 {
		t.Fatalf("expected 2 day buckets, got %d", len(rows))
	}
	// Newest first.
	if rows[0].Period <= rows[1].Period {
		t.Errorf("buckets not sorted newest-first: %+v", rows)
	}
	// Today's bucket sums the two same-day records.
	if rows[0].Input != 300 || rows[0].Turns != 2 {
		t.Errorf("today's bucket wrong: %+v", rows[0])
	}
}

func TestAggregateRespectsCutoff(t *testing.T) {
	now := time.Now().UTC()
	records := []UsageRecord{
		{TS: now.AddDate(0, 0, -1), Model: "opus", Input: 1},
		{TS: now.AddDate(0, 0, -10), Model: "opus", Input: 2},
		{TS: now.AddDate(0, 0, -30), Model: "opus", Input: 3},
	}
	cutoff := now.AddDate(0, 0, -7)
	rows := Aggregate(records, BucketDay, cutoff)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row inside cutoff; got %d", len(rows))
	}
	if rows[0].Input != 1 {
		t.Errorf("only the recent record should be aggregated: %+v", rows)
	}
}

func TestFormatTableEmptyAndPopulated(t *testing.T) {
	if got := FormatTable(nil); !strings.Contains(got, "no usage") {
		t.Errorf("empty case missing message: %q", got)
	}
	rows := []Summary{{
		Period: "2026-05-25", Model: "claude-opus-4-7",
		Input: 100, Output: 50, CacheRead: 20, USD: 0.1234, Turns: 2,
	}}
	out := FormatTable(rows)
	if !strings.Contains(out, "claude-opus-4-7") {
		t.Errorf("missing model row: %s", out)
	}
	if !strings.Contains(out, "$   0.1234") {
		t.Errorf("usd format wrong: %s", out)
	}
}
