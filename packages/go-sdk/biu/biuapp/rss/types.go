// BiuMind-side feed / entry models. Distinct from internal/miniflux/model
// to keep the vendor boundary one-directional: fetcher reads miniflux
// model and projects into these; downstream code (store, scheduler,
// matcher) only ever sees these.

package rss

import (
	"time"

	"github.com/google/uuid"
)

type Feed struct {
	ID                  uuid.UUID
	Scope               string
	ScopeID             string
	FeedURL             string
	SiteURL             string
	Title               string
	Description         string
	IconURL             string
	Category            string
	Kind                string // M13.1 source kind: rss/wechat/x/podcast (display badge)
	RefreshSec          int
	Etag                string
	LastModified        string
	LastFetchedAt       time.Time
	LastStatus          string
	LastError           string
	ConsecutiveFailures int
	Enabled             bool
	Forced              bool // M11.4 org admin 强制订阅, 成员不可删
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type Entry struct {
	ID          uuid.UUID
	FeedID      uuid.UUID
	GUID        string
	URL         string
	Title       string
	Author      string
	ContentHTML string
	ContentText string
	PublishedAt time.Time
	FetchedAt   time.Time
	ReadAt      time.Time
	Starred     bool
	Hash        []byte

	// M13.5 — podcast audio enclosure + transcription state.
	EnclosureURL  string
	EnclosureType string
	TranscribedAt time.Time
	// TranscriptSegments — raw jsonb [{id,start,end,text}] for synced
	// playback (Tier2). Empty for non-podcast / un-transcribed entries.
	TranscriptSegments []byte

	// AI digest fields (populated by services/app_center/internal/rss/digest
	// worker after fetch). Empty / 0 when unprocessed.
	AITakeaway     string
	AIBullets      []string
	AITopics       []string
	AIImportance   int     // 1-3
	AILang         string  // 'zh' | 'en'
	AIProcessedAt  time.Time
	AIError        string
	WordCount      int
	ReadingSeconds int
}

func (e *Entry) Unread() bool { return e.ReadAt.IsZero() }
