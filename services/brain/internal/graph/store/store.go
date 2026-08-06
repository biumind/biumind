// Package store is the data-access layer for Brain.Graph.
//
// Schema lives in services/brain/migrations/00003_graph.sql:
//
//	graph_nodes        — entities/concepts (project_id, kind, name uniqueness)
//	graph_edges        — typed directed relationships
//	graph_block_nodes  — junction: which blocks contain which nodes
//
// All upserts are idempotent on (project_id, kind, name) for nodes and
// (project_id, src_id, dst_id, relation) for edges. Re-running the
// extractor against the same content is therefore safe.
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound = errors.New("graph: not found")
)

type Node struct {
	ID        uuid.UUID
	ProjectID uuid.UUID
	Kind      string
	Name      string
	Aliases   []string
	Summary   string
	Path      *string
	Weight    float32
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Edge struct {
	ID              uuid.UUID
	ProjectID       uuid.UUID
	SrcID           uuid.UUID
	DstID           uuid.UUID
	Relation        string
	Weight          float32
	EvidenceBlockID *uuid.UUID
	CreatedAt       time.Time
}

type Store struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// ─── Nodes ─────────────────────────────────────────────────

type UpsertNodeInput struct {
	ProjectID uuid.UUID
	Kind      string
	Name      string
	Aliases   []string
	Summary   string
	Path      *string
	Weight    float32
}

// UpsertNode is idempotent on (project_id, kind, name). On conflict it
// merges aliases (set union) and keeps the higher weight + most recent
// summary if non-empty.
func (s *Store) UpsertNode(ctx context.Context, in UpsertNodeInput) (*Node, error) {
	if in.Weight == 0 {
		in.Weight = 1.0
	}
	if in.Aliases == nil {
		in.Aliases = []string{}
	}
	n := &Node{}
	err := s.pool.QueryRow(ctx, `
		INSERT INTO brain.graph_nodes
			(project_id, kind, name, aliases, summary, path, weight)
		VALUES ($1,$2,$3,$4,$5,$6::ltree,$7)
		ON CONFLICT (project_id, kind, name) DO UPDATE SET
			aliases    = (SELECT array_agg(DISTINCT a) FROM unnest(graph_nodes.aliases || EXCLUDED.aliases) a),
			summary    = CASE WHEN length(EXCLUDED.summary) > 0 THEN EXCLUDED.summary ELSE graph_nodes.summary END,
			path       = COALESCE(EXCLUDED.path, graph_nodes.path),
			weight     = GREATEST(graph_nodes.weight, EXCLUDED.weight),
			updated_at = now()
		RETURNING id, project_id, kind, name, aliases, summary, path::text, weight, created_at, updated_at
	`, in.ProjectID, in.Kind, in.Name, in.Aliases, in.Summary, nullableString(in.Path), in.Weight).Scan(
		&n.ID, &n.ProjectID, &n.Kind, &n.Name, &n.Aliases, &n.Summary, &n.Path, &n.Weight,
		&n.CreatedAt, &n.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("upsert node: %w", err)
	}
	return n, nil
}

func (s *Store) GetNode(ctx context.Context, id uuid.UUID) (*Node, error) {
	n := &Node{}
	err := s.pool.QueryRow(ctx, `
		SELECT id, project_id, kind, name, aliases, summary, path::text, weight, created_at, updated_at
		FROM brain.graph_nodes WHERE id = $1
	`, id).Scan(
		&n.ID, &n.ProjectID, &n.Kind, &n.Name, &n.Aliases, &n.Summary, &n.Path, &n.Weight,
		&n.CreatedAt, &n.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get node: %w", err)
	}
	return n, nil
}

type ListNodesInput struct {
	ProjectID uuid.UUID
	Kind      string // optional
	Search    string // optional, case-insensitive name/alias prefix
	Limit     int    // default 100, max 500
}

func (s *Store) ListNodes(ctx context.Context, in ListNodesInput) ([]Node, error) {
	if in.Limit <= 0 || in.Limit > 500 {
		in.Limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, project_id, kind, name, aliases, summary, path::text, weight, created_at, updated_at
		FROM brain.graph_nodes
		WHERE project_id = $1
		  AND ($2 = '' OR kind = $2)
		  AND ($3 = ''
		       OR name ILIKE $3 || '%'
		       OR EXISTS (SELECT 1 FROM unnest(aliases) a WHERE a ILIKE $3 || '%'))
		ORDER BY weight DESC, name
		LIMIT $4
	`, in.ProjectID, in.Kind, in.Search, in.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Node
	for rows.Next() {
		var n Node
		if err := rows.Scan(
			&n.ID, &n.ProjectID, &n.Kind, &n.Name, &n.Aliases, &n.Summary, &n.Path, &n.Weight,
			&n.CreatedAt, &n.UpdatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// ─── Edges ─────────────────────────────────────────────────

type UpsertEdgeInput struct {
	ProjectID       uuid.UUID
	SrcID           uuid.UUID
	DstID           uuid.UUID
	Relation        string
	Weight          float32
	EvidenceBlockID *uuid.UUID
}

func (s *Store) UpsertEdge(ctx context.Context, in UpsertEdgeInput) (*Edge, error) {
	if in.Weight == 0 {
		in.Weight = 1.0
	}
	e := &Edge{}
	err := s.pool.QueryRow(ctx, `
		INSERT INTO brain.graph_edges
			(project_id, src_id, dst_id, relation, weight, evidence_block_id)
		VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (project_id, src_id, dst_id, relation) DO UPDATE SET
			weight            = GREATEST(graph_edges.weight, EXCLUDED.weight),
			evidence_block_id = COALESCE(EXCLUDED.evidence_block_id, graph_edges.evidence_block_id)
		RETURNING id, project_id, src_id, dst_id, relation, weight, evidence_block_id, created_at
	`, in.ProjectID, in.SrcID, in.DstID, in.Relation, in.Weight, in.EvidenceBlockID).Scan(
		&e.ID, &e.ProjectID, &e.SrcID, &e.DstID, &e.Relation, &e.Weight, &e.EvidenceBlockID, &e.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("upsert edge: %w", err)
	}
	return e, nil
}

// ListEdges returns edges where the node appears on either side.
func (s *Store) ListEdges(ctx context.Context, projectID, nodeID uuid.UUID) ([]Edge, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, project_id, src_id, dst_id, relation, weight, evidence_block_id, created_at
		FROM brain.graph_edges
		WHERE project_id = $1 AND ($2 IN (src_id, dst_id))
		ORDER BY weight DESC, created_at
	`, projectID, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Edge
	for rows.Next() {
		var e Edge
		if err := rows.Scan(
			&e.ID, &e.ProjectID, &e.SrcID, &e.DstID, &e.Relation, &e.Weight, &e.EvidenceBlockID, &e.CreatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ─── Junction: block ↔ node ───────────────────────────────

func (s *Store) LinkBlock(ctx context.Context, blockID, nodeID uuid.UUID, confidence float32) error {
	if confidence == 0 {
		confidence = 1.0
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO brain.graph_block_nodes (block_id, node_id, confidence)
		VALUES ($1,$2,$3)
		ON CONFLICT (block_id, node_id)
		DO UPDATE SET confidence = GREATEST(graph_block_nodes.confidence, EXCLUDED.confidence)
	`, blockID, nodeID, confidence)
	return err
}

// ─── BFS traversal ─────────────────────────────────────────

type Neighbor struct {
	Node     Node
	Depth    int
	ViaEdge  uuid.UUID
	Relation string
}

// NeighborsBFS walks the graph from `seedID` up to `depth` hops along
// `relations` (empty = all). Implementation is a recursive CTE so a single
// round-trip handles arbitrary depth. Cycles are pruned by tracking visited
// node ids; a per-project node cap prevents runaway queries.
func (s *Store) NeighborsBFS(
	ctx context.Context,
	projectID, seedID uuid.UUID,
	depth int,
	relations []string,
	maxNodes int,
) ([]Neighbor, error) {
	if depth <= 0 {
		depth = 1
	}
	if depth > 6 {
		depth = 6
	}
	if maxNodes <= 0 || maxNodes > 1000 {
		maxNodes = 200
	}
	rows, err := s.pool.Query(ctx, `
		WITH RECURSIVE walk(id, depth, via, relation) AS (
			SELECT $2::uuid, 0, '00000000-0000-0000-0000-000000000000'::uuid, ''
			UNION
			SELECT
				CASE WHEN e.src_id = w.id THEN e.dst_id ELSE e.src_id END,
				w.depth + 1,
				e.id,
				e.relation
			FROM walk w
			JOIN brain.graph_edges e
			  ON e.project_id = $1
			 AND ($2::uuid IS NOT NULL)
			 AND (e.src_id = w.id OR e.dst_id = w.id)
			 AND ($4::text[] = '{}' OR e.relation = ANY($4::text[]))
			WHERE w.depth < $3
		)
		SELECT n.id, n.project_id, n.kind, n.name, n.aliases, n.summary,
		       n.path::text, n.weight, n.created_at, n.updated_at,
		       w.depth, w.via, w.relation
		FROM walk w
		JOIN brain.graph_nodes n ON n.id = w.id
		WHERE w.depth > 0
		ORDER BY w.depth, n.weight DESC
		LIMIT $5
	`, projectID, seedID, depth, relations, maxNodes)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Neighbor, 0)
	seen := map[uuid.UUID]bool{seedID: true}
	for rows.Next() {
		var nb Neighbor
		if err := rows.Scan(
			&nb.Node.ID, &nb.Node.ProjectID, &nb.Node.Kind, &nb.Node.Name,
			&nb.Node.Aliases, &nb.Node.Summary, &nb.Node.Path, &nb.Node.Weight,
			&nb.Node.CreatedAt, &nb.Node.UpdatedAt,
			&nb.Depth, &nb.ViaEdge, &nb.Relation,
		); err != nil {
			return nil, err
		}
		if seen[nb.Node.ID] {
			continue
		}
		seen[nb.Node.ID] = true
		out = append(out, nb)
	}
	return out, rows.Err()
}

// Backlinks returns blocks that mention (link to) `nodeID`.
func (s *Store) Backlinks(ctx context.Context, nodeID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT block_id FROM brain.graph_block_nodes
		WHERE node_id = $1
		ORDER BY confidence DESC
		LIMIT 200
	`, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func nullableString(s *string) any {
	if s == nil || *s == "" {
		return nil
	}
	return *s
}
