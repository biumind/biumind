package dbmigrate

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// validServiceName guards a SQL identifier interpolation in baseline()
// — every real-world service name must be admitted, every shape of
// hostile input must be rejected. Pin the contract so a future "let's
// allow dashes" / "let's go full-utf8" change is a deliberate edit.
func TestValidServiceName_AcceptsRealServices(t *testing.T) {
	for _, name := range []string{
		"identity", "runtime", "brain", "model_relay", "authz",
		"realtime", "channels", "sandbox", "app_center", "deploy",
	} {
		if !validServiceName.MatchString(name) {
			t.Errorf("real service name %q must match", name)
		}
	}
}

func TestValidServiceName_RejectsHostileInput(t *testing.T) {
	bad := []string{
		"",                          // empty
		"Identity",                  // uppercase
		"a-b",                       // dash
		"a.b",                       // dot
		"a;DROP TABLE users",        // sql injection classic
		`a" OR 1=1; --`,             // quote injection
		"a b",                       // space
		"123start",                  // leading digit
		"_underscore_first",         // leading underscore
		"way_too_long_service_name_that_exceeds_limit", // > 31 chars
	}
	for _, name := range bad {
		if validServiceName.MatchString(name) {
			t.Errorf("must reject %q", name)
		}
	}
}

// Run rejects bad service names with a clear error before touching
// the DB. Pool argument doesn't matter — the regex fires first.
func TestRun_RejectsBadServiceName(t *testing.T) {
	var pool *pgxpool.Pool // nil; reaching pool would panic, which is the assertion
	err := Run(context.Background(), pool, "Bad-Name", "/tmp/x", "x.y", 0)
	if err == nil {
		t.Fatal("expected error on invalid service name")
	}
	if got := err.Error(); got == "" || !contains(got, "invalid") {
		t.Errorf("error should mention invalid; got %q", got)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr ||
		(len(substr) > 0 && (indexOf(s, substr) >= 0)))
}

func indexOf(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
