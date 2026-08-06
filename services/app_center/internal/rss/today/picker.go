// Package today — picks the user's daily top-N entries.
//
// Inputs:
//   - rss.entries (24h candidates with embeddings + ai_*)
//   - rss.feeds   (engagement_score)
//   - rss.user_interests (centroid + topics)
//   - rss.reading_log (already-engaged exclusion)
//
// Output: a ranked list of clusters (each represented by a canonical
// entry) for the Today view + a "missed" list of high-quality entries
// the user didn't engage with from yesterday + trends list.
//
// The picker is read-only against the DB (no writes); per-user
// (user_id, hour bucket) cache lives in-process for 30 min.
//
// Score:
//   0.4 * engagement_score(feed)        // proven user attention
//   0.3 * cosine(centroid, entry.embed) // personal interest match
//   0.2 * importance/3                  // AI-flagged severity
//   0.1 * recency_bonus                 // 6h-recent bumps +0.1
//
// Cluster: union-find on entries within 24h whose pairwise embedding
// cosine ≥ 0.85 (near-duplicates from different feeds covering the
// same news). Each cluster's canonical = highest-scoring member.

package today

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"
)

const (
	candidateWindow = 24 * time.Hour
	missedWindow    = 48 * time.Hour
	clusterCosTh    = 0.85
	defaultHeadline = 5
	defaultMissed   = 3
	defaultTrends   = 5
	cacheTTL        = 30 * time.Minute
)

type Picks struct {
	Headline    []*Entry  // 5 distinct-cluster top entries
	Missed      []*Entry  // 3 high-importance unread from yesterday
	Trends      []Trend   // top topics this 24h
	Stats       Stats
	GeneratedAt time.Time
}

type Entry struct {
	ID          uuid.UUID
	FeedID      uuid.UUID
	FeedTitle   string
	FeedColor   string
	URL         string
	Title       string
	Author      string
	Snippet     string
	AITakeaway  string
	AIBullets   []string
	AITopics    []string
	AIImportance int
	WordCount   int
	ReadingSec  int
	PublishedAt time.Time
	FetchedAt   time.Time
	Score       float32
	ClusterSize int      // 1 = singleton; ≥ 2 means there are dup sources
	OtherURLs   []string // member urls (other than canonical) for "另 N 个来源"

	// embeddingVec is the entry's pgvector embedding when populated.
	// Lowercase so it never escapes to JSON or external callers; only
	// the picker uses it (cosine + cluster).
	embeddingVec *pgvector.Vector
}

type Trend struct {
	Topic string
	Count int
}

type Stats struct {
	UnreadTotal  int
	ReadToday    int
	StreakDays   int
	WikiThisWeek int
}

type Picker struct {
	Pool *pgxpool.Pool

	cacheMu sync.Mutex
	cache   map[string]cachedEntry
}

type cachedEntry struct {
	picks   *Picks
	expires time.Time
}

func New(pool *pgxpool.Pool) *Picker {
	return &Picker{Pool: pool, cache: map[string]cachedEntry{}}
}

// PickFor computes (or hits cache for) the picks of one user. scope
// is fixed to 'user' for now; org-scope picks land later.
func (p *Picker) PickFor(ctx context.Context, userID string) (*Picks, error) {
	now := time.Now().UTC()
	key := userID + "|" + now.Format("2006010215")
	p.cacheMu.Lock()
	if c, ok := p.cache[key]; ok && now.Before(c.expires) {
		p.cacheMu.Unlock()
		return c.picks, nil
	}
	p.cacheMu.Unlock()

	picks, err := p.compute(ctx, userID, now)
	if err != nil {
		return nil, err
	}
	p.cacheMu.Lock()
	p.cache[key] = cachedEntry{picks: picks, expires: now.Add(cacheTTL)}
	p.cacheMu.Unlock()
	return picks, nil
}

// Invalidate drops the cache for one user (called after mark_read /
// star / wiki / dismiss so the next Today reflects the new state).
func (p *Picker) Invalidate(userID string) {
	p.cacheMu.Lock()
	defer p.cacheMu.Unlock()
	for k := range p.cache {
		if strings.HasPrefix(k, userID+"|") {
			delete(p.cache, k)
		}
	}
}

func (p *Picker) compute(ctx context.Context, userID string, now time.Time) (*Picks, error) {
	centroid, err := p.fetchCentroid(ctx, userID)
	if err != nil {
		return nil, err
	}

	candidates, err := p.candidates(ctx, userID, now.Add(-candidateWindow), now)
	if err != nil {
		return nil, err
	}
	for _, e := range candidates {
		e.Score = scoreEntry(e, centroid, now)
	}

	clusters := clusterByEmbedding(candidates, clusterCosTh)
	headline := topClusters(clusters, defaultHeadline)

	// Missed = NOT engaged + 24-48h ago + score top-N.
	missedCands, err := p.missed(ctx, userID, now.Add(-missedWindow), now.Add(-candidateWindow))
	if err != nil {
		return nil, err
	}
	for _, e := range missedCands {
		e.Score = scoreEntry(e, centroid, now)
	}
	sort.Slice(missedCands, func(i, j int) bool {
		return missedCands[i].Score > missedCands[j].Score
	})
	if len(missedCands) > defaultMissed {
		missedCands = missedCands[:defaultMissed]
	}

	trends := topTrends(candidates, defaultTrends)

	stats, err := p.stats(ctx, userID, now)
	if err != nil {
		return nil, err
	}

	return &Picks{
		Headline:    headline,
		Missed:      missedCands,
		Trends:      trends,
		Stats:       stats,
		GeneratedAt: now,
	}, nil
}

func (p *Picker) fetchCentroid(ctx context.Context, userID string) (*pgvector.Vector, error) {
	row := p.Pool.QueryRow(ctx, `
		SELECT interest_centroid
		  FROM rss.user_interests
		 WHERE user_id = $1`, userID)
	var v *pgvector.Vector
	if err := row.Scan(&v); err != nil {
		// pgx ErrNoRows path or NULL — both fall through to nil-centroid.
		return nil, nil
	}
	return v, nil
}

const candidateCols = `e.id, e.feed_id, COALESCE(f.title, ''), COALESCE(f.icon_url, ''),
	COALESCE(e.url, ''), e.title, COALESCE(e.author, ''),
	COALESCE(NULLIF(e.content_text, ''), e.content_html, '') AS body_for_snippet,
	COALESCE(e.ai_takeaway, ''),
	COALESCE(e.ai_bullets, '[]'::jsonb),
	COALESCE(e.ai_topics, '{}'::text[]),
	e.ai_importance,
	COALESCE(e.word_count, 0),
	COALESCE(e.reading_seconds, 0),
	COALESCE(e.published_at, e.fetched_at),
	e.fetched_at,
	e.embedding`

func (p *Picker) candidates(ctx context.Context, userID string, from, to time.Time) ([]*Entry, error) {
	rows, err := p.Pool.Query(ctx, `
		SELECT `+candidateCols+`
		  FROM rss.entries e
		  JOIN rss.feeds   f ON f.id = e.feed_id
		 WHERE f.scope = 'user' AND f.scope_id = $1
		   AND f.enabled = true
		   AND e.fetched_at >= $2 AND e.fetched_at < $3
		 ORDER BY e.fetched_at DESC
		 LIMIT 200`, userID, from, to)
	if err != nil {
		return nil, fmt.Errorf("today: candidates: %w", err)
	}
	defer rows.Close()
	out := make([]*Entry, 0, 64)
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// missed returns "yesterday's good stuff you skipped" — 24-48h old
// entries the user has zero reading_log events on.
func (p *Picker) missed(ctx context.Context, userID string, from, to time.Time) ([]*Entry, error) {
	rows, err := p.Pool.Query(ctx, `
		SELECT `+candidateCols+`
		  FROM rss.entries e
		  JOIN rss.feeds   f ON f.id = e.feed_id
		 WHERE f.scope = 'user' AND f.scope_id = $1
		   AND f.enabled = true
		   AND e.fetched_at >= $2 AND e.fetched_at < $3
		   AND e.ai_importance >= 2
		   AND NOT EXISTS (
		     SELECT 1 FROM rss.reading_log l
		      WHERE l.user_id = $1 AND l.entry_id = e.id
		   )
		 ORDER BY e.ai_importance DESC, e.fetched_at DESC
		 LIMIT 60`, userID, from, to)
	if err != nil {
		return nil, fmt.Errorf("today: missed: %w", err)
	}
	defer rows.Close()
	out := make([]*Entry, 0, 16)
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (p *Picker) stats(ctx context.Context, userID string, now time.Time) (Stats, error) {
	var s Stats
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	weekStart := dayStart.AddDate(0, 0, -7)

	// Unread total + read today (read_full event count today).
	if err := p.Pool.QueryRow(ctx, `
		WITH owned AS (
		  SELECT e.id, e.read_at FROM rss.entries e
		   JOIN rss.feeds f ON f.id = e.feed_id
		  WHERE f.scope='user' AND f.scope_id=$1 AND f.enabled = true
		)
		SELECT
		  (SELECT COUNT(*) FROM owned WHERE read_at IS NULL) AS unread,
		  (SELECT COUNT(*) FROM rss.reading_log
		    WHERE user_id=$1 AND event='read_full' AND created_at >= $2)
		`, userID, dayStart).Scan(&s.UnreadTotal, &s.ReadToday); err != nil {
		return s, fmt.Errorf("today: stats core: %w", err)
	}

	if err := p.Pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM rss.entry_marks
		 WHERE user_id=$1 AND mark='wiki' AND created_at >= $2`,
		userID, weekStart).Scan(&s.WikiThisWeek); err != nil {
		return s, fmt.Errorf("today: stats wiki: %w", err)
	}

	// Streak — number of consecutive days (ending today) with at least
	// one read_full event. Walk back day-by-day, max 30.
	for d := 0; d < 30; d++ {
		from := dayStart.AddDate(0, 0, -d)
		toExclusive := from.AddDate(0, 0, 1)
		var n int
		err := p.Pool.QueryRow(ctx, `
			SELECT COUNT(*) FROM rss.reading_log
			 WHERE user_id=$1 AND event='read_full'
			   AND created_at >= $2 AND created_at < $3`,
			userID, from, toExclusive).Scan(&n)
		if err != nil {
			return s, fmt.Errorf("today: stats streak: %w", err)
		}
		if n == 0 {
			// "today" with 0 reads doesn't break a streak — only yesterday's
			// missing read does. So allow 0 on the first iteration.
			if d == 0 {
				continue
			}
			break
		}
		s.StreakDays++
	}
	return s, nil
}

// scoreEntry computes the float32 weighted score described in the
// package doc. Returns a 0-1ish number (no normalisation; the absolute
// magnitude doesn't matter — only relative order does).
func scoreEntry(e *Entry, centroid *pgvector.Vector, now time.Time) float32 {
	var s float32

	// Engagement is approximated for v0 by feed_id distinct read_full
	// hits / total entries. We don't pre-compute it on rss.feeds yet
	// (that's a future M3.X enhancement), so engagement contributes a
	// fixed 0.5 baseline. Once the column lands the picker will start
	// to discriminate between "always-read" feeds and "always-skipped".
	s += 0.4 * 0.5

	// Cosine with user centroid (when available).
	if centroid != nil && e.embeddingVec != nil {
		s += 0.3 * cosine(e.embeddingVec, centroid)
	}

	// Importance.
	if e.AIImportance > 0 {
		s += 0.2 * float32(e.AIImportance) / 3.0
	}

	// Recency: 6h ago and earlier → +0.1; older → linear decay to 0.
	age := now.Sub(e.FetchedAt)
	switch {
	case age < 6*time.Hour:
		s += 0.1
	case age < 24*time.Hour:
		s += 0.1 * float32(1.0-(age-6*time.Hour).Hours()/18.0)
	}
	return s
}

func cosine(a, b *pgvector.Vector) float32 {
	if a == nil || b == nil {
		return 0
	}
	av, bv := a.Slice(), b.Slice()
	if len(av) != len(bv) || len(av) == 0 {
		return 0
	}
	var dot, na, nb float64
	for i := range av {
		x, y := float64(av[i]), float64(bv[i])
		dot += x * y
		na += x * x
		nb += y * y
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return float32(dot / (math.Sqrt(na) * math.Sqrt(nb)))
}

// clusterByEmbedding groups entries whose pairwise embedding cosine
// is ≥ threshold. Returns []cluster sorted by best member score desc.
type cluster struct {
	canonical *Entry
	members   []*Entry
}

func clusterByEmbedding(entries []*Entry, threshold float32) []cluster {
	n := len(entries)
	parent := make([]int, n)
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(i int) int {
		if parent[i] != i {
			parent[i] = find(parent[i])
		}
		return parent[i]
	}
	union := func(i, j int) {
		ri, rj := find(i), find(j)
		if ri != rj {
			parent[ri] = rj
		}
	}
	// O(n²) — fine for n ≤ 200 (the candidate cap upstream).
	for i := 0; i < n; i++ {
		if entries[i].embeddingVec == nil {
			continue
		}
		for j := i + 1; j < n; j++ {
			if entries[j].embeddingVec == nil {
				continue
			}
			if cosine(entries[i].embeddingVec, entries[j].embeddingVec) >= threshold {
				union(i, j)
			}
		}
	}
	groups := map[int][]*Entry{}
	for i := range entries {
		r := find(i)
		groups[r] = append(groups[r], entries[i])
	}
	out := make([]cluster, 0, len(groups))
	for _, g := range groups {
		sort.Slice(g, func(i, j int) bool { return g[i].Score > g[j].Score })
		canonical := g[0]
		canonical.ClusterSize = len(g)
		canonical.OtherURLs = make([]string, 0, len(g)-1)
		for _, m := range g[1:] {
			canonical.OtherURLs = append(canonical.OtherURLs, m.URL)
		}
		out = append(out, cluster{canonical: canonical, members: g})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].canonical.Score > out[j].canonical.Score
	})
	return out
}

// topClusters returns the top N canonicals by score, **distinct topic
// label** when topics are populated (so the headline doesn't show 5
// AI articles when "投资 / 设计 / 政策" might be more diverse).
func topClusters(clusters []cluster, n int) []*Entry {
	out := make([]*Entry, 0, n)
	seenTopic := map[string]bool{}
	for _, c := range clusters {
		topic := primaryTopic(c.canonical)
		if topic != "" && seenTopic[topic] {
			continue
		}
		seenTopic[topic] = true
		out = append(out, c.canonical)
		if len(out) >= n {
			break
		}
	}
	// If we filtered too aggressively (eg. 80% same topic), backfill.
	if len(out) < n {
		for _, c := range clusters {
			if len(out) >= n {
				break
			}
			already := false
			for _, e := range out {
				if e.ID == c.canonical.ID {
					already = true
					break
				}
			}
			if !already {
				out = append(out, c.canonical)
			}
		}
	}
	return out
}

func primaryTopic(e *Entry) string {
	if len(e.AITopics) == 0 {
		return ""
	}
	return e.AITopics[0]
}

func topTrends(entries []*Entry, n int) []Trend {
	count := map[string]int{}
	for _, e := range entries {
		for _, t := range e.AITopics {
			if t != "" {
				count[t]++
			}
		}
	}
	pairs := make([]Trend, 0, len(count))
	for k, v := range count {
		pairs = append(pairs, Trend{Topic: k, Count: v})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].Count != pairs[j].Count {
			return pairs[i].Count > pairs[j].Count
		}
		return pairs[i].Topic < pairs[j].Topic
	})
	if len(pairs) > n {
		pairs = pairs[:n]
	}
	return pairs
}
