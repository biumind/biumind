package skills

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Integration tests run against runtime.* + brain.events. Skip when
// DATABASE_URL is unset or the schema is missing (CI matrix splits
// unit / integration the same way brain/memory does).

func openDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping integration tests")
	}
	p, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(p.Close)

	for _, q := range []struct{ table, name string }{
		{"runtime.skills", "00002_skills.sql"},
		{"brain.events", "00001_wiki.sql"},
	} {
		var exists bool
		if err := p.QueryRow(context.Background(),
			`SELECT EXISTS (SELECT 1 FROM information_schema.tables
			    WHERE table_schema = split_part($1, '.', 1)
			      AND table_name = split_part($1, '.', 2))`,
			q.table,
		).Scan(&exists); err != nil {
			t.Fatalf("table check %s: %v", q.table, err)
		}
		if !exists {
			t.Skipf("%s missing; apply migrations/%s", q.table, q.name)
		}
	}
	return p
}

func freshOrg(t *testing.T) uuid.UUID {
	t.Helper()
	return uuid.New()
}

func newSkillID(t *testing.T) string {
	t.Helper()
	return "skill_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
}

func mustCreate(t *testing.T, r *Registry, in CreateInput) *Skill {
	t.Helper()
	if in.ID == "" {
		in.ID = newSkillID(t)
	}
	if in.OrgID == uuid.Nil {
		in.OrgID = freshOrg(t)
	}
	if in.Source == "" {
		in.Source = SourceUser
	}
	if in.Identifier == "" {
		in.Identifier = "test-" + uuid.NewString()[:8]
	}
	if in.Name == "" {
		in.Name = in.Identifier
	}
	s, err := r.Create(context.Background(), in)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return s
}

func TestCreate_RoundTrip(t *testing.T) {
	pool := openDB(t)
	r := New(pool)
	ctx := context.Background()
	org := freshOrg(t)

	s := mustCreate(t, r, CreateInput{
		OrgID:       org,
		Identifier:  "code-review",
		Name:        "Code Review",
		Description: "PR auto-review",
		Source:      SourceUser,
		Manifest:    Manifest{Version: "1.0.0", License: "MIT"},
		Content:     "# Review\nBe thorough.",
		Paths:       []string{"**/*.go"},
		Permissions: []string{"sandbox.exec"},
	})
	if s.ContentHash == "" {
		t.Error("content_hash should auto-populate")
	}
	if s.Status != StatusActive {
		t.Errorf("default status = %q, want active", s.Status)
	}

	got, err := r.Get(ctx, s.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Identifier != "code-review" || got.Name != "Code Review" {
		t.Errorf("readback mismatch: %+v", got)
	}
	if got.Manifest.License != "MIT" {
		t.Errorf("manifest readback: %+v", got.Manifest)
	}
	if len(got.Paths) != 1 || got.Paths[0] != "**/*.go" {
		t.Errorf("paths readback: %v", got.Paths)
	}
}

func TestCreate_RejectsDuplicateIdentifier(t *testing.T) {
	pool := openDB(t)
	r := New(pool)
	org := freshOrg(t)

	mustCreate(t, r, CreateInput{OrgID: org, Identifier: "dupe"})
	_, err := r.Create(context.Background(), CreateInput{
		ID: newSkillID(t), OrgID: org, Identifier: "dupe",
		Name: "Second", Source: SourceUser,
	})
	if !errors.Is(err, ErrNameTaken) {
		t.Errorf("want ErrNameTaken, got %v", err)
	}
}

func TestCreate_AllowsSameIdentifierAcrossOrgs(t *testing.T) {
	pool := openDB(t)
	r := New(pool)

	mustCreate(t, r, CreateInput{OrgID: freshOrg(t), Identifier: "shared"})
	// Different org → no collision.
	mustCreate(t, r, CreateInput{OrgID: freshOrg(t), Identifier: "shared"})
}

func TestCreate_RejectsBadInputs(t *testing.T) {
	pool := openDB(t)
	r := New(pool)
	ctx := context.Background()

	cases := []struct {
		name string
		in   CreateInput
	}{
		{"missing-id", CreateInput{OrgID: freshOrg(t), Identifier: "x", Name: "X", Source: SourceUser}},
		{"missing-org", CreateInput{ID: newSkillID(t), Identifier: "x", Name: "X", Source: SourceUser}},
		{"missing-identifier", CreateInput{ID: newSkillID(t), OrgID: freshOrg(t), Name: "X", Source: SourceUser}},
		{"missing-name", CreateInput{ID: newSkillID(t), OrgID: freshOrg(t), Identifier: "x", Source: SourceUser}},
		{"bad-source", CreateInput{ID: newSkillID(t), OrgID: freshOrg(t), Identifier: "x", Name: "X", Source: "garbage"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := r.Create(ctx, c.in)
			if err == nil {
				t.Error("expected error")
			}
		})
	}
}

func TestList_FiltersByOwnerAndStatus(t *testing.T) {
	pool := openDB(t)
	r := New(pool)
	ctx := context.Background()
	org := freshOrg(t)
	owner := uuid.New()

	mustCreate(t, r, CreateInput{OrgID: org, Identifier: "mine-a", OwnerID: &owner})
	mustCreate(t, r, CreateInput{OrgID: org, Identifier: "mine-b", OwnerID: &owner})
	// Org-shared (owner_id NULL).
	mustCreate(t, r, CreateInput{OrgID: org, Identifier: "shared-a", Source: SourceOrg})
	// Disabled — should drop out of status-filtered queries.
	disabled := mustCreate(t, r, CreateInput{OrgID: org, Identifier: "off", OwnerID: &owner})
	if _, err := r.SetStatus(ctx, disabled.ID, StatusDisabled, "test"); err != nil {
		t.Fatal(err)
	}

	all, err := r.List(ctx, ListInput{OrgID: org})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(all); got < 4 {
		t.Errorf("expected ≥4 rows, got %d", got)
	}

	active, err := r.List(ctx, ListInput{OrgID: org, Status: StatusActive})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range active {
		if s.Status != StatusActive {
			t.Errorf("active filter let %q through (status=%q)", s.Identifier, s.Status)
		}
	}

	mine, err := r.List(ctx, ListInput{OrgID: org, OwnerID: &owner})
	if err != nil {
		t.Fatal(err)
	}
	// Owner filter is "mine OR org-shared (owner_id NULL)" so
	// shared-a should appear too.
	names := map[string]bool{}
	for _, s := range mine {
		names[s.Identifier] = true
	}
	for _, want := range []string{"mine-a", "mine-b", "shared-a"} {
		if !names[want] {
			t.Errorf("owner filter missing %q; got %v", want, names)
		}
	}
}

func TestUpdate_PartialFieldMask(t *testing.T) {
	pool := openDB(t)
	r := New(pool)
	ctx := context.Background()
	org := freshOrg(t)

	s := mustCreate(t, r, CreateInput{
		OrgID: org, Identifier: "u1", Description: "v1",
		Content: "old body",
	})
	v1Hash := s.ContentHash

	got, err := r.Update(ctx, UpdateInput{
		ID: s.ID, SetDescription: true, Description: "v2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Description != "v2" {
		t.Errorf("desc not updated: %q", got.Description)
	}
	// Content was NOT in the field mask → hash should be unchanged.
	if got.ContentHash != v1Hash {
		t.Errorf("content_hash changed despite SetContent=false: %q vs %q",
			v1Hash, got.ContentHash)
	}

	got, err = r.Update(ctx, UpdateInput{
		ID: s.ID, SetContent: true, Content: "new body",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != "new body" {
		t.Errorf("content not updated: %q", got.Content)
	}
	if got.ContentHash == v1Hash {
		t.Errorf("content_hash should change with content")
	}
}

func TestUpdate_NoOpIsAllowed(t *testing.T) {
	pool := openDB(t)
	r := New(pool)
	ctx := context.Background()
	s := mustCreate(t, r, CreateInput{OrgID: freshOrg(t), Identifier: "noop"})

	got, err := r.Update(ctx, UpdateInput{ID: s.ID})
	if err != nil {
		t.Errorf("no-op update should succeed; got %v", err)
	}
	if got.ID != s.ID {
		t.Error("no-op should return the same row")
	}
}

func TestDelete_RemovesRowAndCascadesAgentSkills(t *testing.T) {
	pool := openDB(t)
	r := New(pool)
	ctx := context.Background()
	org := freshOrg(t)

	s := mustCreate(t, r, CreateInput{OrgID: org, Identifier: "del"})
	agent := uuid.New()
	if _, err := r.Toggle(ctx, agent, s.ID, true, false); err != nil {
		t.Fatal(err)
	}

	if err := r.Delete(ctx, s.ID); err != nil {
		t.Fatal(err)
	}

	if _, err := r.Get(ctx, s.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after delete: want ErrNotFound, got %v", err)
	}

	// agent_skills row should have CASCADE'd away — count = 0.
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM runtime.agent_skills WHERE skill_id = $1`,
		s.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("agent_skills not cascaded: %d rows remain", n)
	}
}

func TestDelete_NotFound(t *testing.T) {
	pool := openDB(t)
	r := New(pool)
	if err := r.Delete(context.Background(), "skill_nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestSetStatus_StateMachine(t *testing.T) {
	pool := openDB(t)
	r := New(pool)
	ctx := context.Background()
	s := mustCreate(t, r, CreateInput{
		OrgID: freshOrg(t), Identifier: "sm", Status: StatusStaged,
	})

	got, err := r.SetStatus(ctx, s.ID, StatusActive, "approved")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusActive {
		t.Errorf("status = %q, want active", got.Status)
	}

	if _, err := r.SetStatus(ctx, s.ID, "garbage", ""); !errors.Is(err, ErrInvalidStatus) {
		t.Errorf("invalid status: want ErrInvalidStatus, got %v", err)
	}
}

func TestToggle_UpsertsAgentSkill(t *testing.T) {
	pool := openDB(t)
	r := New(pool)
	ctx := context.Background()
	s := mustCreate(t, r, CreateInput{OrgID: freshOrg(t), Identifier: "tog"})
	agent := uuid.New()

	as, err := r.Toggle(ctx, agent, s.ID, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if !as.IsEnabled || as.Pinned {
		t.Errorf("upsert wrong: %+v", as)
	}

	// Update — same (agent, skill) → upsert path.
	as, err = r.Toggle(ctx, agent, s.ID, true, true)
	if err != nil {
		t.Fatal(err)
	}
	if !as.Pinned {
		t.Error("pinned not flipped")
	}

	if err := r.Detach(ctx, agent, s.ID); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = pool.QueryRow(ctx,
		`SELECT count(*) FROM runtime.agent_skills WHERE agent_id = $1 AND skill_id = $2`,
		agent, s.ID).Scan(&n)
	if n != 0 {
		t.Errorf("Detach left %d row(s)", n)
	}
}

func TestEvents_EmittedForEveryMutation(t *testing.T) {
	pool := openDB(t)
	r := New(pool)
	ctx := context.Background()
	s := mustCreate(t, r, CreateInput{OrgID: freshOrg(t), Identifier: "ev"})

	if _, err := r.Update(ctx, UpdateInput{
		ID: s.ID, SetDescription: true, Description: "x",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.SetStatus(ctx, s.ID, StatusDisabled, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Toggle(ctx, uuid.New(), s.ID, true, false); err != nil {
		t.Fatal(err)
	}
	if err := r.Delete(ctx, s.ID); err != nil {
		t.Fatal(err)
	}

	// Should see at least: created + updated + status_changed +
	// toggled + deleted = 5 events.
	rows, err := pool.Query(ctx,
		`SELECT event_type FROM brain.events
		  WHERE scope = $1 ORDER BY id`,
		"runtime:skill:"+s.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := []string{}
	for rows.Next() {
		var et string
		_ = rows.Scan(&et)
		got = append(got, et)
	}
	want := []string{
		"skill.created", "skill.updated", "skill.status_changed",
		"skill.toggled", "skill.deleted",
	}
	if len(got) != len(want) {
		t.Fatalf("event types = %v, want %v", got, want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("event[%d] = %q, want %q", i, got[i], w)
		}
	}
}

// ─── Activation audit ──────────────────────────────────────

// LogActivation appends a row and round-trips. Validates the
// CHECK(trigger IN (...)) constraint by also driving an invalid
// trigger and confirming we reject it client-side rather than
// surfacing a raw pg constraint error.
func TestLogActivation_RoundTrip(t *testing.T) {
	pool := openDB(t)
	r := New(pool)
	ctx := context.Background()

	session := uuid.New()
	skillID := "skill_log1"
	traceID := "rn_test"

	// Reject invalid trigger client-side — keeps the DB error opaque
	// and surfaces a typed error the caller can branch on.
	if _, err := r.LogActivation(ctx, Activation{
		SessionID: session, SkillID: skillID,
		Trigger: ActivationTrigger("bogus"),
	}); err == nil || !errors.Is(err, ErrInvalidStatus) {
		t.Fatalf("invalid trigger should yield ErrInvalidStatus; got %v", err)
	}

	// Reject empty skill id — append-only ledger MUST carry a real
	// pointer, otherwise downstream replay can't join back to skills.
	if _, err := r.LogActivation(ctx, Activation{
		SessionID: session, SkillID: "",
		Trigger: TriggerToolCall,
	}); err == nil {
		t.Fatal("empty skill_id should fail")
	}

	a, err := r.LogActivation(ctx, Activation{
		SessionID: session, SkillID: skillID,
		Trigger: TriggerToolCall, TraceID: traceID,
		TokensIn: 10, TokensOut: 20,
	})
	if err != nil {
		t.Fatalf("LogActivation: %v", err)
	}
	if a.ID == uuid.Nil {
		t.Error("returned id should be populated")
	}
	if a.Trigger != TriggerToolCall || a.TraceID != traceID {
		t.Errorf("round-trip mismatch: %+v", a)
	}
	if a.TokensIn != 10 || a.TokensOut != 20 {
		t.Errorf("token counts not persisted: %+v", a)
	}

	// List by session — pinning the order contract (oldest first).
	_, _ = r.LogActivation(ctx, Activation{
		SessionID: session, SkillID: skillID,
		Trigger: TriggerAutoAttach,
	})
	rows, err := r.ListActivationsBySession(ctx, session, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 activations; got %d", len(rows))
	}
	if rows[0].Trigger != TriggerToolCall || rows[1].Trigger != TriggerAutoAttach {
		t.Errorf("order should be insertion-order: %v / %v",
			rows[0].Trigger, rows[1].Trigger)
	}
}
