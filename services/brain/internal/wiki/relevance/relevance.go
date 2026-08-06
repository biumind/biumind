// Page relatedness — 4-signal scoring lifted from knowcode's
// graph-relevance scheme:
//
//  1. Direct wikilinks   ×3.0   blocks of A reference [[B]] (or B→A)
//  2. Adamic-Adar        ×1.5   shared neighbors weighted 1/log(deg)
//  3. Type affinity      ×1.0   frontmatter.type pair affinity matrix
//  4. Source overlap     ×4.0   pages share wiki_sources provenance
//
// All signals are pure functions over a pre-loaded ProjectGraph; no
// I/O, no DB. Worker producers (worker.go) load the graph from
// brain.pages + brain.blocks + brain.page_sources before invoking ScoreAll.
//
// Signal 4 (source overlap) landed with brain.page_sources many-to-many
// (migration 00058). webclip 抓取建页即写归属；upload 待 Phase 3 parser。
package relevance

import (
	"math"
	"regexp"
	"strings"

	"github.com/google/uuid"
)

// Signal weights match knowcode/llm_wiki byte-for-byte so dogfood
// scoring stays comparable across the migration. Tunable via
// ScoreOptions if a project wants to weight signals differently.
const (
	WeightDirectLink    = 3.0
	WeightAdamicAdar    = 1.5
	WeightTypeAffinity  = 1.0
	WeightSourceOverlap = 4.0 // 最高权重：同源血缘比人写 wikilink 更可靠
	DefaultTypeAffinity = 0.5 // fallback for missing-type pairs
)

// PageNode is the relevance-side projection of one wiki page. The
// title is used to resolve [[wikilinks]] and is lowercased + trimmed
// before populating so the link-resolver matches case-insensitively.
type PageNode struct {
	ID          uuid.UUID
	NormTitle   string                 // lowercase + trim of the page title
	Type        string                 // frontmatter.type ("entity" / "concept" / "source"), or ""
	OutgoingIDs map[uuid.UUID]struct{} // page IDs this page links to via [[wikilinks]]
	Sources     map[uuid.UUID]struct{} // wiki_sources IDs this page derives from (P1-4 source overlap)
}

// ProjectGraph is the input for ScoreAll — a snapshot of one project's
// pages + the wikilink edges between them. Producers load this once
// per worker tick and pass to ScoreAll for every page they want to
// rank neighbours for (or call ScoreAll once and cache).
type ProjectGraph struct {
	Pages map[uuid.UUID]*PageNode
}

// PairScore is one (page_a, page_b) result. PageA < PageB by UUID
// string ordering so callers can use the pair as a stable key.
type PairScore struct {
	PageA   uuid.UUID
	PageB   uuid.UUID
	Score   float32
	Signals map[string]float32
}

// ScoreOptions tunes the run. All zero values fall back to the
// constants above.
type ScoreOptions struct {
	WeightDirect        float32
	WeightAdamicAdar    float32
	WeightTypeAffinity  float32
	WeightSourceOverlap float32
	// MinScore floors the output: pairs scoring below are dropped so a
	// project with thousands of pages doesn't produce O(N²) rows.
	// Default 0.5 — empirically the noise floor in knowcode dogfood.
	MinScore float32
	// MaxNeighborsPerPage caps how many top-scoring neighbours each
	// page produces. The worker uses this to cap row counts per
	// project even on dense wikilink graphs. Default 30.
	MaxNeighborsPerPage int
}

func (o ScoreOptions) withDefaults() ScoreOptions {
	if o.WeightDirect == 0 {
		o.WeightDirect = WeightDirectLink
	}
	if o.WeightAdamicAdar == 0 {
		o.WeightAdamicAdar = WeightAdamicAdar
	}
	if o.WeightTypeAffinity == 0 {
		o.WeightTypeAffinity = WeightTypeAffinity
	}
	if o.WeightSourceOverlap == 0 {
		o.WeightSourceOverlap = WeightSourceOverlap
	}
	if o.MinScore == 0 {
		o.MinScore = 0.5
	}
	if o.MaxNeighborsPerPage <= 0 {
		o.MaxNeighborsPerPage = 30
	}
	return o
}

// ScoreAll computes relatedness for every undirected page pair in the
// graph and returns the per-page-cap top-K with score ≥ MinScore.
// Symmetry: pair (A,B) appears exactly once with PageA < PageB string
// ordering. Caller can iterate to write rows or build a per-page top-K.
func ScoreAll(graph *ProjectGraph, opt ScoreOptions) []PairScore {
	opt = opt.withDefaults()
	if graph == nil || len(graph.Pages) < 2 {
		return nil
	}

	// Pre-compute total degree per node (sum of in + out edges) for
	// the Adamic-Adar denominator. Self-loops aren't possible since
	// we filter them when reading the wikilink graph; the calculation
	// is undirected so an edge a→b counts once for both a and b.
	degree := computeDegree(graph)

	// Index outgoing edges into both-direction adjacency for cheap
	// "do A and B link, in either direction?" checks during scoring.
	adj := buildAdjacency(graph)

	// Build per-page top-K heap-style. Iterating ordered IDs means a
	// project with N pages computes N(N-1)/2 pair scores worst case;
	// with MinScore filtering most pairs short-circuit.
	ids := make([]uuid.UUID, 0, len(graph.Pages))
	for id := range graph.Pages {
		ids = append(ids, id)
	}
	// Stable order — string-sort UUIDs so output is deterministic
	// across runs (helps tests + makes SQL conflict resolution sane).
	sortUUIDs(ids)

	byPage := make(map[uuid.UUID]*perPageBucket, len(ids))

	for i := 0; i < len(ids); i++ {
		for j := i + 1; j < len(ids); j++ {
			a, b := ids[i], ids[j]
			score, signals := scorePair(graph.Pages[a], graph.Pages[b],
				adj, degree, opt)
			if score < opt.MinScore {
				continue
			}
			ps := PairScore{PageA: a, PageB: b, Score: score, Signals: signals}
			pushTopK(byPage, a, ps, opt.MaxNeighborsPerPage)
			// Mirror under b so the per-b cap is also enforced — same
			// PairScore object referenced from both lists, deduped
			// later by canonical (a<b) on emission.
			pushTopK(byPage, b, ps, opt.MaxNeighborsPerPage)
		}
	}

	// Two-step emit:
	//   (1) Union all per-page buckets, dedupe to canonical (a<b) keys.
	//   (2) Enforce the per-page cap globally — a pair survives only
	//       if it's still among each endpoint's top-K from its bucket.
	//
	// (1) alone over-emits when an endpoint's PageA-side cap dropped
	// the pair from one bucket but the other endpoint's bucket still
	// holds it. Without (2), the test for "model-relay limited to K neighbours"
	// fails when model-relay's UUID lands mid-range.
	candidates := make(map[[2]uuid.UUID]PairScore)
	for _, bucket := range byPage {
		if bucket == nil {
			continue
		}
		for _, ps := range bucket.entries {
			key := [2]uuid.UUID{ps.PageA, ps.PageB}
			candidates[key] = ps
		}
	}
	// Per-page survivor set: a pair must appear in BOTH endpoints'
	// buckets to be emitted. That's the strict interpretation of
	// "each page sees at most K neighbours in the output".
	inBucket := func(id uuid.UUID, key [2]uuid.UUID) bool {
		b := byPage[id]
		if b == nil {
			return false
		}
		for _, e := range b.entries {
			if e.PageA == key[0] && e.PageB == key[1] {
				return true
			}
		}
		return false
	}
	out := make([]PairScore, 0, len(candidates))
	for key, ps := range candidates {
		if !inBucket(key[0], key) || !inBucket(key[1], key) {
			continue
		}
		out = append(out, ps)
	}
	return out
}

// scorePair computes the weighted-sum score for one ordered pair (a,b).
// Returns the total + the per-signal contribution for diagnostics.
func scorePair(a, b *PageNode, adj map[uuid.UUID]map[uuid.UUID]struct{},
	degree map[uuid.UUID]int, opt ScoreOptions,
) (float32, map[string]float32) {
	signals := map[string]float32{}
	var total float32

	// Signal 1: direct wikilink in either direction.
	if hasDirectLink(a.ID, b.ID, adj) {
		s := opt.WeightDirect
		signals["direct_link"] = s
		total += s
	}

	// Signal 2: Adamic-Adar over shared neighbours. We iterate the
	// smaller adjacency set and probe the larger.
	if aa := adamicAdar(a.ID, b.ID, adj, degree); aa > 0 {
		s := opt.WeightAdamicAdar * aa
		signals["adamic_adar"] = s
		total += s
	}

	// Signal 3: type affinity. Multiplied by the weight so a perfect
	// match contributes weight×1.2; the default DefaultTypeAffinity
	// fallback gives a small constant baseline so two pages with
	// matching types get *something* even without other signals.
	if af := typeAffinity(a.Type, b.Type); af > 0 {
		s := opt.WeightTypeAffinity * af
		signals["type_affinity"] = s
		total += s
	}

	// Signal 4: source overlap — pages sharing wiki_sources provenance.
	// 裸交集计数 × 权重（对齐 reference/llm_wiki graph-relevance.ts:259-265）。
	if inter := sourceIntersect(a.Sources, b.Sources); inter > 0 {
		s := opt.WeightSourceOverlap * float32(inter)
		signals["source_overlap"] = s
		total += s
	}

	return total, signals
}

// sourceIntersect returns |A.sources ∩ B.sources|。两页共享的 wiki_sources 数。
func sourceIntersect(a, b map[uuid.UUID]struct{}) int {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	n := 0
	for s := range a {
		if _, ok := b[s]; ok {
			n++
		}
	}
	return n
}

// ─── Direct link check ────────────────────────────────────────

func hasDirectLink(a, b uuid.UUID, adj map[uuid.UUID]map[uuid.UUID]struct{}) bool {
	if neigh, ok := adj[a]; ok {
		if _, has := neigh[b]; has {
			return true
		}
	}
	if neigh, ok := adj[b]; ok {
		if _, has := neigh[a]; has {
			return true
		}
	}
	return false
}

// ─── Adamic-Adar ──────────────────────────────────────────────

// adamicAdar computes Σ 1/log(degree(c)) for each common neighbor c
// (excluding a and b themselves). High-degree hubs contribute little;
// rare shared neighbours carry the most signal. Returns 0 when the
// adjacency lookup fails (page has no edges).
func adamicAdar(a, b uuid.UUID, adj map[uuid.UUID]map[uuid.UUID]struct{},
	degree map[uuid.UUID]int,
) float32 {
	na, oka := adj[a]
	nb, okb := adj[b]
	if !oka || !okb || len(na) == 0 || len(nb) == 0 {
		return 0
	}
	// Iterate the smaller set.
	small, large := na, nb
	if len(nb) < len(na) {
		small, large = nb, na
	}
	var total float32
	for c := range small {
		if c == a || c == b {
			continue
		}
		if _, has := large[c]; !has {
			continue
		}
		d := degree[c]
		if d <= 1 {
			// log(1) = 0 → divide-by-zero. Treat degree≤1 as a
			// signal-less model-relay (it isn't a model-relay).
			continue
		}
		total += float32(1.0 / math.Log(float64(d)))
	}
	return total
}

// ─── Type affinity ────────────────────────────────────────────

// typeAffinityMatrix is the constant from knowcode (lifted from
// llm_wiki's graph-relevance.ts). Returned values are multiplied by
// WeightTypeAffinity in scorePair; this matrix's domain is roughly
// [0.5, 1.2].
var typeAffinityMatrix = map[string]map[string]float32{
	"entity": {
		"concept": 1.2, "entity": 0.8, "source": 1.0,
		"synthesis": 1.0, "query": 0.8,
	},
	"concept": {
		"entity": 1.2, "concept": 0.8, "source": 1.0,
		"synthesis": 1.2, "query": 1.0,
	},
	"source": {
		"entity": 1.0, "concept": 1.0, "source": 0.5,
		"query": 0.8, "synthesis": 1.0,
	},
	"query": {
		"concept": 1.0, "entity": 0.8, "synthesis": 1.0,
		"source": 0.8, "query": 0.5,
	},
	"synthesis": {
		"concept": 1.2, "entity": 1.0, "source": 1.0,
		"query": 1.0, "synthesis": 0.8,
	},
}

func typeAffinity(a, b string) float32 {
	if a == "" || b == "" {
		// Missing type → neutral baseline. Better than dropping the
		// signal entirely because newly-ingested pages take time to
		// pick up frontmatter, and we don't want them invisible.
		return DefaultTypeAffinity
	}
	row, ok := typeAffinityMatrix[a]
	if !ok {
		return DefaultTypeAffinity
	}
	v, ok := row[b]
	if !ok {
		return DefaultTypeAffinity
	}
	return v
}

// ─── Wikilink resolution ──────────────────────────────────────

// wikilinkRE — same shape as reviews/lint.go, kept duplicated here
// rather than imported across packages because relevance is a leaf
// (lint depends on path_safety; relevance has no other domain deps).
var wikilinkRE = regexp.MustCompile(`\[\[([^\]|\n]+)(?:\|[^\]\n]*)?\]\]`)

// ResolveWikilinks scans `text` for [[target]] mentions and returns
// the ids of pages whose normalised title matches. Never produces
// the source page id (a self-link is a no-op). Caller is responsible
// for de-duping per source page.
func ResolveWikilinks(text string, byTitle map[string]uuid.UUID, exclude uuid.UUID) []uuid.UUID {
	if !strings.Contains(text, "[[") {
		return nil
	}
	matches := wikilinkRE.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[uuid.UUID]struct{})
	var out []uuid.UUID
	for _, m := range matches {
		target := strings.TrimSpace(strings.ToLower(m[1]))
		if target == "" {
			continue
		}
		id, ok := byTitle[target]
		if !ok || id == exclude {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

// ─── helpers ──────────────────────────────────────────────────

func computeDegree(graph *ProjectGraph) map[uuid.UUID]int {
	out := make(map[uuid.UUID]int, len(graph.Pages))
	for id, p := range graph.Pages {
		out[id] += len(p.OutgoingIDs)
		for target := range p.OutgoingIDs {
			out[target]++
		}
	}
	return out
}

func buildAdjacency(graph *ProjectGraph) map[uuid.UUID]map[uuid.UUID]struct{} {
	adj := make(map[uuid.UUID]map[uuid.UUID]struct{}, len(graph.Pages))
	for id, p := range graph.Pages {
		if _, ok := adj[id]; !ok {
			adj[id] = map[uuid.UUID]struct{}{}
		}
		for t := range p.OutgoingIDs {
			adj[id][t] = struct{}{}
			if _, ok := adj[t]; !ok {
				adj[t] = map[uuid.UUID]struct{}{}
			}
			// Treat wikilinks as undirected for adjacency / Adamic-Adar.
			// "A links to B" makes B a neighbour of A regardless.
			adj[t][id] = struct{}{}
		}
	}
	return adj
}

func pushTopK(buckets map[uuid.UUID]*perPageBucket, id uuid.UUID,
	ps PairScore, maxK int,
) {
	b := buckets[id]
	if b == nil {
		b = &perPageBucket{}
		buckets[id] = b
	}
	b.push(ps, maxK)
}

// perPageBucket is a tiny insertion-sort top-K. K is small (default 30)
// so a heap would be over-engineered — linear insert is faster in
// practice for K < ~50.
type perPageBucket struct {
	entries []PairScore
}

func (b *perPageBucket) push(ps PairScore, maxK int) {
	if len(b.entries) < maxK {
		b.entries = append(b.entries, ps)
		// Bubble down — keep entries sorted DESC by score so the last
		// element is always the "weakest" candidate to evict.
		for i := len(b.entries) - 1; i > 0; i-- {
			if b.entries[i].Score > b.entries[i-1].Score {
				b.entries[i-1], b.entries[i] = b.entries[i], b.entries[i-1]
			} else {
				break
			}
		}
		return
	}
	// At capacity — evict the weakest if ps is stronger.
	weakest := &b.entries[len(b.entries)-1]
	if ps.Score <= weakest.Score {
		return
	}
	b.entries[len(b.entries)-1] = ps
	for i := len(b.entries) - 1; i > 0; i-- {
		if b.entries[i].Score > b.entries[i-1].Score {
			b.entries[i-1], b.entries[i] = b.entries[i], b.entries[i-1]
		} else {
			break
		}
	}
}

func sortUUIDs(ids []uuid.UUID) {
	// Insertion sort on []uuid.UUID. The slice is bounded by project
	// page count (single-digit-thousand worst case) and the comparator
	// is string-based. stdlib sort.Slice would work; insertion sort
	// keeps the dependency surface tighter.
	for i := 1; i < len(ids); i++ {
		for j := i; j > 0 && ids[j].String() < ids[j-1].String(); j-- {
			ids[j-1], ids[j] = ids[j], ids[j-1]
		}
	}
}
