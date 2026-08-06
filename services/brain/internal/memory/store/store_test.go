package store

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Integration tests run against the brain.* schema in a Postgres reachable
// via DATABASE_URL. They skip when unset (CI matrix splits unit / integration).
// They also skip if `brain.memories` is missing — emit a hint so it's clear
// the migration needs to be applied.

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

	// Sanity-check the migration is applied — surface a clear message
	// instead of cryptic Postgres errors below.
	var exists bool
	if err := p.QueryRow(context.Background(),
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables
		   WHERE table_schema = 'brain' AND table_name = 'memories')`,
	).Scan(&exists); err != nil {
		t.Fatalf("table check: %v", err)
	}
	if !exists {
		t.Skip("brain.memories not present; apply migrations/00004_memory.sql first")
	}
	return p
}

func seedProject(t *testing.T, p *pgxpool.Pool) (uuid.UUID, uuid.UUID) {
	t.Helper()
	owner := uuid.New()
	var id uuid.UUID
	err := p.QueryRow(context.Background(),
		`INSERT INTO brain.projects (owner_id, name) VALUES ($1, $2) RETURNING id`,
		owner, "memory-test-"+uuid.NewString(),
	).Scan(&id)
	if err != nil {
		t.Fatalf("seed project: %v", err)
	}
	t.Cleanup(func() {
		_, _ = p.Exec(context.Background(),
			`DELETE FROM brain.projects WHERE id = $1`, id)
	})
	return id, owner
}

func TestCreateAndList(t *testing.T) {
	p := openDB(t)
	s := New(p)
	pid, uid := seedProject(t, p)
	ctx := context.Background()

	for _, c := range []string{"prefers concise replies", "uses Go", "writes Vim"} {
		if _, err := s.Create(ctx, StoreInput{
			ProjectID: pid, OwnerID: uid, Kind: KindPreference, Content: c,
		}); err != nil {
			t.Fatalf("create %q: %v", c, err)
		}
	}
	ms, err := s.List(ctx, ListInput{ProjectID: pid, OwnerID: uid, Kind: KindPreference})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(ms) != 3 {
		t.Errorf("want 3 memories, got %d", len(ms))
	}
}

func TestRecall_RanksByContent(t *testing.T) {
	p := openDB(t)
	s := New(p)
	pid, uid := seedProject(t, p)
	ctx := context.Background()

	wants := []string{
		"prefers Spanish translations",
		"writes Go services",
		"likes concise emails",
		"uses Vim with vimwiki",
	}
	for _, c := range wants {
		if _, err := s.Create(ctx, StoreInput{
			ProjectID: pid, OwnerID: uid, Kind: KindRecall, Content: c,
		}); err != nil {
			t.Fatalf("create %q: %v", c, err)
		}
	}
	ms, err := s.Recall(ctx, RecallInput{
		ProjectID: pid, OwnerID: uid, Query: "vim", Limit: 5,
	})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(ms) != 1 {
		t.Fatalf("expected 1 result for 'vim', got %d", len(ms))
	}
	if !strings.Contains(strings.ToLower(ms[0].Content), "vim") {
		t.Errorf("recall returned non-matching content: %q", ms[0].Content)
	}
	if ms[0].Score <= 0 {
		t.Errorf("expected positive score, got %v", ms[0].Score)
	}
}

func TestRecall_TouchesLastAccessed(t *testing.T) {
	p := openDB(t)
	s := New(p)
	pid, uid := seedProject(t, p)
	ctx := context.Background()

	m, err := s.Create(ctx, StoreInput{
		ProjectID: pid, OwnerID: uid, Kind: KindRecall,
		Content: "deploy via biu push",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	beforeTouch := m.LastAccessedAt

	if _, err := s.Recall(ctx, RecallInput{
		ProjectID: pid, OwnerID: uid, Query: "deploy",
	}); err != nil {
		t.Fatalf("recall: %v", err)
	}

	got, err := s.Get(ctx, uid, m.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !got.LastAccessedAt.After(beforeTouch) {
		t.Errorf("last_accessed_at not updated by recall: before=%v after=%v",
			beforeTouch, got.LastAccessedAt)
	}
}

func TestDelete_OwnerScoped(t *testing.T) {
	p := openDB(t)
	s := New(p)
	pid, uid := seedProject(t, p)
	stranger := uuid.New()
	ctx := context.Background()

	m, err := s.Create(ctx, StoreInput{
		ProjectID: pid, OwnerID: uid, Kind: KindSkill,
		Content: "use go test -race",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Stranger cannot delete.
	if err := s.Delete(ctx, stranger, m.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("stranger delete: want ErrNotFound, got %v", err)
	}
	// Owner can.
	if err := s.Delete(ctx, uid, m.ID); err != nil {
		t.Errorf("owner delete: %v", err)
	}
	// Idempotent: second delete returns ErrNotFound.
	if err := s.Delete(ctx, uid, m.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("repeat delete: want ErrNotFound, got %v", err)
	}
}

func TestCreate_RejectsInvalidKind(t *testing.T) {
	p := openDB(t)
	s := New(p)
	pid, uid := seedProject(t, p)
	ctx := context.Background()

	_, err := s.Create(ctx, StoreInput{
		ProjectID: pid, OwnerID: uid, Kind: "garbage", Content: "x",
	})
	if err == nil {
		t.Error("expected error for invalid kind")
	}
}

func TestCreate_RejectsEmptyContent(t *testing.T) {
	p := openDB(t)
	s := New(p)
	pid, uid := seedProject(t, p)
	ctx := context.Background()

	if _, err := s.Create(ctx, StoreInput{
		ProjectID: pid, OwnerID: uid, Kind: KindRecall, Content: "   ",
	}); err == nil {
		t.Error("expected error for empty content")
	}
}

// TestStoreAcceptsHabit — happy-path for the new canonical kind.
func TestStoreAcceptsHabit(t *testing.T) {
	p := openDB(t)
	s := New(p)
	pid, uid := seedProject(t, p)
	ctx := context.Background()

	m, err := s.Create(ctx, StoreInput{
		ProjectID: pid, OwnerID: uid, Kind: KindHabit,
		Content: "user always uses Conventional Commits",
	})
	if err != nil {
		t.Fatalf("create habit: %v", err)
	}
	if m.Kind != KindHabit {
		t.Errorf("kind = %q, want %q", m.Kind, KindHabit)
	}

	// Filtering by kind=habit returns the row.
	ms, err := s.List(ctx, ListInput{ProjectID: pid, OwnerID: uid, Kind: KindHabit})
	if err != nil {
		t.Fatalf("list habit: %v", err)
	}
	if len(ms) != 1 {
		t.Fatalf("list habit count = %d, want 1", len(ms))
	}
}

// TestAliasSkillToHabitFor90Days — backwards-compat: callers passing
// the deprecated "skill" kind get their row stored as "habit", and
// can read it back via either filter. Remove this test (and the alias
// path) on or after 2026-08-25.
func TestAliasSkillToHabitFor90Days(t *testing.T) {
	p := openDB(t)
	s := New(p)
	pid, uid := seedProject(t, p)
	ctx := context.Background()

	// Write with deprecated alias.
	m, err := s.Create(ctx, StoreInput{
		ProjectID: pid, OwnerID: uid, Kind: KindSkill,
		Content: "user prefers Vim keybindings",
	})
	if err != nil {
		t.Fatalf("create with deprecated kind: %v", err)
	}
	if m.Kind != KindHabit {
		t.Errorf("persisted kind = %q, want %q (alias should rewrite)",
			m.Kind, KindHabit)
	}

	// Filter by deprecated input → still finds the row (via NormalizeKind).
	msSkill, err := s.List(ctx, ListInput{
		ProjectID: pid, OwnerID: uid, Kind: KindSkill,
	})
	if err != nil {
		t.Fatalf("list with deprecated kind: %v", err)
	}
	if len(msSkill) != 1 {
		t.Errorf("list with kind=skill count = %d, want 1", len(msSkill))
	}

	// Recall with deprecated kind also works.
	hits, err := s.Recall(ctx, RecallInput{
		ProjectID: pid, OwnerID: uid, Query: "Vim", Kind: KindSkill,
	})
	if err != nil {
		t.Fatalf("recall with deprecated kind: %v", err)
	}
	if len(hits) != 1 {
		t.Errorf("recall with kind=skill count = %d, want 1", len(hits))
	}
}

// TestNormalizeKind_AliasFlag — pure function check, no DB.
func TestNormalizeKind_AliasFlag(t *testing.T) {
	cases := []struct {
		in           string
		wantOut      string
		wantDeprecat bool
	}{
		{KindRecall, KindRecall, false},
		{KindPreference, KindPreference, false},
		{KindHabit, KindHabit, false},
		{KindSkill, KindHabit, true},
		{"", "", false},
	}
	for _, c := range cases {
		out, dep := NormalizeKind(c.in)
		if out != c.wantOut || dep != c.wantDeprecat {
			t.Errorf("NormalizeKind(%q) = (%q, %v); want (%q, %v)",
				c.in, out, dep, c.wantOut, c.wantDeprecat)
		}
	}
}
