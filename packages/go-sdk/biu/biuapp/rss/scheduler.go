// Refresh scheduler — pulls due feeds, fan-outs across N workers,
// updates fetch state per feed. Runs from the rss App's cron trigger.

package rss

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
)

const (
	defaultConcurrency = 8
	maxBackoffSec      = 86400
)

type Logger interface {
	Debug(msg string, kv ...any)
	Info(msg string, kv ...any)
	Warn(msg string, kv ...any)
	Error(msg string, kv ...any)
}

type discardLogger struct{}

func (discardLogger) Debug(string, ...any) {}
func (discardLogger) Info(string, ...any)  {}
func (discardLogger) Warn(string, ...any)  {}
func (discardLogger) Error(string, ...any) {}

// NewEntry is the SDK-side projection of a freshly-inserted entry,
// passed to OnNew. The radar matcher uses this to produce candidates.
type NewEntry struct {
	FeedID       string
	OwnerScope   string
	OwnerScopeID string
	Title        string
	URL          string
	TitleHash    []byte
}

// SchedulerCallback is invoked once per RefreshAll tick with the
// freshly-inserted entries from all feeds. nil = skip.
type SchedulerCallback func(ctx context.Context, entries []NewEntry)

type Scheduler struct {
	Store       *PGStore
	Fetcher     Fetcher
	Logger      Logger
	Concurrency int
	BatchSize   int
	OnNew       SchedulerCallback
}

func NewScheduler(store *PGStore, fetcher Fetcher) *Scheduler {
	return &Scheduler{
		Store:       store,
		Fetcher:     fetcher,
		Logger:      discardLogger{},
		Concurrency: defaultConcurrency,
		BatchSize:   50,
	}
}

type RefreshStats struct {
	Considered int
	OK         int
	NotMod     int
	Errors     int
	NewEntries int
}

// RefreshAll claims due feeds and fans out workers. Idempotent — if
// nothing is due returns zero counts. Errors on individual feeds are
// logged + folded into the count, never aborting the batch.
func (s *Scheduler) RefreshAll(ctx context.Context) (RefreshStats, error) {
	stats := RefreshStats{}
	if s.Store == nil || s.Fetcher == nil {
		return stats, errors.New("rss: scheduler not wired")
	}
	feeds, err := s.Store.DueFeeds(ctx, s.BatchSize)
	if err != nil {
		return stats, err
	}
	stats.Considered = len(feeds)
	if len(feeds) == 0 {
		return stats, nil
	}
	conc := s.Concurrency
	if conc <= 0 {
		conc = defaultConcurrency
	}

	jobs := make(chan *Feed)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var freshAll []NewEntry
	for i := 0; i < conc; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for f := range jobs {
				outcome, fresh := s.refreshOne(ctx, f)
				mu.Lock()
				switch outcome.Status {
				case "ok":
					stats.OK++
				case "not_modified":
					stats.NotMod++
					outcome.Status = "ok"
				default:
					stats.Errors++
				}
				stats.NewEntries += len(fresh)
				freshAll = append(freshAll, fresh...)
				mu.Unlock()
				_ = s.Store.UpdateFetchState(ctx, f.ID, outcome)
			}
		}()
	}
	for _, f := range feeds {
		select {
		case <-ctx.Done():
		case jobs <- f:
		}
	}
	close(jobs)
	wg.Wait()

	if s.OnNew != nil && len(freshAll) > 0 {
		s.OnNew(ctx, freshAll)
	}
	return stats, nil
}

func (s *Scheduler) refreshOne(ctx context.Context, f *Feed) (FetchOutcome, []NewEntry) {
	start := time.Now()
	// M13.1 — virtual feeds (future newsletter/podcast-alias kinds) carry a
	// non-HTTP feed_url (e.g. mailto:<alias>); there is nothing to fetch, so
	// mark them ok and move on rather than logging a fetch error every tick.
	if !strings.HasPrefix(f.FeedURL, "http://") && !strings.HasPrefix(f.FeedURL, "https://") {
		return FetchOutcome{Status: "ok"}, nil
	}
	res, err := s.Fetcher.Fetch(ctx, FetchRequest{
		FeedURL:      f.FeedURL,
		Etag:         f.Etag,
		LastModified: f.LastModified,
	})
	if err != nil {
		s.Logger.Warn("rss: fetch failed", "feed_id", f.ID, "url", f.FeedURL, "err", err.Error())
		RecordFeedRefresh("error", time.Since(start).Seconds(), 0)
		return FetchOutcome{Status: "error", ErrMsg: err.Error()}, nil
	}
	if res.NotModified {
		s.Logger.Debug("rss: not modified", "feed_id", f.ID)
		RecordFeedRefresh("not_modified", time.Since(start).Seconds(), 0)
		return FetchOutcome{Status: "not_modified"}, nil
	}
	inserted, insErr := s.Store.InsertEntries(ctx, f.ID, res.Entries)
	if insErr != nil {
		s.Logger.Error("rss: insert entries", "feed_id", f.ID, "err", insErr.Error())
		RecordFeedRefresh("error", time.Since(start).Seconds(), 0)
		return FetchOutcome{Status: "error", ErrMsg: insErr.Error()}, nil
	}
	RecordFeedRefresh("ok", time.Since(start).Seconds(), len(inserted))
	fresh := make([]NewEntry, len(inserted))
	feedIDStr := f.ID.String()
	for i, e := range inserted {
		fresh[i] = NewEntry{
			FeedID:       feedIDStr,
			OwnerScope:   f.Scope,
			OwnerScopeID: f.ScopeID,
			Title:        e.Title,
			URL:          e.URL,
			TitleHash:    e.TitleHash,
		}
	}
	s.Logger.Info("rss: fetched", "feed_id", f.ID, "url", f.FeedURL, "new", len(inserted), "total", len(res.Entries))
	return FetchOutcome{
		Status:       "ok",
		Etag:         res.Etag,
		LastModified: res.LastModified,
		Title:        res.Title,
		SiteURL:      res.SiteURL,
		IconURL:      res.IconURL,
	}, fresh
}
