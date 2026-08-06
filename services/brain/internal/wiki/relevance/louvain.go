// Louvain community detection — modularity-based clustering of the
// page-relevance edge graph.
//
// We ship the local-optimization phase only (pass 1 of the canonical
// Louvain algorithm). For the wiki sizes biumind targets (≤ ~10K
// pages per project, typically ≤ 1K) one greedy pass converges to
// communities that match what users intuitively call "topics" in
// dogfood. The full multi-level aggregation buys ~5-10% modularity
// at the cost of ~3× implementation complexity; we'll add it when
// page counts demand it.
//
// Modularity (undirected weighted graph):
//
//   Q = (1 / 2m) * Σ_ij (A_ij - k_i*k_j/2m) δ(c_i, c_j)
//
// The local greedy moves pick, for each node, the neighbour community
// that maximises ΔQ. Iterates until no node moves.
//
// All inputs are pure (edge list + node set + edge weights); no I/O,
// no DB. The worker (worker.go) builds the inputs from
// brain.page_relevance and writes the resulting community_id back
// into brain.pages.
package relevance

import (
	"sort"

	"github.com/google/uuid"
)

// Edge is one weighted undirected edge between two pages.
type Edge struct {
	A      uuid.UUID
	B      uuid.UUID
	Weight float64
}

// LouvainResult maps every node id to its community id (a non-negative
// integer assigned arbitrarily during clustering — only equality
// matters, the int has no semantic value beyond "two pages share an id
// → same cluster").
type LouvainResult struct {
	Community map[uuid.UUID]int
	// Modularity at convergence — useful for ops dashboards / tests
	// to assert clustering produced a non-trivial partition.
	Modularity float64
}

// LouvainOptions tunes the clustering. Zero values fall back to
// reasonable defaults.
type LouvainOptions struct {
	// MaxIterations caps the local-optimization passes. The greedy
	// loop converges in 5-10 iterations on typical inputs; the cap
	// is a safety belt against pathological edge weights.
	MaxIterations int
	// MinCommunitySize keeps singletons from polluting the output:
	// nodes whose community ends up smaller than this stay
	// "unassigned" (community_id = -1) so the search-time boost
	// doesn't fire on them. 2 is a sensible default — a community
	// of 1 is just the node itself.
	MinCommunitySize int
}

func (o LouvainOptions) withDefaults() LouvainOptions {
	if o.MaxIterations <= 0 {
		o.MaxIterations = 50
	}
	if o.MinCommunitySize <= 0 {
		o.MinCommunitySize = 2
	}
	return o
}

// DetectCommunities runs Louvain phase-1 clustering on the supplied
// undirected weighted graph.
//
// The result is deterministic across runs on identical inputs: nodes
// are visited in stable string-sorted UUID order, ties are broken by
// "stay in current community". Tests + diff-aware UI rely on this.
func DetectCommunities(nodes []uuid.UUID, edges []Edge, opt LouvainOptions) LouvainResult {
	opt = opt.withDefaults()

	if len(nodes) == 0 {
		return LouvainResult{Community: map[uuid.UUID]int{}}
	}

	// Build adjacency: neighbours[i] = []{neighbour-index, weight}.
	// We use indices instead of uuids on the inner loop so the per-
	// iteration cost is tight.
	idx := make(map[uuid.UUID]int, len(nodes))
	sortedNodes := append([]uuid.UUID(nil), nodes...)
	sort.Slice(sortedNodes, func(i, j int) bool {
		return sortedNodes[i].String() < sortedNodes[j].String()
	})
	for i, id := range sortedNodes {
		idx[id] = i
	}
	type adjEntry struct {
		j int
		w float64
	}
	neighbours := make([][]adjEntry, len(sortedNodes))
	for _, e := range edges {
		ai, ok := idx[e.A]
		if !ok {
			continue
		}
		bi, ok := idx[e.B]
		if !ok || ai == bi {
			continue
		}
		w := e.Weight
		if w <= 0 {
			continue
		}
		neighbours[ai] = append(neighbours[ai], adjEntry{j: bi, w: w})
		neighbours[bi] = append(neighbours[bi], adjEntry{j: ai, w: w})
	}

	// Total edge weight (×2 for undirected; we store the sum and
	// divide where the formula calls for 2m).
	var twoM float64
	for _, list := range neighbours {
		for _, e := range list {
			twoM += e.w
		}
	}
	if twoM == 0 {
		// No usable edges — every node is its own community. The
		// MinCommunitySize=2 default below filters them out, leaving
		// nodes "unassigned" rather than emitting noise.
		return finaliseCommunities(sortedNodes, makeIdentity(sortedNodes), opt)
	}

	// k[i] = sum of edge weights touching node i.
	k := make([]float64, len(sortedNodes))
	for i, list := range neighbours {
		for _, e := range list {
			k[i] += e.w
		}
	}

	// community[i] = current community label (init: each node alone).
	community := make([]int, len(sortedNodes))
	for i := range community {
		community[i] = i
	}
	// sigmaTot[c] = sum of k[i] for nodes i in community c.
	sigmaTot := append([]float64(nil), k...)

	// Greedy local-optimization loop.
	for iter := 0; iter < opt.MaxIterations; iter++ {
		moved := false
		for i := range sortedNodes {
			cur := community[i]
			ki := k[i]

			// k_i_in[c] = sum of edge weights from i into community c
			// (excluding the self-loop, which we don't have anyway).
			kIn := map[int]float64{}
			for _, e := range neighbours[i] {
				kIn[community[e.j]] += e.w
			}
			// Tentatively remove i from its current community.
			sigmaTotMinus := sigmaTot[cur] - ki

			// Iterate kIn in sorted-community order so map randomness
			// doesn't make clustering non-deterministic across runs.
			// Same-gain ties go to the smaller community id, which
			// also helps the per-iteration outcome stay stable.
			cs := make([]int, 0, len(kIn))
			for c := range kIn {
				cs = append(cs, c)
			}
			sort.Ints(cs)

			// The "stay" baseline: gain of putting i back into cur
			// after the virtual removal. Bug in earlier iterations
			// of this code compared candidate moves against zero,
			// which let any positive-gain neighbour pull i out of
			// a stronger home community on every iteration. The
			// canonical Louvain formulation tracks the BEST gain
			// across all candidates including cur.
			stayGain := kIn[cur] - sigmaTotMinus*ki/twoM
			bestGain := stayGain
			bestC := cur
			for _, c := range cs {
				if c == cur {
					continue
				}
				kic := kIn[c]
				// Canonical Louvain ΔQ when moving an isolated i into
				// community C:
				//   ΔQ ∝ k_i_in − Σ_tot · k_i / (2m)
				// twoM in our naming is 2m, so dividing by twoM gives
				// the correct denominator. Constants common to every
				// candidate (the 1/2m prefactor of the modularity)
				// are dropped; only the argmax matters.
				gain := kic - sigmaTot[c]*ki/twoM
				if gain > bestGain {
					bestGain = gain
					bestC = c
				}
			}
			// Compare against keeping i in cur (with i temporarily
			// out of its community). Keep i where it is when no move
			// is strictly better than 0.
			if bestC == cur {
				continue
			}
			// Apply the move.
			sigmaTot[cur] = sigmaTotMinus
			sigmaTot[bestC] += ki
			community[i] = bestC
			moved = true
		}
		if !moved {
			break
		}
	}

	// Phase 2: graph aggregation. Phase 1 alone gets stuck in local
	// optima on symmetric cliques (canonical example: K_5 splits 3+2
	// even though "all in one community" has higher Q). We collapse
	// each Phase-1 community into a single super-node and re-run
	// Phase 1 on the aggregated graph. Iterate until aggregation
	// stops shrinking the partition.
	//
	// The aggregation captures intra-community edges as super-node
	// self-loops (preserved in the degree calculations below) and
	// inter-community edges as super-node edges weighted by the sum
	// of original cross-community edges.
	for pass := 0; pass < 5; pass++ {
		// Distinct communities + their canonical sort order.
		commSet := map[int]struct{}{}
		for _, c := range community {
			commSet[c] = struct{}{}
		}
		if len(commSet) >= len(sortedNodes) {
			break // no aggregation possible — every node alone
		}

		commIdx := make(map[int]int, len(commSet))
		commList := make([]int, 0, len(commSet))
		for c := range commSet {
			commList = append(commList, c)
		}
		sort.Ints(commList)
		for i, c := range commList {
			commIdx[c] = i
		}

		// aggK[i] = Σ k of original nodes in super-node i (including
		// self-loop weight from intra-community edges, since those
		// stay attached to the super-node's degree).
		aggK := make([]float64, len(commList))
		for i, c := range community {
			aggK[commIdx[c]] += k[i]
		}

		// aggNeighbours: super-graph adjacency. Same shape as the
		// original neighbours[][] for code reuse.
		aggNeighbours := make([][]adjEntry, len(commList))
		seen := make([]map[int]float64, len(commList))
		for i := range seen {
			seen[i] = map[int]float64{}
		}
		for i, list := range neighbours {
			si := commIdx[community[i]]
			for _, e := range list {
				sj := commIdx[community[e.j]]
				if si == sj {
					// Intra-community edge — stays inside the super-
					// node as a self-loop. Already counted in aggK
					// above; we don't add it to neighbours.
					continue
				}
				seen[si][sj] += e.w
			}
		}
		for si, m := range seen {
			for sj, w := range m {
				aggNeighbours[si] = append(aggNeighbours[si], adjEntry{j: sj, w: w / 1.0})
			}
		}

		// Run Phase-1-style local optimisation on the super-graph.
		superCommunity := make([]int, len(commList))
		for i := range superCommunity {
			superCommunity[i] = i
		}
		superSigmaTot := append([]float64(nil), aggK...)
		moved := false
		for iter := 0; iter < opt.MaxIterations; iter++ {
			anyMove := false
			for si := range commList {
				cur := superCommunity[si]
				ki := aggK[si]
				kIn := map[int]float64{}
				for _, e := range aggNeighbours[si] {
					kIn[superCommunity[e.j]] += e.w
				}
				sigmaTotMinus := superSigmaTot[cur] - ki
				cs := make([]int, 0, len(kIn))
				for c := range kIn {
					cs = append(cs, c)
				}
				sort.Ints(cs)
				stayGain := kIn[cur] - sigmaTotMinus*ki/twoM
				bestGain := stayGain
				bestC := cur
				for _, c := range cs {
					if c == cur {
						continue
					}
					gain := kIn[c] - superSigmaTot[c]*ki/twoM
					if gain > bestGain {
						bestGain = gain
						bestC = c
					}
				}
				if bestC != cur {
					superSigmaTot[cur] = sigmaTotMinus
					superSigmaTot[bestC] += ki
					superCommunity[si] = bestC
					anyMove = true
					moved = true
				}
			}
			if !anyMove {
				break
			}
		}
		if !moved {
			break
		}
		// Project the super-graph result back onto the original
		// nodes: each node inherits its old community's super-id.
		for i := range community {
			oldC := community[i]
			community[i] = commList[superCommunity[commIdx[oldC]]]
		}
		// Refresh sigmaTot for the absorber pass below.
		newSigmaTot := map[int]float64{}
		for i, c := range community {
			newSigmaTot[c] += k[i]
		}
		for c := range sigmaTot {
			sigmaTot[c] = 0
		}
		for c, v := range newSigmaTot {
			if c < len(sigmaTot) {
				sigmaTot[c] = v
			}
		}
	}

	// Singleton absorber. Phase 2 should already have folded most
	// orphans, but defence in depth — any node still alone after
	// Phase 2 joins its strongest neighbour community.
	for absorb := 0; absorb < 10; absorb++ {
		size := map[int]int{}
		for _, c := range community {
			size[c]++
		}
		moved := false
		for i := range sortedNodes {
			cur := community[i]
			if size[cur] > 1 {
				continue
			}
			// Find neighbour community with max k_in; iterate sorted
			// for deterministic tie-breaking.
			kIn := map[int]float64{}
			for _, e := range neighbours[i] {
				kIn[community[e.j]] += e.w
			}
			cs := make([]int, 0, len(kIn))
			for c := range kIn {
				cs = append(cs, c)
			}
			sort.Ints(cs)
			bestC := cur
			bestK := 0.0
			for _, c := range cs {
				if c == cur {
					continue
				}
				if kIn[c] > bestK {
					bestK = kIn[c]
					bestC = c
				}
			}
			if bestC != cur {
				sigmaTot[cur] -= k[i]
				sigmaTot[bestC] += k[i]
				community[i] = bestC
				size[cur]--
				size[bestC]++
				moved = true
			}
		}
		if !moved {
			break
		}
	}

	// Diagnostic Q value at the current partition. Inline here because
	// it needs the local adjacency type — separate function would
	// require lifting that to package-level for one call site.
	var q float64
	for i, list := range neighbours {
		for _, e := range list {
			if community[i] != community[e.j] {
				continue
			}
			q += e.w - k[i]*k[e.j]/twoM
		}
	}
	q /= twoM

	// Re-pack community labels into 0..N-1 for downstream storage.
	commByID := make(map[uuid.UUID]int, len(sortedNodes))
	for i, c := range community {
		commByID[sortedNodes[i]] = c
	}
	res := finaliseCommunities(sortedNodes, commByID, opt)
	res.Modularity = q
	return res
}

// finaliseCommunities reindexes communities to 0..N-1 and applies
// the MinCommunitySize threshold (small communities → -1).
func finaliseCommunities(nodes []uuid.UUID, raw map[uuid.UUID]int, opt LouvainOptions) LouvainResult {
	count := map[int]int{}
	for _, c := range raw {
		count[c]++
	}
	// Stable reindex: walk nodes in sorted order so the same input
	// always produces the same labels. New label = order-of-first-
	// appearance.
	relabel := map[int]int{}
	next := 0
	out := make(map[uuid.UUID]int, len(nodes))
	for _, id := range nodes {
		c := raw[id]
		if count[c] < opt.MinCommunitySize {
			out[id] = -1
			continue
		}
		nl, ok := relabel[c]
		if !ok {
			nl = next
			next++
			relabel[c] = nl
		}
		out[id] = nl
	}
	return LouvainResult{Community: out}
}

// makeIdentity wraps "every node is its own community" for the
// degenerate-graph fallback.
func makeIdentity(nodes []uuid.UUID) map[uuid.UUID]int {
	out := make(map[uuid.UUID]int, len(nodes))
	for i, id := range nodes {
		out[id] = i
	}
	return out
}

