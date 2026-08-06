package agent

import (
	"strings"
	"testing"
	"time"
)

func TestNewIDsUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		th, rn := IDs()
		if !strings.HasPrefix(th, "th_") || !strings.HasPrefix(rn, "rn_") {
			t.Fatalf("prefix wrong: %s %s", th, rn)
		}
		if seen[th] || seen[rn] {
			t.Fatalf("collision: %s %s", th, rn)
		}
		seen[th] = true
		seen[rn] = true
	}
}

func TestBackoffMonotonicCapped(t *testing.T) {
	prev := time.Duration(0)
	for i := 0; i < 10; i++ {
		d := Backoff(i)
		if d < prev && d != 5*time.Second {
			t.Errorf("backoff non-monotonic: i=%d d=%v prev=%v", i, d, prev)
		}
		if d > 5*time.Second {
			t.Errorf("backoff over cap: %v", d)
		}
		prev = d
	}
}
