package relevance

import (
	"testing"

	"github.com/google/uuid"
)

func TestDetectCommunities_TwoCliquesProduceTwoCommunities(t *testing.T) {
	// Build two disjoint triangles connected by a single weak edge.
	// A weighted ratio of 10:1 (intra:inter) is enough to push
	// Louvain into the canonical two-community partition.
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	x, y, z := uuid.New(), uuid.New(), uuid.New()
	nodes := []uuid.UUID{a, b, c, x, y, z}
	edges := []Edge{
		{a, b, 10}, {b, c, 10}, {a, c, 10},
		{x, y, 10}, {y, z, 10}, {x, z, 10},
		{c, x, 1}, // bridge
	}
	res := DetectCommunities(nodes, edges, LouvainOptions{})
	if len(res.Community) != 6 {
		t.Fatalf("want 6 community assignments, got %d", len(res.Community))
	}

	// Check: a, b, c share community ≠ x, y, z's community.
	abc := []int{res.Community[a], res.Community[b], res.Community[c]}
	xyz := []int{res.Community[x], res.Community[y], res.Community[z]}
	if abc[0] != abc[1] || abc[1] != abc[2] {
		t.Errorf("abc should share a community, got %v", abc)
	}
	if xyz[0] != xyz[1] || xyz[1] != xyz[2] {
		t.Errorf("xyz should share a community, got %v", xyz)
	}
	if abc[0] == xyz[0] {
		t.Errorf("abc and xyz should be in different communities, both = %d", abc[0])
	}
	if res.Modularity <= 0 {
		t.Errorf("modularity for two-clique graph should be > 0, got %v",
			res.Modularity)
	}
}

func TestDetectCommunities_SingletonsBecomeUnassigned(t *testing.T) {
	// Two disconnected components, each of size 1: with the default
	// MinCommunitySize=2 they're filtered to "-1" so search-time
	// boost doesn't fire on them.
	a, b := uuid.New(), uuid.New()
	res := DetectCommunities([]uuid.UUID{a, b}, nil, LouvainOptions{})
	if res.Community[a] != -1 || res.Community[b] != -1 {
		t.Errorf("isolated nodes should be -1, got a=%d b=%d",
			res.Community[a], res.Community[b])
	}
}

func TestDetectCommunities_RespectsMinCommunitySize(t *testing.T) {
	// 5-node clique + 1 isolated node. The clique should cluster
	// together; the isolate should remain unassigned.
	c1, c2, c3, c4, c5 := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	iso := uuid.New()
	nodes := []uuid.UUID{c1, c2, c3, c4, c5, iso}
	edges := []Edge{
		{c1, c2, 1}, {c2, c3, 1}, {c3, c4, 1}, {c4, c5, 1}, {c5, c1, 1},
		{c1, c3, 1}, {c2, c4, 1}, {c3, c5, 1},
	}
	res := DetectCommunities(nodes, edges, LouvainOptions{})
	if res.Community[iso] != -1 {
		t.Errorf("isolated node should be -1, got %d", res.Community[iso])
	}
	clique := []int{
		res.Community[c1], res.Community[c2], res.Community[c3],
		res.Community[c4], res.Community[c5],
	}
	for i := 1; i < len(clique); i++ {
		if clique[i] != clique[0] {
			t.Errorf("clique should share community, got %v", clique)
		}
	}
}

func TestDetectCommunities_DeterministicOrdering(t *testing.T) {
	// Same input run twice must produce identical labels (we sort
	// nodes by uuid string + relabel by first-appearance order).
	a, b, c, d := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	nodes := []uuid.UUID{a, b, c, d}
	edges := []Edge{{a, b, 5}, {c, d, 5}, {a, c, 1}}
	r1 := DetectCommunities(nodes, edges, LouvainOptions{})
	r2 := DetectCommunities(nodes, edges, LouvainOptions{})
	for _, n := range nodes {
		if r1.Community[n] != r2.Community[n] {
			t.Errorf("non-deterministic: node %s got %d then %d",
				n, r1.Community[n], r2.Community[n])
		}
	}
}

func TestDetectCommunities_EmptyInput(t *testing.T) {
	res := DetectCommunities(nil, nil, LouvainOptions{})
	if len(res.Community) != 0 {
		t.Errorf("empty input should yield empty result, got %v", res.Community)
	}
}

func TestDetectCommunities_RespectsCustomMinSize(t *testing.T) {
	// 3-node clique: with MinCommunitySize=10 they should all become
	// unassigned because their community is too small to qualify.
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	res := DetectCommunities(
		[]uuid.UUID{a, b, c},
		[]Edge{{a, b, 1}, {b, c, 1}, {a, c, 1}},
		LouvainOptions{MinCommunitySize: 10},
	)
	for _, n := range []uuid.UUID{a, b, c} {
		if res.Community[n] != -1 {
			t.Errorf("expected unassigned with MinCommunitySize=10, got %d",
				res.Community[n])
		}
	}
}

func TestDetectCommunities_ZeroOrNegativeWeightEdgesIgnored(t *testing.T) {
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	res := DetectCommunities(
		[]uuid.UUID{a, b, c},
		[]Edge{
			{a, b, 0},     // dropped
			{a, c, -5},    // dropped
		},
		LouvainOptions{},
	)
	// All nodes effectively isolated → all -1.
	for _, n := range []uuid.UUID{a, b, c} {
		if res.Community[n] != -1 {
			t.Errorf("nodes with no real edges should be -1, got %d",
				res.Community[n])
		}
	}
}
