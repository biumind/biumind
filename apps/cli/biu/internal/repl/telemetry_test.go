// Tests for /telemetry slash. Each test points HOME at a t.TempDir
// so config + events file land in an isolated location.

package repl

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/internal/telemetry"
)

// freshTelemetryHome isolates the test from the real ~/.biu —
// telemetry.LoadConfig reads from $HOME, so pointing it at a temp
// dir gives every test a clean slate. Also clears the env vars
// that could otherwise leak in from CI.
func freshTelemetryHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("BIU_TELEMETRY_DISABLED", "")
	t.Setenv("BIU_TELEMETRY_ENABLED", "")
	t.Setenv("BIU_TELEMETRY_ENDPOINT", "")
	return home
}

func writeEvents(t *testing.T, home string, evs []telemetry.Event) {
	t.Helper()
	dir := filepath.Join(home, ".biu")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(dir, "telemetry.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, ev := range evs {
		if err := enc.Encode(ev); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTelemetryStatusOff(t *testing.T) {
	freshTelemetryHome(t)
	got := renderTelemetryStatus()
	for _, must := range []string{"telemetry: off", "config:", "endpoint:", "(none"} {
		if !strings.Contains(got, must) {
			t.Errorf("status missing %q;\nfull:\n%s", must, got)
		}
	}
}

// Status with the events file present surfaces the count + size.
func TestTelemetryStatusReportsEventsCount(t *testing.T) {
	home := freshTelemetryHome(t)
	now := time.Date(2026, 5, 25, 10, 0, 0, 0, time.UTC)
	writeEvents(t, home, []telemetry.Event{
		{Schema: "v1", TS: now, Subcommand: "doctor", Outcome: "ok"},
		{Schema: "v1", TS: now.Add(time.Minute), Subcommand: "auth", Outcome: "error"},
	})
	got := renderTelemetryStatus()
	if !strings.Contains(got, "2 events") {
		t.Errorf("status should report 2 events; got %q", got)
	}
	if !strings.Contains(got, "telemetry.jsonl") {
		t.Errorf("status should mention the file path; got %q", got)
	}
}

// /telemetry tail prints the last N events. Default is 10.
func TestTelemetryTailPrintsRecentEvents(t *testing.T) {
	home := freshTelemetryHome(t)
	now := time.Date(2026, 5, 25, 10, 0, 0, 0, time.UTC)
	evs := []telemetry.Event{}
	for i := 0; i < 15; i++ {
		evs = append(evs, telemetry.Event{
			Schema: "v1", TS: now.Add(time.Duration(i) * time.Minute),
			Subcommand: "run", Outcome: "ok", DurationMs: int64(100 + i),
		})
	}
	writeEvents(t, home, evs)

	got := renderTelemetryTail(5)
	if !strings.Contains(got, "last 5 events") {
		t.Errorf("expected count header; got %q", got)
	}
	// Earliest in the tail should be event #10 (i=10) — line 11
	// (1-indexed) of 15. We verify by checking that durations
	// 110ms..114ms appear and 100ms..109ms don't.
	for i := 10; i < 15; i++ {
		if !strings.Contains(got, formatThousandsDuration(100+i)) {
			t.Errorf("tail missing event with duration %d; got %q", 100+i, got)
		}
	}
	if strings.Contains(got, "108ms") {
		t.Errorf("tail should not include older events; got %q", got)
	}
}

// formatThousandsDuration is local to the tests — formatThousands is
// used in /cost output but we want to assert on the duration
// substring without coupling to its renderer.
func formatThousandsDuration(ms int) string {
	return strings.TrimSpace(stringInt(ms)) + "ms"
}

// stringInt avoids strconv just to keep test imports tight.
func stringInt(n int) string {
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if neg {
		digits = append([]byte{'-'}, digits...)
	}
	return string(digits)
}

// /telemetry tail with no events file says so politely.
func TestTelemetryTailNoFile(t *testing.T) {
	freshTelemetryHome(t)
	got := renderTelemetryTail(10)
	if !strings.Contains(got, "no events yet") {
		t.Errorf("missing-file message wrong; got %q", got)
	}
}

// /telemetry tail with empty file (ENOENT after stat? actually
// truncate). We write an empty file.
func TestTelemetryTailEmptyFile(t *testing.T) {
	home := freshTelemetryHome(t)
	if err := os.MkdirAll(filepath.Join(home, ".biu"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".biu", "telemetry.jsonl"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	got := renderTelemetryTail(10)
	if !strings.Contains(got, "events file is empty") {
		t.Errorf("empty-file path missing; got %q", got)
	}
}

// /telemetry tail with N > line count should clamp instead of
// erroring.
func TestTelemetryTailClampsNToAvailable(t *testing.T) {
	home := freshTelemetryHome(t)
	writeEvents(t, home, []telemetry.Event{
		{Schema: "v1", TS: time.Now(), Subcommand: "x", Outcome: "ok"},
	})
	got := renderTelemetryTail(99)
	if !strings.Contains(got, "last 1 events") {
		t.Errorf("should clamp to 1; got %q", got)
	}
}

// /telemetry export copies the events file verbatim.
func TestTelemetryExportCopiesFile(t *testing.T) {
	home := freshTelemetryHome(t)
	now := time.Date(2026, 5, 25, 10, 0, 0, 0, time.UTC)
	writeEvents(t, home, []telemetry.Event{
		{Schema: "v1", TS: now, Subcommand: "x", Outcome: "ok"},
		{Schema: "v1", TS: now, Subcommand: "y", Outcome: "ok"},
	})
	dest := filepath.Join(t.TempDir(), "out.jsonl")
	got := renderTelemetryExport(dest)
	if !strings.Contains(got, "exported 2 events") {
		t.Errorf("status line wrong; got %q", got)
	}
	out, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"subcommand":"x"`) ||
		!strings.Contains(string(out), `"subcommand":"y"`) {
		t.Errorf("export content lost: %q", out)
	}
}

func TestTelemetryExportNoFile(t *testing.T) {
	freshTelemetryHome(t)
	dest := filepath.Join(t.TempDir(), "out.jsonl")
	got := renderTelemetryExport(dest)
	if !strings.Contains(got, "no events to export") {
		t.Errorf("missing-file path wrong; got %q", got)
	}
}

// Relative export path resolves against cwd.
func TestTelemetryExportRelativePath(t *testing.T) {
	home := freshTelemetryHome(t)
	cwd := chdirTmp(t)
	writeEvents(t, home, []telemetry.Event{
		{Schema: "v1", TS: time.Now(), Subcommand: "x", Outcome: "ok"},
	})
	got := renderTelemetryExport("rel-out.jsonl")
	if !strings.Contains(got, filepath.Join(cwd, "rel-out.jsonl")) {
		t.Errorf("relative path should resolve under cwd; got %q", got)
	}
	if _, err := os.Stat(filepath.Join(cwd, "rel-out.jsonl")); err != nil {
		t.Errorf("export file missing under cwd: %v", err)
	}
}

// /telemetry handler dispatch covers usage / unknown subcommands /
// shape of bare command.
func TestHandleTelemetryDispatch(t *testing.T) {
	freshTelemetryHome(t)
	m := model{}
	if got := m.handleTelemetry([]string{"/telemetry"}); !strings.Contains(got, "telemetry:") {
		t.Errorf("bare /telemetry should run status; got %q", got)
	}
	if got := m.handleTelemetry([]string{"/telemetry", "wat"}); !strings.HasPrefix(got, "/telemetry: usage:") {
		t.Errorf("unknown sub should print usage; got %q", got)
	}
	if got := m.handleTelemetry([]string{"/telemetry", "tail", "abc"}); !strings.Contains(got, "must be a positive integer") {
		t.Errorf("non-numeric tail should reject; got %q", got)
	}
	if got := m.handleTelemetry([]string{"/telemetry", "export"}); !strings.Contains(got, "usage:") {
		t.Errorf("missing path should print usage; got %q", got)
	}
	if got := m.handleTelemetry([]string{"/telemetry", "enable"}); !strings.Contains(got, "usage:") {
		t.Errorf("missing endpoint should print usage; got %q", got)
	}
}

// redactID rounds-trips the install ID safely so the full machine
// identifier never reaches the system note.
func TestRedactID(t *testing.T) {
	cases := map[string]string{
		"":                  "(none)",
		"short":             "short",
		"abcdefgh":          "abcdefgh",
		"abcdefghi":         "abcd…fghi",
		"01234567890abcdef": "0123…cdef",
	}
	for in, want := range cases {
		if got := redactID(in); got != want {
			t.Errorf("redactID(%q) = %q, want %q", in, got, want)
		}
	}
}

// formatBytes covers the unit transitions.
func TestFormatBytes(t *testing.T) {
	cases := []struct {
		n    int64
		want string
	}{
		{0, "0 B"},
		{500, "500 B"},
		{1024, "1.00 KB"},
		{2048, "2.00 KB"},
		{1024 * 1024, "1.00 MB"},
		{1024 * 1024 * 1024, "1.00 GB"},
	}
	for _, c := range cases {
		if got := formatBytes(c.n); got != c.want {
			t.Errorf("formatBytes(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}

func TestTruncateLineUnicodeSafe(t *testing.T) {
	// Multi-byte chars (中文) must not be split mid-rune.
	in := "abc中文defg"
	got := truncateLine(in, 5)
	if !strings.HasSuffix(got, "…") {
		t.Errorf("expected ellipsis suffix; got %q", got)
	}
	// Should be 5 runes + ellipsis = 6.
	r := []rune(got)
	if len(r) != 6 {
		t.Errorf("truncated rune count: got %d, want 6 (5 + ellipsis)", len(r))
	}
}
