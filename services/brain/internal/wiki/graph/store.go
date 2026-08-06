// Persistence + lookup for the wiki page graph served by this package.
//
// The graph is read-only over two tables populated by the relevance
// worker (relevance/worker.go):
//
//   * brain.pages          id, title, frontmatter->>'type', community_id
//   * brain.page_relevance page_a, page_b, score   (undirected, page_a<b)
//
// page_relevance holds the top-K strongest relevance pairs per page
// (ScoreAll output), so the edges here are the *salient* connections,
// not every raw wikilink. That matches what the graph visualisation
// and the insights heuristics want to reason over.
package graph

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Node is one wiki page projected for the graph view. LinkCount is the
// node's degree in the relevance-edge graph and is exposed to the client
// as `weight` (node importance). Clustered is false when Louvain hasn't
// assigned a community (community_id NULL) — kept out of the JSON but
// used by the insights heuristics to avoid treating "unclustered" as a
// real community.
type Node struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Type      string `json:"page_type,omitempty"`
	Community int    `json:"community"`
	LinkCount int    `json:"weight"`

	clustered bool `json:"-"`
}

type Edge struct {
	Source string  `json:"source"`
	Target string  `json:"target"`
	Weight float32 `json:"weight"`
}

type Graph struct {
	Nodes []Node `json:"nodes"`
	Edges []Edge `json:"edges"`
}

// LoadProjectGraph loads the live pages + relevance edges for one
// project and derives per-node degree. Pages with no relevance edges
// still appear as nodes (LinkCount 0) so isolated-page detection works.
func (s *Store) LoadProjectGraph(ctx context.Context, projectID uuid.UUID) (*Graph, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, title, COALESCE(frontmatter->>'type', ''), community_id
		  FROM brain.pages
		 WHERE project_id = $1 AND deleted_at IS NULL
	`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	g := &Graph{Nodes: []Node{}, Edges: []Edge{}}
	indexByID := make(map[string]int)
	for rows.Next() {
		var (
			id          uuid.UUID
			title       string
			pageType    string
			communityID *int
		)
		if err := rows.Scan(&id, &title, &pageType, &communityID); err != nil {
			return nil, err
		}
		n := Node{
			ID:       id.String(),
			Title:    title,
			Type:     pageType,
			LinkCount: 0, // degree, derived from edges below
		}
		if communityID != nil {
			n.Community = *communityID
			n.clustered = true
		}
		indexByID[n.ID] = len(g.Nodes)
		g.Nodes = append(g.Nodes, n)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	edgeRows, err := s.pool.Query(ctx, `
		SELECT page_a, page_b, score
		  FROM brain.page_relevance
		 WHERE project_id = $1
	`, projectID)
	if err != nil {
		return nil, err
	}
	defer edgeRows.Close()
	for edgeRows.Next() {
		var (
			a, b  uuid.UUID
			score float32
		)
		if err := edgeRows.Scan(&a, &b, &score); err != nil {
			return nil, err
		}
		as, bs := a.String(), b.String()
		g.Edges = append(g.Edges, Edge{Source: as, Target: bs, Weight: score})
		// Undirected degree — bump both endpoints if present.
		if i, ok := indexByID[as]; ok {
			g.Nodes[i].LinkCount++
		}
		if i, ok := indexByID[bs]; ok {
			g.Nodes[i].LinkCount++
		}
	}
	if err := edgeRows.Err(); err != nil {
		return nil, err
	}

	// LinkCount is the JSON `weight` exposed to the client (node
	// importance = degree in the relevance-edge graph). Isolated pages
	// keep LinkCount 0; the Flutter client floors its render radius so
	// they still appear.
	return g, nil
}

// isStructural reports whether a page is a navigational/structural page
// (index, log, overview) that the insights heuristics should ignore —
// these link to everything and would dominate "bridge" / "surprising"
// results without carrying topical meaning. Matched on lowercased title
// or frontmatter type, mirroring reference/llm_wiki's STRUCTURAL_IDS.
func isStructural(n Node) bool {
	if n.Type == "overview" {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(n.Title)) {
	case "index", "log", "overview":
		return true
	}
	return false
}
