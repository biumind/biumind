package graph

import "testing"

// TestComputeInsightsEmpty — nil/empty graph never panics and returns
// zero-length slices (not nil) so the JSON encoder emits [].
func TestComputeInsightsEmpty(t *testing.T) {
	for _, g := range []*Graph{nil, {Nodes: nil, Edges: nil}, {Nodes: []Node{}, Edges: []Edge{}}} {
		got := ComputeInsights(g)
		if len(got.Surprising) != 0 || len(got.Gaps) != 0 {
			t.Fatalf("empty graph produced findings: %+v", got)
		}
		if got.Surprising == nil || got.Gaps == nil {
			t.Fatalf("nil slice instead of []: %+v", got)
		}
	}
}

// TestSurprisingCrossCommunity — an edge between two clustered pages
// in different communities scores ≥3 (cross-community bonus alone).
func TestSurprisingCrossCommunity(t *testing.T) {
	g := &Graph{
		Nodes: []Node{
			{ID: "a", Title: "Alpha", Type: "concept", Community: 1, clustered: true, LinkCount: 3},
			{ID: "b", Title: "Beta", Type: "concept", Community: 2, clustered: true, LinkCount: 3},
			{ID: "c", Title: "Gamma", Type: "concept", Community: 1, clustered: true, LinkCount: 1},
		},
		Edges: []Edge{
			{Source: "a", Target: "b", Weight: 1.0}, // cross-community + weak → score 4
			{Source: "a", Target: "c", Weight: 1.0}, // same community → no surprise
		},
	}
	got := findSurprising(g, 5)
	if len(got) == 0 {
		t.Fatalf("want the cross-community a-b edge, got none: %+v", got)
	}
	// a-b is the top scorer (cross-community +3, weak +1 = 4).
	top := got[0]
	if top.Source.ID != "a" || top.Target.ID != "b" {
		t.Fatalf("top surprising should be a-b, got %s-%s", top.Source.ID, top.Target.ID)
	}
	if top.Score < 3 {
		t.Fatalf("score %d below threshold", top.Score)
	}
	foundCross := false
	for _, r := range top.Reasons {
		if r == "crosses community boundary" {
			foundCross = true
		}
	}
	if !foundCross {
		t.Fatalf("a-b missing cross-community reason: %v", top.Reasons)
	}
	// dismiss key is order-independent.
	if top.Key != "a:::b" {
		t.Fatalf("dismiss key %q want a:::b", top.Key)
	}
}

// TestStructuralExcluded — index/log/overview + type overview never
// appear in findings even if they're high-degree hubs.
func TestStructuralExcluded(t *testing.T) {
	g := &Graph{
		Nodes: []Node{
			{ID: "idx", Title: "Index", Type: "", Community: 1, clustered: true, LinkCount: 10},
			{ID: "ov", Title: "Overview Page", Type: "overview", Community: 2, clustered: true, LinkCount: 10},
			{ID: "a", Title: "Alpha", Type: "concept", Community: 3, clustered: true, LinkCount: 1},
			{ID: "b", Title: "Beta", Type: "concept", Community: 4, clustered: true, LinkCount: 1},
		},
		Edges: []Edge{
			{Source: "idx", Target: "a", Weight: 1.0},
			{Source: "ov", Target: "b", Weight: 1.0},
			{Source: "a", Target: "b", Weight: 1.0}, // both non-structural, cross-community
		},
	}
	got := findSurprising(g, 5)
	for _, s := range got {
		if s.Source.ID == "idx" || s.Source.ID == "ov" || s.Target.ID == "idx" || s.Target.ID == "ov" {
			t.Fatalf("structural page leaked into surprising: %+v", s)
		}
	}
}

// TestGapIsolatedNode — degree-0/1 non-structural page flags isolated.
func TestGapIsolatedNode(t *testing.T) {
	g := &Graph{
		Nodes: []Node{
			{ID: "a", Title: "Alpha", Type: "concept", Community: 1, clustered: true, LinkCount: 0},
			{ID: "b", Title: "Beta", Type: "concept", Community: 1, clustered: true, LinkCount: 5},
			{ID: "c", Title: "Gamma", Type: "concept", Community: 1, clustered: true, LinkCount: 5},
		},
		Edges: []Edge{{Source: "b", Target: "c", Weight: 3.0}},
	}
	gaps := detectGaps(g, 8)
	var isolated *Gap
	for i := range gaps {
		if gaps[i].Type == "isolated-node" {
			isolated = &gaps[i]
			break
		}
	}
	if isolated == nil {
		t.Fatalf("no isolated-node gap; got %+v", gaps)
	}
	if len(isolated.NodeIDs) != 1 || isolated.NodeIDs[0] != "a" {
		t.Fatalf("isolated should be [a], got %v", isolated.NodeIDs)
	}
}

// TestGapSparseCommunity — a 3-node community with 1 internal edge has
// cohesion 1/3 ≈ 0.33... that's ≥0.15, so NOT sparse. Drive cohesion
// below 0.15 with 4 nodes + 1 edge → 1/6 ≈ 0.17 still ≥0.15. Use 4
// nodes 0 internal edges → cohesion 0 < 0.15 → sparse.
func TestGapSparseCommunity(t *testing.T) {
	g := &Graph{
		Nodes: []Node{
			{ID: "a", Title: "Alpha", Type: "concept", Community: 1, clustered: true, LinkCount: 1},
			{ID: "b", Title: "Beta", Type: "concept", Community: 1, clustered: true, LinkCount: 1},
			{ID: "c", Title: "Gamma", Type: "concept", Community: 1, clustered: true, LinkCount: 1},
			{ID: "d", Title: "Delta", Type: "concept", Community: 1, clustered: true, LinkCount: 0},
		},
		// One internal edge a-b: cohesion = 1 / (4*3/2) = 1/6 ≈ 0.167 ≥ 0.15 → not sparse.
		// Remove it to get cohesion 0. We keep a connected to an outside node so
		// a/b/c don't all collapse into isolated-node only.
		Edges: []Edge{
			{Source: "a", Target: "x", Weight: 2.0}, // x not in community
		},
	}
	// Add an outside node so the edge resolves.
	g.Nodes = append(g.Nodes, Node{ID: "x", Title: "X", Type: "concept", Community: 2, clustered: true, LinkCount: 1})
	gaps := detectGaps(g, 8)
	var sparse *Gap
	for i := range gaps {
		if gaps[i].Type == "sparse-community" {
			sparse = &gaps[i]
			break
		}
	}
	if sparse == nil {
		t.Fatalf("no sparse-community gap; got %+v", gaps)
	}
}

// TestGapBridgeNode — a node whose neighbours span ≥3 communities.
func TestGapBridgeNode(t *testing.T) {
	g := &Graph{
		Nodes: []Node{
			{ID: "hub", Title: "Hub", Type: "concept", Community: 1, clustered: true, LinkCount: 3},
			{ID: "a", Title: "A", Type: "concept", Community: 2, clustered: true, LinkCount: 1},
			{ID: "b", Title: "B", Type: "concept", Community: 3, clustered: true, LinkCount: 1},
			{ID: "c", Title: "C", Type: "concept", Community: 4, clustered: true, LinkCount: 1},
		},
		Edges: []Edge{
			{Source: "hub", Target: "a", Weight: 2.0},
			{Source: "hub", Target: "b", Weight: 2.0},
			{Source: "hub", Target: "c", Weight: 2.0},
		},
	}
	gaps := detectGaps(g, 8)
	var bridge *Gap
	for i := range gaps {
		if gaps[i].Type == "bridge-node" {
			bridge = &gaps[i]
			break
		}
	}
	if bridge == nil {
		t.Fatalf("no bridge-node gap; got %+v", gaps)
	}
	if bridge.NodeIDs[0] != "hub" {
		t.Fatalf("bridge should be hub, got %v", bridge.NodeIDs)
	}
}

// TestStatsCommunityCount — unclustered pages don't count as a community.
func TestStatsCommunityCount(t *testing.T) {
	g := &Graph{
		Nodes: []Node{
			{ID: "a", Community: 1, clustered: true},
			{ID: "b", Community: 1, clustered: true},
			{ID: "c", Community: 2, clustered: true},
			{ID: "d", clustered: false}, // unclustered
		},
	}
	got := ComputeInsights(g)
	if got.Stats.CommunityCount != 2 {
		t.Fatalf("community count %d want 2", got.Stats.CommunityCount)
	}
	if got.Stats.NodeCount != 4 {
		t.Fatalf("node count %d want 4", got.Stats.NodeCount)
	}
}

func TestDismissKeyOrderIndependent(t *testing.T) {
	if dismissKey("a", "b") != dismissKey("b", "a") {
		t.Fatal("dismiss key must be order-independent")
	}
}

func TestItoaFtoa(t *testing.T) {
	if itoa(0) != "0" || itoa(42) != "42" {
		t.Fatalf("itoa broken: %q %q", itoa(0), itoa(42))
	}
	if ftoa(0.167) != "0.16" && ftoa(0.167) != "0.17" {
		// truncation → 0.16; either acceptable tolerance documented here.
		t.Fatalf("ftoa(0.167)=%q", ftoa(0.167))
	}
}

// ─── structured reason codes (client localization) ─────────────

func reasonCodes(s Surprising) []string {
	out := make([]string, 0, len(s.ReasonDetails))
	for _, r := range s.ReasonDetails {
		out = append(out, r.Code)
	}
	return out
}

// TestReasonDetailsAlignWithReasons — ReasonDetails[i] is the structured
// form of Reasons[i]; clients zip them for per-reason fallback.
func TestReasonDetailsAlignWithReasons(t *testing.T) {
	g := &Graph{
		Nodes: []Node{
			{ID: "a", Title: "Alpha", Type: "source", Community: 1, clustered: true, LinkCount: 5},
			{ID: "b", Title: "Beta", Type: "concept", Community: 2, clustered: true, LinkCount: 1},
		},
		Edges: []Edge{{Source: "a", Target: "b", Weight: 1.0}},
	}
	got := findSurprising(g, 5)
	if len(got) != 1 {
		t.Fatalf("want 1 surprising edge, got %+v", got)
	}
	s := got[0]
	if len(s.ReasonDetails) != len(s.Reasons) {
		t.Fatalf("details %d != reasons %d: %+v", len(s.ReasonDetails), len(s.Reasons), s)
	}
	// source-concept is a distant pair (+2), cross-community +3,
	// periphery↔hub +2 (minDeg 1 ≤ 2, maxDeg 5 ≥ 5*0.5), weak +1.
	want := []string{"cross_community", "cross_type", "periphery_hub", "weak_edge"}
	codes := reasonCodes(s)
	if len(codes) != len(want) {
		t.Fatalf("codes %v want %v", codes, want)
	}
	for i, w := range want {
		if codes[i] != w {
			t.Fatalf("codes %v want %v", codes, want)
		}
	}
	// Params per code.
	cc := s.ReasonDetails[0].Params
	if cc["source_community"] != 1 || cc["target_community"] != 2 {
		t.Fatalf("cross_community params %v", cc)
	}
	ct := s.ReasonDetails[1].Params
	if ct["source_type"] != "source" || ct["target_type"] != "concept" || ct["distant"] != true {
		t.Fatalf("cross_type params %v", ct)
	}
	ph := s.ReasonDetails[2].Params
	if ph["min_degree"] != 1 || ph["max_degree"] != 5 || ph["graph_max_degree"] != 5 {
		t.Fatalf("periphery_hub params %v", ph)
	}
	we := s.ReasonDetails[3].Params
	if we["weight"] != float32(1.0) {
		t.Fatalf("weak_edge params %v", we)
	}
}

// TestCrossTypeGeneric — same-distant=false for a non-distant type pair.
func TestCrossTypeGeneric(t *testing.T) {
	g := &Graph{
		Nodes: []Node{
			{ID: "a", Title: "Alpha", Type: "concept", Community: 1, clustered: true, LinkCount: 3},
			{ID: "b", Title: "Beta", Type: "entity", Community: 2, clustered: true, LinkCount: 3},
		},
		Edges: []Edge{{Source: "a", Target: "b", Weight: 3.0}}, // not weak
	}
	got := findSurprising(g, 5)
	if len(got) != 1 {
		t.Fatalf("want 1 surprising edge, got %+v", got)
	}
	for _, r := range got[0].ReasonDetails {
		if r.Code == "cross_type" {
			if r.Params["distant"] != false {
				t.Fatalf("concept-entity should not be distant: %v", r.Params)
			}
			return
		}
	}
	t.Fatalf("missing cross_type detail: %+v", got[0].ReasonDetails)
}

// TestGapReasonCodes — every gap carries reason_code + params matching its
// legacy kebab-case type.
func TestGapReasonCodes(t *testing.T) {
	g := &Graph{
		Nodes: []Node{
			{ID: "iso", Title: "Lonely", Type: "concept", Community: 1, clustered: true, LinkCount: 0},
			{ID: "s1", Title: "S1", Type: "concept", Community: 2, clustered: true, LinkCount: 0},
			{ID: "s2", Title: "S2", Type: "concept", Community: 2, clustered: true, LinkCount: 0},
			{ID: "s3", Title: "S3", Type: "concept", Community: 2, clustered: true, LinkCount: 0},
			{ID: "hub", Title: "Hub", Type: "concept", Community: 3, clustered: true, LinkCount: 3},
			{ID: "b1", Title: "B1", Type: "concept", Community: 4, clustered: true, LinkCount: 2},
			{ID: "b2", Title: "B2", Type: "concept", Community: 5, clustered: true, LinkCount: 2},
			{ID: "b3", Title: "B3", Type: "concept", Community: 6, clustered: true, LinkCount: 2},
		},
		Edges: []Edge{
			{Source: "hub", Target: "b1", Weight: 2.0},
			{Source: "hub", Target: "b2", Weight: 2.0},
			{Source: "hub", Target: "b3", Weight: 2.0},
		},
	}
	gaps := detectGaps(g, 8)
	byCode := map[string]Gap{}
	for _, gap := range gaps {
		if gap.ReasonCode == "" {
			t.Fatalf("gap %q missing reason_code", gap.Type)
		}
		byCode[gap.ReasonCode] = gap
	}
	// orphan_page ← isolated-node.
	orphan, ok := byCode["orphan_page"]
	if !ok || orphan.Type != "isolated-node" {
		t.Fatalf("orphan_page gap missing/mismatched: %+v", gaps)
	}
	if orphan.Params["count"] != 4 { // iso + s1 + s2 + s3 all degree ≤ 1
		t.Fatalf("orphan count param %v", orphan.Params["count"])
	}
	// sparse_community ← sparse-community (community 2: 3 nodes, 0 internal edges).
	sparse, ok := byCode["sparse_community"]
	if !ok || sparse.Type != "sparse-community" {
		t.Fatalf("sparse_community gap missing/mismatched: %+v", gaps)
	}
	if sparse.Params["community_id"] != 2 || sparse.Params["member_count"] != 3 {
		t.Fatalf("sparse params %v", sparse.Params)
	}
	if cohesion, ok := sparse.Params["cohesion"].(float64); !ok || cohesion >= 0.15 {
		t.Fatalf("sparse cohesion param %v", sparse.Params["cohesion"])
	}
	// bridge_node ← bridge-node (hub spans communities 4/5/6).
	bridge, ok := byCode["bridge_node"]
	if !ok || bridge.Type != "bridge-node" {
		t.Fatalf("bridge_node gap missing/mismatched: %+v", gaps)
	}
	if bridge.Params["node_id"] != "hub" || bridge.Params["community_count"] != 3 {
		t.Fatalf("bridge params %v", bridge.Params)
	}
}
