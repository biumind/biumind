package triggers

import (
	"strings"
	"testing"
	"time"
)

func TestParse_Standard5Field(t *testing.T) {
	for _, expr := range []string{
		"5 * * * *",      // 5-min past every hour
		"0 8 * * *",      // 8am daily
		"0 0 1 * *",      // 1st of every month
		"*/15 * * * *",   // every 15 min
		"0 9-17 * * 1-5", // weekday 9-5 hourly
	} {
		if _, err := Parse(expr); err != nil {
			t.Errorf("Parse(%q): %v", expr, err)
		}
	}
}

func TestParse_RejectsEveryMinute(t *testing.T) {
	_, err := Parse("* * * * *")
	if err != ErrTooFrequent {
		t.Errorf("expected ErrTooFrequent, got %v", err)
	}
}

func TestParse_RejectsMalformed(t *testing.T) {
	for _, expr := range []string{
		"",
		"60 * * * *", // minute > 59
		"* * * 13 *", // month > 12
		"abc def",
	} {
		_, err := Parse(expr)
		if err == nil {
			t.Errorf("Parse(%q): expected error", expr)
		}
	}
}

func TestParse_RejectsSecondsField(t *testing.T) {
	// 6-field (with seconds) is what robfig defaults to but we
	// explicitly disable it. Confirm a 6-field expr fails.
	_, err := Parse("0 5 * * * *")
	if err == nil {
		t.Error("expected 6-field form to be rejected")
	}
}

func TestNextRun_Deterministic(t *testing.T) {
	// "5 * * * *" — next run at minute=5 of next hour.
	from := time.Date(2026, 5, 29, 10, 0, 0, 0, time.UTC)
	next, err := NextRun("5 * * * *", from)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 5, 29, 10, 5, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Errorf("next = %v, want %v", next, want)
	}
}

func TestNextRun_EveryMinuteRejected(t *testing.T) {
	from := time.Date(2026, 5, 29, 10, 0, 0, 0, time.UTC)
	_, err := NextRun("* * * * *", from)
	if err == nil || !strings.Contains(err.Error(), "1 minute") {
		t.Errorf("expected too-frequent error, got %v", err)
	}
}

func TestNextRun_DailyAt8(t *testing.T) {
	// from 9am — next 8am is tomorrow.
	from := time.Date(2026, 5, 29, 9, 0, 0, 0, time.UTC)
	next, _ := NextRun("0 8 * * *", from)
	want := time.Date(2026, 5, 30, 8, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Errorf("next = %v, want %v", next, want)
	}
}
