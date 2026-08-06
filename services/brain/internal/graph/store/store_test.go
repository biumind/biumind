package store

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// These tests run against a real Postgres if DATABASE_URL is exported. They
// are skipped otherwise so `go test ./...` stays usable in environments
// without a database (CI matrix splits into unit + integration jobs).

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
	return p
}

// seedProject creates a throwaway project row and returns its id. Tests use
// it as a tenant so they can run in parallel without collisions.
func seedProject(t *testing.T, p *pgxpool.Pool) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	owner := uuid.New()
	var id uuid.UUID
	err := p.QueryRow(ctx,
		`INSERT INTO brain.projects (owner_id, name) VALUES ($1, $2) RETURNING id`,
		owner, "graph-test-"+uuid.NewString(),
	).Scan(&id)
	if err != nil {
		t.Fatalf("seed project: %v", err)
	}
	t.Cleanup(func() {
		_, _ = p.Exec(context.Background(), `DELETE FROM brain.projects WHERE id = $1`, id)
	})
	return id
}

func TestUpsertNodeIdempotent(t *testing.T) {
	p := openDB(t)
	s := New(p)
	pid := seedProject(t, p)
	ctx := context.Background()

	n1, err := s.UpsertNode(ctx, UpsertNodeInput{
		ProjectID: pid, Kind: "tag", Name: "rust", Aliases: []string{"rs"},
		Summary: "systems lang", Weight: 0.7,
	})
	if err != nil {
		t.Fatalf("upsert 1: %v", err)
	}
	n2, err := s.UpsertNode(ctx, UpsertNodeInput{
		ProjectID: pid, Kind: "tag", Name: "rust", Aliases: []string{"rustlang"},
		Weight: 1.0,
	})
	if err != nil {
		t.Fatalf("upsert 2: %v", err)
	}
	if n1.ID != n2.ID {
		t.Fatalf("expected idempotent id, got %s vs %s", n1.ID, n2.ID)
	}
	if n2.Weight < 1.0 {
		t.Errorf("expected merged weight ≥ 1.0, got %v", n2.Weight)
	}
	gotAliases := map[string]bool{}
	for _, a := range n2.Aliases {
		gotAliases[a] = true
	}
	for _, want := range []string{"rs", "rustlang"} {
		if !gotAliases[want] {
			t.Errorf("missing alias %q in %v", want, n2.Aliases)
		}
	}
	if n2.Summary != "systems lang" {
		t.Errorf("summary should be preserved when second call has empty summary, got %q", n2.Summary)
	}
}

func TestUpsertEdgeAndBFS(t *testing.T) {
	p := openDB(t)
	s := New(p)
	pid := seedProject(t, p)
	ctx := context.Background()

	// Build: A — links_to → B — links_to → C
	mk := func(name string) uuid.UUID {
		n, err := s.UpsertNode(ctx, UpsertNodeInput{ProjectID: pid, Kind: "concept", Name: name})
		if err != nil {
			t.Fatalf("upsert %s: %v", name, err)
		}
		return n.ID
	}
	a, b, c := mk("A"), mk("B"), mk("C")

	if _, err := s.UpsertEdge(ctx, UpsertEdgeInput{
		ProjectID: pid, SrcID: a, DstID: b, Relation: "links_to",
	}); err != nil {
		t.Fatalf("edge a-b: %v", err)
	}
	if _, err := s.UpsertEdge(ctx, UpsertEdgeInput{
		ProjectID: pid, SrcID: b, DstID: c, Relation: "links_to",
	}); err != nil {
		t.Fatalf("edge b-c: %v", err)
	}

	// Depth=1 from A → just B.
	got, err := s.NeighborsBFS(ctx, pid, a, 1, []string{}, 50)
	if err != nil {
		t.Fatalf("bfs depth 1: %v", err)
	}
	if len(got) != 1 || got[0].Node.ID != b {
		t.Fatalf("depth 1 want [B], got %+v", got)
	}

	// Depth=2 from A → B and C.
	got, err = s.NeighborsBFS(ctx, pid, a, 2, []string{}, 50)
	if err != nil {
		t.Fatalf("bfs depth 2: %v", err)
	}
	ids := map[uuid.UUID]bool{}
	for _, n := range got {
		ids[n.Node.ID] = true
	}
	if !ids[b] || !ids[c] {
		t.Fatalf("depth 2 want B+C, got %v", ids)
	}

	// Relation filter that doesn't match → empty.
	got, err = s.NeighborsBFS(ctx, pid, a, 2, []string{"references"}, 50)
	if err != nil {
		t.Fatalf("bfs filtered: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("relation filter should drop everything, got %+v", got)
	}
}

func TestUpsertEdgeIdempotent(t *testing.T) {
	p := openDB(t)
	s := New(p)
	pid := seedProject(t, p)
	ctx := context.Background()

	n1, _ := s.UpsertNode(ctx, UpsertNodeInput{ProjectID: pid, Kind: "tag", Name: "x"})
	n2, _ := s.UpsertNode(ctx, UpsertNodeInput{ProjectID: pid, Kind: "tag", Name: "y"})

	e1, err := s.UpsertEdge(ctx, UpsertEdgeInput{
		ProjectID: pid, SrcID: n1.ID, DstID: n2.ID, Relation: "mentions", Weight: 0.5,
	})
	if err != nil {
		t.Fatalf("edge 1: %v", err)
	}
	e2, err := s.UpsertEdge(ctx, UpsertEdgeInput{
		ProjectID: pid, SrcID: n1.ID, DstID: n2.ID, Relation: "mentions", Weight: 0.9,
	})
	if err != nil {
		t.Fatalf("edge 2: %v", err)
	}
	if e1.ID != e2.ID {
		t.Fatalf("expected idempotent edge id")
	}
	if e2.Weight < 0.9 {
		t.Errorf("expected merged weight ≥ 0.9, got %v", e2.Weight)
	}
}
