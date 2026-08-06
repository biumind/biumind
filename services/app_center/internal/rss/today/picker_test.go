package today

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pgvector/pgvector-go"
)

func mkEntry(score float32, importance int, topics []string, age time.Duration, vec []float32) *Entry {
	now := time.Now()
	e := &Entry{
		ID:           uuid.New(),
		FeedID:       uuid.New(),
		Title:        "x",
		AITopics:     topics,
		AIImportance: importance,
		FetchedAt:    now.Add(-age),
		Score:        score,
	}
	if vec != nil {
		v := pgvector.NewVector(vec)
		e.embeddingVec = &v
	}
	return e
}

func TestCosine_OrthogonalAndIdentical(t *testing.T) {
	a := pgvector.NewVector([]float32{1, 0, 0})
	b := pgvector.NewVector([]float32{0, 1, 0})
	c := pgvector.NewVector([]float32{1, 0, 0})
	if got := cosine(&a, &b); got != 0 {
		t.Errorf("orthogonal cos = %f, want 0", got)
	}
	if got := cosine(&a, &c); got < 0.999 {
		t.Errorf("identical cos = %f, want ~1", got)
	}
}

func TestCosine_NilSafe(t *testing.T) {
	a := pgvector.NewVector([]float32{1, 0, 0})
	if cosine(nil, &a) != 0 || cosine(&a, nil) != 0 {
		t.Error("nil input should return 0")
	}
}

func TestScoreEntry_RecencyBonus(t *testing.T) {
	now := time.Now()
	fresh := mkEntry(0, 2, nil, 1*time.Hour, nil)
	old := mkEntry(0, 2, nil, 30*time.Hour, nil)
	sFresh := scoreEntry(fresh, nil, now)
	sOld := scoreEntry(old, nil, now)
	if sFresh <= sOld {
		t.Errorf("fresh should beat old: %f vs %f", sFresh, sOld)
	}
}

func TestScoreEntry_ImportanceWeight(t *testing.T) {
	now := time.Now()
	hi := mkEntry(0, 3, nil, 0, nil)
	lo := mkEntry(0, 1, nil, 0, nil)
	if scoreEntry(hi, nil, now) <= scoreEntry(lo, nil, now) {
		t.Error("higher importance should score higher")
	}
}

func TestClusterByEmbedding_GroupsAndSingletons(t *testing.T) {
	// 3 entries: a + b are near-duplicates (cos > 0.85), c is far.
	a := mkEntry(0.5, 2, nil, 0, []float32{1, 0, 0, 0})
	b := mkEntry(0.4, 1, nil, 0, []float32{0.99, 0.05, 0, 0}) // very close to a
	c := mkEntry(0.3, 1, nil, 0, []float32{0, 1, 0, 0})       // orthogonal
	clusters := clusterByEmbedding([]*Entry{a, b, c}, 0.85)
	if len(clusters) != 2 {
		t.Errorf("clusters = %d, want 2 (one cluster of 2 + one singleton)", len(clusters))
	}
	// Pick top by canonical.Score — should be a (0.5), then c (0.3).
	if clusters[0].canonical.ID != a.ID {
		t.Errorf("top canonical wrong")
	}
	if clusters[0].canonical.ClusterSize != 2 {
		t.Errorf("top cluster size = %d", clusters[0].canonical.ClusterSize)
	}
}

func TestTopClusters_DistinctTopics(t *testing.T) {
	// 4 candidates; top 3 by score share topic AI; 4th is "投资".
	// With distinct-topic gate, expected order: AI canonical, 投资,
	// then backfill with another AI (since N=3 > distinct topics).
	mkSingleton := func(score float32, topic string) cluster {
		e := mkEntry(score, 2, []string{topic}, 0, nil)
		return cluster{canonical: e, members: []*Entry{e}}
	}
	clusters := []cluster{
		mkSingleton(0.9, "AI"),
		mkSingleton(0.8, "AI"),
		mkSingleton(0.7, "AI"),
		mkSingleton(0.6, "投资"),
	}
	top := topClusters(clusters, 3)
	if len(top) != 3 {
		t.Fatalf("len = %d", len(top))
	}
	if top[0].AITopics[0] != "AI" || top[1].AITopics[0] != "投资" {
		t.Errorf("distinct topic gate failed: got %v / %v",
			top[0].AITopics, top[1].AITopics)
	}
	// 3rd backfilled (no more distinct topics → take next AI).
	if top[2].AITopics[0] != "AI" {
		t.Errorf("backfill failed: got %v", top[2].AITopics)
	}
}

func TestTopTrends_OrderAndCap(t *testing.T) {
	entries := []*Entry{
		{AITopics: []string{"AI", "科技"}},
		{AITopics: []string{"AI", "投资"}},
		{AITopics: []string{"投资"}},
		{AITopics: []string{"AI"}},
	}
	out := topTrends(entries, 3)
	if len(out) != 3 {
		t.Fatalf("len = %d", len(out))
	}
	if out[0].Topic != "AI" || out[0].Count != 3 {
		t.Errorf("top1 = %+v", out[0])
	}
	if out[1].Topic != "投资" || out[1].Count != 2 {
		t.Errorf("top2 = %+v", out[1])
	}
}

func TestPicker_Cache(t *testing.T) {
	// Cache key is (user, hour bucket). Two calls in same hour
	// should hit the cache; we verify by storing a sentinel directly.
	p := New(nil)
	now := time.Now().UTC()
	key := "user-1|" + now.Format("2006010215")
	sentinel := &Picks{Headline: []*Entry{{Title: "cached"}}}
	p.cache[key] = cachedEntry{picks: sentinel, expires: now.Add(1 * time.Hour)}
	got, _ := p.PickFor(t.Context(), "user-1")
	if got != sentinel {
		t.Errorf("expected cached sentinel, got %+v", got)
	}
}

func TestPicker_Invalidate(t *testing.T) {
	p := New(nil)
	now := time.Now().UTC()
	key := "user-1|" + now.Format("2006010215")
	p.cache[key] = cachedEntry{picks: &Picks{}, expires: now.Add(1 * time.Hour)}
	p.cache["user-2|"+now.Format("2006010215")] = cachedEntry{picks: &Picks{}, expires: now.Add(1 * time.Hour)}
	p.Invalidate("user-1")
	if _, ok := p.cache[key]; ok {
		t.Error("invalidate failed for user-1")
	}
	if _, ok := p.cache["user-2|"+now.Format("2006010215")]; !ok {
		t.Error("user-2 cache should be intact")
	}
}
