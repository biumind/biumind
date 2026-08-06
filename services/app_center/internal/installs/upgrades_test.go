package installs

import (
	"reflect"
	"testing"
)

func TestDiffPermissions_AddedRemovedUnchanged(t *testing.T) {
	current := []string{"hub.invoke", "wiki.write", "cron.register"}
	target := []string{"hub.invoke", "net.outbound:*.api", "cron.register"}
	d := DiffPermissions(current, target)

	if !reflect.DeepEqual(d.Added, []string{"net.outbound:*.api"}) {
		t.Errorf("Added = %v", d.Added)
	}
	if !reflect.DeepEqual(d.Removed, []string{"wiki.write"}) {
		t.Errorf("Removed = %v", d.Removed)
	}
	if !reflect.DeepEqual(d.Unchanged, []string{"cron.register", "hub.invoke"}) {
		t.Errorf("Unchanged = %v", d.Unchanged)
	}
	if !d.IsBreaking() {
		t.Error("IsBreaking should be true (Added non-empty)")
	}
}

func TestDiffPermissions_NoChange(t *testing.T) {
	d := DiffPermissions(
		[]string{"hub.invoke"},
		[]string{"hub.invoke"},
	)
	if d.IsBreaking() {
		t.Error("identical sets should not be breaking")
	}
	if len(d.Added) != 0 || len(d.Removed) != 0 {
		t.Errorf("expected zero diff: %+v", d)
	}
}

func TestDiffPermissions_NetOutboundParamSensitive(t *testing.T) {
	// Different domain params count as different perms — granting
	// access to *.a.com is NOT the same as *.b.com.
	d := DiffPermissions(
		[]string{"net.outbound:*.a.com"},
		[]string{"net.outbound:*.b.com"},
	)
	if !reflect.DeepEqual(d.Added, []string{"net.outbound:*.b.com"}) ||
		!reflect.DeepEqual(d.Removed, []string{"net.outbound:*.a.com"}) {
		t.Errorf("got %+v", d)
	}
}

func TestDiffPermissions_DropsEmptyStrings(t *testing.T) {
	d := DiffPermissions(
		[]string{"", "hub.invoke", ""},
		[]string{"hub.invoke"},
	)
	if d.IsBreaking() || len(d.Removed) != 0 {
		t.Errorf("empty strings should be filtered: %+v", d)
	}
}

// ─── version ordering ─────────────────────────────────────

func TestVersionGreater(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"0.2.0", "0.1.0", true},
		{"0.1.1", "0.1.0", true},
		{"1.0.0", "0.99.99", true},
		{"0.1.0", "0.2.0", false},
		{"0.1.0", "0.1.0", false},
		// Pre-release tags ignored — 0.2.0 > 0.2.0-rc1 (we lose Semver
		// §11 precedence for v2.0 simplicity; documented).
		{"0.2.0", "0.2.0-rc1", false},  // numeric prefix tied
		// Bad input → treated as 0
		{"abc", "0.0.1", false},
	}
	for _, c := range cases {
		got := versionGreater(c.a, c.b)
		if got != c.want {
			t.Errorf("versionGreater(%q,%q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
