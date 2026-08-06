package reviews

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestDedupKeyForPair_DeterministicRegardlessOfOrder(t *testing.T) {
	a := uuid.New()
	b := uuid.New()
	k1 := DedupKeyForPair(a, b)
	k2 := DedupKeyForPair(b, a)
	if k1 != k2 {
		t.Errorf("dedupe key must be order-independent: %s vs %s", k1, k2)
	}
	if !strings.HasPrefix(k1, "dedup:") {
		t.Errorf("dedupe key missing prefix: %s", k1)
	}
}

func TestDedupKeyForPair_EmbedsBothUUIDs(t *testing.T) {
	a := uuid.New()
	b := uuid.New()
	k := DedupKeyForPair(a, b)
	if !strings.Contains(k, a.String()) || !strings.Contains(k, b.String()) {
		t.Errorf("key should contain both UUIDs: %s", k)
	}
}

func TestDedupOptions_Defaults(t *testing.T) {
	got := (DedupOptions{}).withDefaults()
	if got.MaxDistance <= 0 || got.MaxDistance >= 1 {
		t.Errorf("MaxDistance default unsensible: %v", got.MaxDistance)
	}
	if got.PerPageNeighbours <= 0 || got.PerPageNeighbours > 100 {
		t.Errorf("PerPageNeighbours default unsensible: %v", got.PerPageNeighbours)
	}
	if got.MaxPairsPerProject <= 0 {
		t.Errorf("MaxPairsPerProject must be positive: %v", got.MaxPairsPerProject)
	}
}

func TestDedupOptions_PreservesUserOverrides(t *testing.T) {
	o := DedupOptions{
		MaxDistance: 0.05, PerPageNeighbours: 10, MaxPairsPerProject: 100,
	}.withDefaults()
	if o.MaxDistance != 0.05 || o.PerPageNeighbours != 10 || o.MaxPairsPerProject != 100 {
		t.Errorf("defaults should not override user values: %+v", o)
	}
}

func TestDisplayTitle(t *testing.T) {
	if got := displayTitle(""); got != "(未命名页面)" {
		t.Errorf("empty fallback: %q", got)
	}
	long := strings.Repeat("x", 80)
	got := displayTitle(long)
	if len(got) > 64 || !strings.HasSuffix(got, "…") {
		t.Errorf("long title not truncated: %q (len=%d)", got, len(got))
	}
	if got := displayTitle("short"); got != "short" {
		t.Errorf("short title should pass through: %q", got)
	}
}

func TestValidKindAndStatus(t *testing.T) {
	for _, k := range []string{"dedup", "lint", "sweep", "merge", "suggestion"} {
		if !ValidKind(k) {
			t.Errorf("kind %q should be valid", k)
		}
	}
	if ValidKind("bogus") {
		t.Error("bogus kind should be invalid")
	}
	for _, s := range []string{"open", "resolved", "dismissed"} {
		if !ValidStatus(s) {
			t.Errorf("status %q should be valid", s)
		}
	}
	if ValidStatus("") || ValidStatus("xxx") {
		t.Error("invalid statuses pass")
	}
	if !IsTerminal("resolved") || !IsTerminal("dismissed") || IsTerminal("open") {
		t.Error("IsTerminal wrong")
	}
}
