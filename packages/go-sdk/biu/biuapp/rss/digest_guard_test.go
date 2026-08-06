package rss

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// Regression: a stale cron trigger firing the legacy "digest" / "fetch"
// action against a PG-backed App used to dereference the nil in-memory
// store and SIGSEGV the whole process. The dispatcher must now return an
// error instead of panicking.
func TestPGModeLegacyActionFailsLoud(t *testing.T) {
	a := newPGApp(t) // skips if no DATABASE_URL
	for _, action := range []string{"digest", "fetch"} {
		t.Run(action, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("Invoke(%q) panicked instead of erroring: %v", action, r)
				}
			}()
			_, err := a.Invoke(context.Background(), action, json.RawMessage(`{}`))
			if err == nil {
				t.Fatalf("Invoke(%q) should error in PG mode, got nil", action)
			}
			if !strings.Contains(err.Error(), "not supported in PG-backed mode") {
				t.Fatalf("Invoke(%q) wrong error: %v", action, err)
			}
		})
	}
}
