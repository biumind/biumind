// Dedup detection — find pages whose vector embeddings are
// suspiciously close.
//
// The first chunk per page (the title-headed slice produced by
// internal/wiki/chunks chunker) is used as the page's representative
// embedding. That picks up the page's "what's this page about" signal
// strongly while keeping per-detection cost O(K) ANN lookups instead
// of O(N²) full-page comparisons.
//
// Threshold default: cosine similarity ≥ 0.92 (cosine distance ≤ 0.08).
// Below 0.92 false-positive rate climbs steeply on multilingual content;
// above 0.95 we miss real near-duplicates that paraphrase. 0.92 split
// the difference well in llm_wiki dogfood.
//
// We don't auto-merge — every detection becomes an "open" review item
// the user has to accept. P2-D-extended will add an LLM detector pass
// (see knowcode/dedup.py) that classifies the candidate pairs into
// "confidently duplicate" vs "merely related"; until then the cosine
// floor + manual review keeps precision acceptable.
package reviews

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DedupOptions tunes detection. Zero values fall back to safe defaults.
type DedupOptions struct {
	// MaxDistance is cosine_distance threshold; pairs strictly above
	// this are dropped. Default 0.08 (≈ 0.92 similarity).
	MaxDistance float64
	// PerPageNeighbours caps how many ANN neighbours we examine per
	// page. With 0.08 distance most projects produce 0-1 hit per page;
	// the cap protects against pathological hubs (a "table of contents"
	// page that's loosely related to everything).
	PerPageNeighbours int
	// MaxPairsPerProject caps the total pairs written per scan. At
	// the default 50 the user sees a manageable queue; further pairs
	// surface on the next scan after some are resolved.
	MaxPairsPerProject int
}

func (o DedupOptions) withDefaults() DedupOptions {
	if o.MaxDistance <= 0 {
		o.MaxDistance = 0.08
	}
	if o.PerPageNeighbours <= 0 {
		o.PerPageNeighbours = 5
	}
	if o.MaxPairsPerProject <= 0 {
		o.MaxPairsPerProject = 50
	}
	return o
}

// PagePair is one candidate duplicate. PageA / PageB are ordered by
// UUID string so dedupe_key generation is deterministic regardless of
// which side the ANN found first.
//
// SnippetA / SnippetB carry the first chunk's text so the LLM filter
// (P2-D-LLM) can decide "duplicate" vs "merely related" without an
// extra DB round trip per pair. The cosine ANN already had to fetch
// embeddings; tacking text on is one column more in the same row.
type PagePair struct {
	PageA      uuid.UUID
	PageB      uuid.UUID
	TitleA     string
	TitleB     string
	SnippetA   string
	SnippetB   string
	Similarity float64 // 1 - cosine_distance, range [0, 1]
}

// FindDedupCandidates returns up to MaxPairsPerProject pairs of
// non-deleted pages within `projectID` whose first-chunk embeddings
// are within MaxDistance of each other.
//
// The query uses pgvector's ivfflat index for ANN; on small projects
// it falls back to a sequential scan, which is fine.
//
// Why first-chunk and not "average chunk embedding": averaging dilutes
// the topical signal — a page that opens with a sharp topic statement
// then meanders won't match a paraphrased page if we average. The
// chunker pads chunk 0 with the page title so even short pages get a
// usable representative.
func FindDedupCandidates(
	ctx context.Context,
	pool *pgxpool.Pool,
	projectID uuid.UUID,
	opt DedupOptions,
) ([]PagePair, error) {
	opt = opt.withDefaults()

	// First-chunk-per-page CTE. DISTINCT ON (page_id) ORDER BY ord
	// picks the lowest-ord chunk; the chunker emits 0 for the first
	// chunk that carries the title heading.
	//
	// LATERAL JOIN per row asks pgvector for the K nearest neighbours
	// (excluding self). Cosine distance threshold is applied after.
	// Result is symmetric (a-b and b-a both appear), so we filter
	// a.page_id < b.page_id at the top level.
	rows, err := pool.Query(ctx, `
		WITH first_chunks AS (
		  SELECT DISTINCT ON (c.page_id)
		    c.page_id, c.embedding, c.text, p.title
		  FROM brain.wiki_chunks c
		  JOIN brain.pages p ON p.id = c.page_id
		  WHERE c.embedding IS NOT NULL
		    AND c.project_id = $1
		    AND p.deleted_at IS NULL
		  ORDER BY c.page_id, c.ord
		)
		SELECT a.page_id, b.page_id,
		       a.title, b.title,
		       a.text, b.text,
		       1.0 - (a.embedding <=> b.embedding) AS similarity
		  FROM first_chunks a,
		  LATERAL (
		    SELECT page_id, embedding, title, text
		    FROM first_chunks
		    WHERE page_id <> a.page_id
		    ORDER BY embedding <=> a.embedding
		    LIMIT $2
		  ) b
		WHERE a.page_id::text < b.page_id::text
		  AND (a.embedding <=> b.embedding) <= $3
		ORDER BY similarity DESC
		LIMIT $4
	`, projectID, opt.PerPageNeighbours, opt.MaxDistance, opt.MaxPairsPerProject)
	if err != nil {
		return nil, fmt.Errorf("dedup query: %w", err)
	}
	defer rows.Close()

	var out []PagePair
	for rows.Next() {
		var p PagePair
		if err := rows.Scan(&p.PageA, &p.PageB,
			&p.TitleA, &p.TitleB,
			&p.SnippetA, &p.SnippetB,
			&p.Similarity); err != nil {
			return nil, fmt.Errorf("dedup scan: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// DedupKeyForPair generates the canonical dedupe_key for a duplicate
// candidate pair. Order of inputs doesn't matter — the function sorts
// the UUIDs so PageA<->PageB and PageB<->PageA hash to the same key.
func DedupKeyForPair(a, b uuid.UUID) string {
	as := a.String()
	bs := b.String()
	if as > bs {
		as, bs = bs, as
	}
	return "dedup:" + as + ":" + bs
}

// MergeSimilarityThreshold — 相似度 ≥ 此值的 dedup pair 升级为 kind=merge
// （强合并信号，蓝徽章优先于 dedup 琥珀）。高于 FindDedupCandidates 的 dedup
// 召回阈值，即「几乎确定该合并」的子集。
const MergeSimilarityThreshold = 0.92

// WritePairs upserts each pair as a review_item. Similarity ≥
// MergeSimilarityThreshold → kind=merge（强合并信号）；否则 kind=dedup
// （疑似重复）。dedupe_key 统一 pair 级（DedupKeyForPair，不按 kind 分），
// 同一对不会因 kind 切换重复出条目；旧 dedup 高分对由 migration 00068 升级。
func WritePairs(
	ctx context.Context,
	store *Store,
	projectID, ownerID uuid.UUID,
	pairs []PagePair,
	logger *slog.Logger,
) (created, skipped int, err error) {
	for _, p := range pairs {
		key := DedupKeyForPair(p.PageA, p.PageB)
		kind := KindDedup
		if p.Similarity >= MergeSimilarityThreshold {
			kind = KindMerge
		}
		title := fmt.Sprintf("可能重复：%s ↔ %s",
			displayTitle(p.TitleA), displayTitle(p.TitleB))
		desc := fmt.Sprintf(
			"两页的语义相似度为 %.2f，建议人工确认是否合并。",
			p.Similarity)
		_, isNew, uerr := store.Upsert(ctx, UpsertInput{
			ProjectID: projectID, OwnerID: ownerID,
			Kind: kind, Title: title, Description: desc,
			PageIDs:   []uuid.UUID{p.PageA, p.PageB},
			Payload:   map[string]any{"similarity": p.Similarity},
			DedupeKey: key,
		})
		if uerr != nil {
			if logger != nil {
				logger.Warn("dedup upsert failed",
					"project_id", projectID, "key", key, "err", uerr)
			}
			continue
		}
		if isNew {
			created++
		} else {
			skipped++
		}
	}
	return created, skipped, nil
}

func displayTitle(t string) string {
	if t == "" {
		return "(未命名页面)"
	}
	const cap = 60
	if len(t) > cap {
		return t[:cap] + "…"
	}
	return t
}
