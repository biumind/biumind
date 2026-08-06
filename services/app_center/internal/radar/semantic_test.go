package radar

import (
	"context"
	"crypto/rand"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pgvector/pgvector-go"
)

// TestSemanticBatch_RealPG — 端到端验证 cosine 路径:
//   1. 建一条带 semantic_query + semantic_embedding 的 rule
//   2. 注一个 entry+feed (同 scope), 设置一条 "几乎相同" 的 embedding
//   3. SemanticBatch 应该返回这条命中
//   4. 验证 1 - cosine_distance >= threshold
//
// 为了避免真去调 model-relay /v1/embeddings, 测试直接 INSERT 已知的
// vector — 同方向(夹角 0) sim=1.0 大于任何 threshold; 反向 sim=-1.0
// 不可能命中.
func TestSemanticBatch_RealPG(t *testing.T) {
	pool := openDB(t)
	ctx := context.Background()
	scope, scopeID := freshScope(t)

	// 1. rule with semantic embedding (1024 维全 1 = 单位方向, 模拟 "AI 监管")
	v := unit1024(t, 1.0)
	store := NewStore(pool)
	r, err := store.CreateRule(ctx, CreateRuleInput{
		Scope: scope, ScopeID: scopeID, Name: "AI watch",
		SemanticQuery:     "AI 监管",
		SemanticThreshold: 0.5,
		MatchAny:          []string{"placeholder_kw"}, // 防 ErrEmptyRule
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.DeleteRule(ctx, scope, scopeID, r.ID) })

	if _, err := pool.Exec(ctx, `
		UPDATE rss.watch_rules SET semantic_embedding=$2 WHERE id=$1`,
		r.ID, pgvector.NewVector(v)); err != nil {
		t.Fatal(err)
	}

	// 2. feed + entry with the SAME embedding direction (cosine_sim ≈ 1.0)
	feedID := uuid.New()
	_, err = pool.Exec(ctx, `
		INSERT INTO rss.feeds (id, scope, scope_id, feed_url, title, enabled)
		VALUES ($1,$2,$3,$4,$5,true)`,
		feedID, scope, scopeID, "https://test.feed/"+feedID.String(), "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM rss.feeds WHERE id=$1`, feedID) })

	entryID := uuid.New()
	_, err = pool.Exec(ctx, `
		INSERT INTO rss.entries (id, feed_id, guid, title, url, hash, embedding, fetched_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,now())`,
		entryID, feedID, entryID.String(),
		"AI 监管: 欧盟最新动态", "https://e/"+entryID.String(),
		[]byte("h"+entryID.String()), pgvector.NewVector(v))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM rss.entries WHERE id=$1`, entryID)
	})

	// 3. SemanticBatch — should pick up the entry
	hits, err := store.SemanticBatch(ctx, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, h := range hits {
		if h.RuleID == r.ID && h.Title == "AI 监管: 欧盟最新动态" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("cosine match missed; hits=%+v", hits)
	}

	// 4. Negative case: opposite direction → sim=-1.0 < threshold(0.5)
	entryID2 := uuid.New()
	vNeg := unit1024(t, -1.0)
	_, err = pool.Exec(ctx, `
		INSERT INTO rss.entries (id, feed_id, guid, title, url, hash, embedding, fetched_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,now())`,
		entryID2, feedID, entryID2.String(),
		"完全无关的内容", "https://e2/"+entryID2.String(),
		[]byte("h"+entryID2.String()), pgvector.NewVector(vNeg))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM rss.entries WHERE id=$1`, entryID2) })

	hits2, err := store.SemanticBatch(ctx, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hits2 {
		if h.RuleID == r.ID && h.Title == "完全无关的内容" {
			t.Errorf("anti-aligned vector should not match: %+v", h)
		}
	}
}

// unit1024 — 1024 维, 全部填同一个值 (然后 normalize 为单位向量).
// dir=1 朝 +向, -1 朝 -向.
func unit1024(t *testing.T, dir float32) []float32 {
	t.Helper()
	v := make([]float32, 1024)
	// 1/sqrt(1024) ≈ 0.03125
	for i := range v {
		v[i] = dir / 32 // |v| = 1024 * (1/32)^2 → 1.0
	}
	return v
}

// (kept for future: random vector helper, not used for now).
var _ = func() []byte {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return b
}
