package skills

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// LoadForAgent classification matrix. Spans every Skills-Design §8
// tier we model in this loader:
//
//   - pinned                  → LoadedSkills.Pinned
//   - enabled + paths match   → LoadedSkills.AutoAttach
//   - enabled, no paths       → LoadedSkills.Available
//   - org-shared, no toggle   → LoadedSkills.Available (when IncludeOrgShared)
//   - disabled or wrong org   → excluded entirely
func TestLoadForAgent_FourTierClassification(t *testing.T) {
	pool := openDB(t)
	r := New(pool)
	ctx := context.Background()
	org := freshOrg(t)
	agent := uuid.New()

	// Pinned: explicit toggle with pinned=true.
	pinned := mustCreate(t, r, CreateInput{OrgID: org, Identifier: "pin"})
	if _, err := r.Toggle(ctx, agent, pinned.ID, true, true); err != nil {
		t.Fatal(err)
	}

	// AutoAttach: enabled + paths matches cwd.
	autoAttach := mustCreate(t, r, CreateInput{
		OrgID: org, Identifier: "auto",
		Paths: []string{"src/**/*.go"},
	})
	if _, err := r.Toggle(ctx, agent, autoAttach.ID, true, false); err != nil {
		t.Fatal(err)
	}

	// Available: enabled, no paths.
	avail := mustCreate(t, r, CreateInput{OrgID: org, Identifier: "avail"})
	if _, err := r.Toggle(ctx, agent, avail.ID, true, false); err != nil {
		t.Fatal(err)
	}

	// Disabled (status) — should be filtered out.
	off := mustCreate(t, r, CreateInput{OrgID: org, Identifier: "off"})
	if _, err := r.Toggle(ctx, agent, off.ID, true, false); err != nil {
		t.Fatal(err)
	}
	if _, err := r.SetStatus(ctx, off.ID, StatusDisabled, "test"); err != nil {
		t.Fatal(err)
	}

	// Org-shared without explicit toggle: only surfaces when
	// IncludeOrgShared=true.
	mustCreate(t, r, CreateInput{
		OrgID: org, Identifier: "org-vis", Source: SourceOrg,
	})

	// Different org — should never appear.
	mustCreate(t, r, CreateInput{OrgID: freshOrg(t), Identifier: "other-org"})

	loaded, err := r.LoadForAgent(ctx, LoadForAgentInput{
		OrgID: org, AgentID: agent,
		Cwd: "/repo/src/auth/login.go",
	})
	if err != nil {
		t.Fatal(err)
	}

	if got, want := identifiers(loaded.Pinned), []string{"pin"}; !sliceEqualUnordered(got, want) {
		t.Errorf("pinned = %v, want %v", got, want)
	}
	if got, want := identifiers(loaded.AutoAttach), []string{"auto"}; !sliceEqualUnordered(got, want) {
		t.Errorf("auto-attach = %v, want %v", got, want)
	}
	if got, want := identifiers(loaded.Available), []string{"avail"}; !sliceEqualUnordered(got, want) {
		t.Errorf("available (no org-shared) = %v, want %v", got, want)
	}
	for _, s := range loaded.Available {
		if s.Identifier == "off" {
			t.Error("disabled skill leaked into Available")
		}
		if s.Identifier == "other-org" {
			t.Error("cross-org skill leaked")
		}
	}

	// IncludeOrgShared=true → org-vis also surfaces (as Available).
	loaded2, err := r.LoadForAgent(ctx, LoadForAgentInput{
		OrgID: org, AgentID: agent,
		Cwd:              "/repo/src/auth/login.go",
		IncludeOrgShared: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	avSet := identifiers(loaded2.Available)
	if !contains(avSet, "avail") || !contains(avSet, "org-vis") {
		t.Errorf("with IncludeOrgShared, Available = %v should include avail+org-vis", avSet)
	}
	// Pin/auto from agent's explicit set should NOT also appear in
	// Available — dedupe via enabledIDs.
	if contains(avSet, "pin") || contains(avSet, "auto") {
		t.Errorf("dedupe failed: pin/auto leaked into Available: %v", avSet)
	}
}

func TestLoadForAgent_NoMatchesIsEmpty(t *testing.T) {
	pool := openDB(t)
	r := New(pool)
	loaded, err := r.LoadForAgent(context.Background(), LoadForAgentInput{
		OrgID: freshOrg(t), AgentID: uuid.New(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Pinned)+len(loaded.AutoAttach)+len(loaded.Available) != 0 {
		t.Errorf("empty agent should yield empty load; got %+v", loaded)
	}
}

// ─── pure-function tests (no DB) ─────────────────────────────

func TestPathMatches_LiteralAndGlob(t *testing.T) {
	cases := []struct {
		name     string
		patterns []string
		cwd      string
		want     bool
	}{
		{"literal-substring", []string{"apps/cli/biu"}, "/Users/x/repo/apps/cli/biu/cmd/main.go", true},
		{"literal-miss", []string{"apps/server"}, "/repo/apps/cli/biu", false},
		{"glob-globstar", []string{"src/**/*.go"}, "/repo/src/auth/login.go", true},
		{"glob-globstar-miss", []string{"src/**/*.go"}, "/repo/cmd/main.go", false},
		{"basename-glob", []string{"*.go"}, "/repo/foo/bar.go", true},
		{"empty-pattern", []string{""}, "/repo/x.go", false},
		{"empty-target", []string{"src/**"}, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := pathMatches(c.patterns, c.cwd, nil)
			if got != c.want {
				t.Errorf("pathMatches(%v, %q) = %v; want %v",
					c.patterns, c.cwd, got, c.want)
			}
		})
	}
}

// ─── tiny helpers ─────────────────────────────────────────────

func identifiers(skills []*Skill) []string {
	out := make([]string, 0, len(skills))
	for _, s := range skills {
		out = append(out, s.Identifier)
	}
	return out
}

func sliceEqualUnordered(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	m := make(map[string]int, len(a))
	for _, x := range a {
		m[x]++
	}
	for _, x := range b {
		m[x]--
	}
	for _, v := range m {
		if v != 0 {
			return false
		}
	}
	return true
}

func contains(haystack []string, needle string) bool {
	for _, x := range haystack {
		if x == needle {
			return true
		}
	}
	return false
}
