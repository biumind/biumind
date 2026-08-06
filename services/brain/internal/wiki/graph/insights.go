// Graph insights — structural heuristics (pure algorithm, no LLM).
//
// Two families of findings:
//
//   - Surprising connections — relevance edges that "shouldn't" exist by
//     cluster/type/degree intuition: cross-community, cross-type,
//     peripheral↔hub, or weak-but-present. Each scored; top-N returned.
//
//   - Knowledge gaps — structurally weak spots: isolated pages (degree
//     ≤1), sparse communities (low internal cohesion), and bridge nodes
//     (connecting ≥3 clusters).
//
// The scoring constants and thresholds are fixed values; dogfood output
// stays comparable across runs. The data shape differs (biumind edges
// come from page_relevance, not raw wikilinks, and communities live on
// brain.pages.community_id).
package graph

import "sort"

// Brief is the node projection embedded in insight findings — enough
// for the client to render a label + deep link without a second round
// trip.
type Brief struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Type  string `json:"type"`
}

// Surprising is one scored cross-cutting edge.
type Surprising struct {
	Source  Brief    `json:"source"`
	Target  Brief    `json:"target"`
	Score   int      `json:"score"`
	Reasons []string `json:"reasons"`
	Key     string   `json:"key"` // stable dismiss key: sorted "id:::id"
}

// Gap is one detected knowledge-gap finding.
type Gap struct {
	Type        string   `json:"type"` // isolated-node | sparse-community | bridge-node
	Title       string   `json:"title"`
	Description string   `json:"description"`
	NodeIDs     []string `json:"node_ids"`
	Suggestion  string   `json:"suggestion"`
}

// Insights is the full payload returned by POST /graph/insights.
type Insights struct {
	Surprising []Surprising `json:"surprising_connections"`
	Gaps       []Gap        `json:"knowledge_gaps"`
	Stats      Stats        `json:"stats"`
}

// Stats summarises the graph the insights were computed over, so the
// client can show "12 pages · 18 edges · 3 clusters" alongside findings.
type Stats struct {
	NodeCount      int `json:"node_count"`
	EdgeCount      int `json:"edge_count"`
	CommunityCount int `json:"community_count"`
}

// distantTypePairs mirrors reference's distantPairs set — type pairs
// judged "far apart" (worth +2 vs the +1 generic cross-type bonus).
var distantTypePairs = map[string]bool{
	"source-concept": true, "concept-source": true,
	"source-synthesis": true, "synthesis-source": true,
	"query-entity": true, "entity-query": true,
}

// ComputeInsights runs both heuristics over a loaded project graph.
// nil/empty graph returns an empty (zero-length) result, not an error —
// a project whose relevance worker hasn't run yet simply has no findings.
func ComputeInsights(g *Graph) *Insights {
	out := &Insights{
		Surprising: []Surprising{},
		Gaps:       []Gap{},
	}
	if g == nil || len(g.Nodes) == 0 {
		return out
	}

	out.Stats.NodeCount = len(g.Nodes)
	out.Stats.EdgeCount = len(g.Edges)
	out.Stats.CommunityCount = countCommunities(g)
	out.Surprising = findSurprising(g, 5)
	out.Gaps = detectGaps(g, 8)
	return out
}

func countCommunities(g *Graph) int {
	seen := map[int]struct{}{}
	for _, n := range g.Nodes {
		if n.clustered {
			seen[n.Community] = struct{}{}
		}
	}
	return len(seen)
}

// ─── Surprising connections ────────────────────────────────────

func findSurprising(g *Graph, limit int) []Surprising {
	byID := make(map[string]Node, len(g.Nodes))
	maxDegree := 1
	for _, n := range g.Nodes {
		byID[n.ID] = n
		if n.LinkCount > maxDegree {
			maxDegree = n.LinkCount
		}
	}

	var scored = []Surprising{}
	for _, e := range g.Edges {
		src, sok := byID[e.Source]
		tgt, tok := byID[e.Target]
		if !sok || !tok {
			continue
		}
		if isStructural(src) || isStructural(tgt) {
			continue
		}

		score := 0
		var reasons []string

		// Signal 1: cross-community edge (+3). Only counts when both
		// endpoints are actually clustered — two unclustered pages share
		// the default 0 but that's "unknown", not "same cluster".
		if src.clustered && tgt.clustered && src.Community != tgt.Community {
			score += 3
			reasons = append(reasons, "crosses community boundary")
		}

		// Signal 2: cross-type edge (+2 distant, +1 generic).
		if src.Type != "" && tgt.Type != "" && src.Type != tgt.Type {
			pair := src.Type + "-" + tgt.Type
			if distantTypePairs[pair] {
				score += 2
				reasons = append(reasons, "connects "+src.Type+" to "+tgt.Type)
			} else {
				score += 1
				reasons = append(reasons, "different types")
			}
		}

		// Signal 3: peripheral-to-hub coupling (+2). Compare against
		// maxDegree*0.5 as a float; keep the float compare so the threshold
		// stays exact (int /2 truncation would be looser on odd maxDegree).
		minDeg := src.LinkCount
		maxDeg := tgt.LinkCount
		if maxDeg < minDeg {
			minDeg, maxDeg = maxDeg, minDeg
		}
		if minDeg <= 2 && float64(maxDeg) >= float64(maxDegree)*0.5 {
			score += 2
			reasons = append(reasons, "peripheral node links to hub")
		}

		// Signal 4: weak-but-present edge (+1).
		if e.Weight > 0 && e.Weight < 2 {
			score += 1
			reasons = append(reasons, "weak but present connection")
		}

		if score >= 3 && len(reasons) > 0 {
			key := dismissKey(src.ID, tgt.ID)
			scored = append(scored, Surprising{
				Source:  brief(src),
				Target:  brief(tgt),
				Score:   score,
				Reasons: reasons,
				Key:     key,
			})
		}
	}

	sort.Slice(scored, func(i, j int) bool { return scored[i].Score > scored[j].Score })
	if limit > 0 && len(scored) > limit {
		scored = scored[:limit]
	}
	return scored
}

// ─── Knowledge gaps ────────────────────────────────────────────

func detectGaps(g *Graph, limit int) []Gap {
	byID := make(map[string]Node, len(g.Nodes))
	for _, n := range g.Nodes {
		byID[n.ID] = n
	}

	var gaps = []Gap{}

	// 1. Isolated nodes (degree ≤ 1).
	var isolated []Node
	for _, n := range g.Nodes {
		if n.LinkCount <= 1 && !isStructural(n) {
			isolated = append(isolated, n)
		}
	}
	if len(isolated) > 0 {
		top := isolated
		if len(top) > 5 {
			top = top[:5]
		}
		labels := make([]string, 0, len(top))
		ids := make([]string, 0, len(isolated))
		for _, n := range top {
			labels = append(labels, n.Title)
		}
		for _, n := range isolated {
			ids = append(ids, n.ID)
		}
		desc := joinLabels(labels)
		if len(isolated) > 5 {
			desc += pluralMore(len(isolated) - 5)
		}
		gaps = append(gaps, Gap{
			Type:        "isolated-node",
			Title:       pluralTitle(len(isolated), "isolated page"),
			Description: desc,
			NodeIDs:     ids,
			Suggestion:  "These pages have few or no connections. Consider adding [[wikilinks]] to related pages, or research to expand their content.",
		})
	}

	// 2. Sparse communities (cohesion < 0.15 with ≥3 nodes).
	commMembers := map[int][]Node{}
	for _, n := range g.Nodes {
		if n.clustered {
			commMembers[n.Community] = append(commMembers[n.Community], n)
		}
	}
	internal := internalEdgeCounts(g, byID)
	for comm, members := range commMembers {
		n := len(members)
		if n < 3 {
			continue
		}
		maxEdges := n * (n - 1) / 2
		cohesion := 0.0
		if maxEdges > 0 {
			cohesion = float64(internal[comm]) / float64(maxEdges)
		}
		if cohesion < 0.15 {
			title := "Community " + itoa(comm)
			if members[0].Title != "" {
				title = members[0].Title
			}
			ids := make([]string, 0, n)
			for _, m := range members {
				ids = append(ids, m.ID)
			}
			gaps = append(gaps, Gap{
				Type:        "sparse-community",
				Title:       "Sparse cluster: " + title,
				Description: itoa(n) + " pages with cohesion " + ftoa(cohesion) + " — internal connections are weak.",
				NodeIDs:     ids,
				Suggestion:  "This knowledge area lacks internal cross-references. Consider adding links between these pages or researching to fill gaps.",
			})
		}
	}

	// 3. Bridge nodes (neighbours span ≥3 communities).
	neighborComms := map[string]map[int]struct{}{}
	for _, n := range g.Nodes {
		neighborComms[n.ID] = map[int]struct{}{}
	}
	for _, e := range g.Edges {
		s, sok := byID[e.Source]
		t, tok := byID[e.Target]
		if !sok || !tok {
			continue
		}
		if t.clustered {
			neighborComms[s.ID][t.Community] = struct{}{}
		}
		if s.clustered {
			neighborComms[t.ID][s.Community] = struct{}{}
		}
	}
	var bridges []Node
	for _, n := range g.Nodes {
		if isStructural(n) {
			continue
		}
		if len(neighborComms[n.ID]) >= 3 {
			bridges = append(bridges, n)
		}
	}
	sort.Slice(bridges, func(i, j int) bool {
		return len(neighborComms[bridges[i].ID]) > len(neighborComms[bridges[j].ID])
	})
	if len(bridges) > 3 {
		bridges = bridges[:3]
	}
	for _, b := range bridges {
		commCount := len(neighborComms[b.ID])
		gaps = append(gaps, Gap{
			Type:        "bridge-node",
			Title:       "Key bridge: " + b.Title,
			Description: "Connects " + itoa(commCount) + " different knowledge clusters. This is a critical junction in your wiki.",
			NodeIDs:     []string{b.ID},
			Suggestion:  "This page bridges multiple knowledge areas. Ensure it's well-maintained — if it's thin, expanding it will strengthen your entire wiki.",
		})
	}

	if limit > 0 && len(gaps) > limit {
		gaps = gaps[:limit]
	}
	return gaps
}

// internalEdgeCounts returns, per community id, the number of edges
// whose both endpoints are clustered members of that same community.
func internalEdgeCounts(g *Graph, byID map[string]Node) map[int]int {
	out := map[int]int{}
	for _, e := range g.Edges {
		s, sok := byID[e.Source]
		t, tok := byID[e.Target]
		if !sok || !tok {
			continue
		}
		if s.clustered && t.clustered && s.Community == t.Community {
			out[s.Community]++
		}
	}
	return out
}

// ─── helpers ───────────────────────────────────────────────────

func brief(n Node) Brief {
	return Brief{ID: n.ID, Title: n.Title, Type: n.Type}
}

// dismissKey is the stable client-side dismiss key: the two endpoint
// ids sorted and joined so the edge (A,B) and (B,A) collapse to one key.
func dismissKey(a, b string) string {
	if a > b {
		a, b = b, a
	}
	return a + ":::" + b
}

func joinLabels(labels []string) string {
	out := ""
	for i, l := range labels {
		if i > 0 {
			out += ", "
		}
		out += l
	}
	return out
}

func pluralTitle(n int, noun string) string {
	if n > 1 {
		return itoa(n) + " " + noun + "s"
	}
	return itoa(n) + " " + noun
}

func pluralMore(n int) string {
	return " and " + itoa(n) + " more"
}

// itoa / ftoa avoid pulling strconv into this leaf algorithm file for
// just two conversions; the inputs are small and non-negative.

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func ftoa(f float64) string {
	// Two-decimal truncation is all the cohesion display needs.
	whole := int(f)
	frac := int((f - float64(whole)) * 100)
	if frac < 0 {
		frac = -frac
	}
	return itoa(whole) + "." + twoDigits(frac)
}

func twoDigits(n int) string {
	hi := n / 10
	lo := n % 10
	return string([]byte{byte('0' + hi), byte('0' + lo)})
}
