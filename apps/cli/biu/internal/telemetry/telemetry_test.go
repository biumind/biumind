package telemetry

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// withTempHome redirects $HOME so tests can inspect ~/.biu/* files
// without touching the real user.
func withTempHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	// Also clear env-driven overrides so tests are deterministic.
	t.Setenv("BIU_TELEMETRY_DISABLED", "")
	t.Setenv("BIU_TELEMETRY_ENABLED", "")
	t.Setenv("BIU_TELEMETRY_ENDPOINT", "")
	return dir
}

func TestDefaultIsDisabled(t *testing.T) {
	withTempHome(t)
	r, err := New("0.1.0", "abc")
	if err != nil {
		t.Fatal(err)
	}
	if !r.Disabled() {
		t.Errorf("default reporter should be disabled")
	}
}

func TestEnableThenRecordWritesJSONL(t *testing.T) {
	home := withTempHome(t)
	if _, err := Enable(""); err != nil {
		t.Fatal(err)
	}

	r, err := New("0.1.0", "abc1234")
	if err != nil {
		t.Fatal(err)
	}
	if r.Disabled() {
		t.Fatal("after Enable, reporter must be active")
	}

	r.Record(Event{
		Subcommand: "doctor",
		Outcome:    "ok",
		DurationMs: 12,
	})

	jsonlPath := filepath.Join(home, ".biu", "telemetry.jsonl")
	f, err := os.Open(jsonlPath)
	if err != nil {
		t.Fatalf("expected jsonl at %s: %v", jsonlPath, err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	if !sc.Scan() {
		t.Fatal("expected one line of telemetry")
	}
	var got Event
	if err := json.Unmarshal(sc.Bytes(), &got); err != nil {
		t.Fatalf("malformed: %v", err)
	}
	if got.Tool != "biu" || got.Version != "0.1.0" || got.Commit != "abc1234" {
		t.Errorf("metadata wrong: %+v", got)
	}
	if got.Subcommand != "doctor" || got.Outcome != "ok" || got.DurationMs != 12 {
		t.Errorf("payload wrong: %+v", got)
	}
	if got.Schema != "biu.telemetry.v1" {
		t.Errorf("schema field missing: %+v", got)
	}
	if got.InstallID == "" {
		t.Errorf("install_id should be populated; got %+v", got)
	}
	if got.SessionID == "" {
		t.Errorf("session_id should be populated; got %+v", got)
	}
}

func TestDisableNoOps(t *testing.T) {
	home := withTempHome(t)
	if _, err := Enable(""); err != nil {
		t.Fatal(err)
	}
	if _, err := Disable(); err != nil {
		t.Fatal(err)
	}
	r, _ := New("0.1.0", "abc")
	if !r.Disabled() {
		t.Fatal("Disable should produce a no-op reporter")
	}
	r.Record(Event{Subcommand: "doctor"}) // must not panic / write
	if _, err := os.Stat(filepath.Join(home, ".biu", "telemetry.jsonl")); err == nil {
		t.Errorf("disabled reporter should not write jsonl")
	}
}

func TestEnableRotatesInstallID(t *testing.T) {
	withTempHome(t)
	c1, _ := Enable("")
	if _, err := Disable(); err != nil {
		t.Fatal(err)
	}
	c2, _ := Enable("")
	if c1.InstallID == "" || c2.InstallID == "" {
		t.Fatalf("install_id should be populated; got %q / %q", c1.InstallID, c2.InstallID)
	}
	if c1.InstallID == c2.InstallID {
		t.Errorf("install_id should rotate on off→on; both = %q", c1.InstallID)
	}
}

func TestEnvVarDisabledOverride(t *testing.T) {
	withTempHome(t)
	if _, err := Enable(""); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BIU_TELEMETRY_DISABLED", "1")
	r, _ := New("0.1.0", "abc")
	if !r.Disabled() {
		t.Errorf("BIU_TELEMETRY_DISABLED=1 should override saved config")
	}
}

func TestEnvVarEnabledWithoutConfigFile(t *testing.T) {
	withTempHome(t)
	t.Setenv("BIU_TELEMETRY_ENABLED", "1")
	r, _ := New("0.1.0", "abc")
	if r.Disabled() {
		t.Errorf("BIU_TELEMETRY_ENABLED=1 should opt-in even without saved config")
	}
}

func TestRotateIfHugeMovesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	// Write a 100-byte file; rotate threshold = 50.
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 100)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := rotateIfHuge(path, 50); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("original should be moved away; got %v", err)
	}
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Errorf("rotated file missing: %v", err)
	}
}

func TestClassifyError(t *testing.T) {
	cases := []struct {
		err  error
		want ErrorClass
	}{
		{nil, ""},
		{errors.New("user-cancelled context canceled"), ErrUserCancel},
		{errors.New("config: missing api_key"), ErrConfig},
		{errors.New("permission denied for Bash"), ErrPermission},
		{errors.New("tool foo crashed"), ErrTool},
		{errors.New("provider anthropic 503"), ErrProvider},
		{errors.New("totally novel weird thing"), ErrUnknown},
	}
	for _, c := range cases {
		got := ClassifyError(c.err)
		if got != c.want {
			t.Errorf("ClassifyError(%v) = %q, want %q", c.err, got, c.want)
		}
	}
}

func TestRecordTimestampDefaultsToNow(t *testing.T) {
	withTempHome(t)
	if _, err := Enable(""); err != nil {
		t.Fatal(err)
	}
	r, _ := New("0.1.0", "")
	before := time.Now().UTC()
	r.Record(Event{Subcommand: "x"})
	jsonlPath, _ := EventsPath()
	body, _ := os.ReadFile(jsonlPath)
	var got Event
	for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		_ = json.Unmarshal([]byte(line), &got)
	}
	if got.TS.Before(before.Add(-time.Minute)) {
		t.Errorf("ts should be recent; got %v before %v", got.TS, before)
	}
}
