// Radar types — keyword rules + match candidates + hits. Schema is
// in services/app_center/migrations/00006_rss_schema.sql under
// rss.watch_rules + rss.watch_hits.

package radar

import (
	"time"

	"github.com/google/uuid"
)

// Rule is one user-defined keyword rule. The match function lives
// in matcher.go; this struct is a pure value type.
type Rule struct {
	ID           uuid.UUID
	Scope        string // 'user' | 'org'
	ScopeID      string
	Name         string
	MatchAny     []string
	MatchAll     []string
	Exclude      []string
	Sources      []string // '*' | 'rss:<feed_id>' | <board_id>
	OnHitBadge   string   // info | warn | error
	OnHitNotify  []string
	CooldownSec  int
	Enabled      bool
	CreatedAt    time.Time
	UpdatedAt    time.Time

	// M4 — Semantic radar.
	//
	// SemanticQuery is a free-text description of what the user wants
	// to monitor ("任何关于 EU AI 监管的内容"). When set, the matcher
	// derives match terms from the query (M4 v0 = simple tokenize
	// fallback; M5+ swap in cosine(query.embedding, entry.embedding)
	// once the embedding worker lands).
	SemanticQuery     string
	SemanticThreshold float32
	// SemanticEmbedding holds the precomputed query embedding once
	// available. Currently always nil; populated by a future M5
	// embedding worker tick.
	SemanticEmbedding []byte // pgvector binary for transport ergonomics

	// Actions is the JSON-encoded recipe array that fires on hit.
	// dispatcher.Dispatch reads this in order. Each element is
	// {type: 'notify'|'wiki'|'task'|'skill', config: {...}}.
	Actions []byte // raw jsonb
}

// Candidate is one item a fetcher just produced and wants matched
// against rules.
type Candidate struct {
	Source    string // 'rss:<feed_id>' | <board_id>
	Title     string
	URL       string
	TitleHash []byte
	// OwnerScope/OwnerScopeID is set for RSS candidates so we can
	// filter to rules belonging to that scope. Empty for board
	// candidates (boards are global; every user's rules are eligible).
	OwnerScope   string
	OwnerScopeID string
}

// Hit is one (rule, candidate) match that fired (passed cooldown).
type Hit struct {
	ID         int64
	RuleID     uuid.UUID
	HitAt      time.Time
	Source     string
	Title      string
	URL        string
	TitleHash  []byte
	Notified   bool
	ReadAt     time.Time
	// RuleSnapshot is a defensive copy of (Name, OnHitBadge, OnHitNotify)
	// captured at fire time so the dispatcher doesn't need a second DB
	// hit to render the notification.
	RuleSnapshot Rule
}

// Severity ordering for Badge merge.
const (
	SeverityInfo  = "info"
	SeverityWarn  = "warn"
	SeverityError = "error"
)

func severityRank(s string) int {
	switch s {
	case SeverityError:
		return 3
	case SeverityWarn:
		return 2
	case SeverityInfo:
		return 1
	}
	return 0
}

// MaxSeverity returns the higher-rank severity. Used by Badge merge.
func MaxSeverity(a, b string) string {
	if severityRank(a) >= severityRank(b) {
		if a == "" {
			return b
		}
		return a
	}
	return b
}
