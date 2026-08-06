// Refresh scheduler — pulls due boards, fans out across N workers,
// validates each snapshot's URLs against expected_domain, ingests,
// hands the new-item list to the radar matcher (P2 — for now the
// callback is a no-op when nil).

package rankings

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
)

const defaultConcurrency = 4

// MatcherCallback receives the items detected as "first seen" in the
// current snapshot. P2 wires the radar matcher in here.
type MatcherCallback func(ctx context.Context, items []NewItem)

type Scheduler struct {
	Store       *Store
	Client      *Client
	Logger      *slog.Logger
	Concurrency int
	BatchSize   int
	OnNew       MatcherCallback
}

func NewScheduler(store *Store, client *Client) *Scheduler {
	return &Scheduler{
		Store:       store,
		Client:      client,
		Logger:      slog.Default(),
		Concurrency: defaultConcurrency,
		BatchSize:   50,
	}
}

type RefreshStats struct {
	Considered int
	OK         int
	Warn       int
	Errors     int
	NewItems   int
}

func (s *Scheduler) RefreshAll(ctx context.Context) (RefreshStats, error) {
	stats := RefreshStats{}
	if s.Store == nil || s.Client == nil {
		return stats, errors.New("rankings: scheduler not wired")
	}
	boards, err := s.Store.DueBoards(ctx, s.BatchSize)
	if err != nil {
		return stats, err
	}
	stats.Considered = len(boards)
	if len(boards) == 0 {
		return stats, nil
	}
	conc := s.Concurrency
	if conc <= 0 {
		conc = defaultConcurrency
	}

	jobs := make(chan *Board)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var newItemsAll []NewItem
	for i := 0; i < conc; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for b := range jobs {
				outcome, newItems := s.refreshOne(ctx, b)
				mu.Lock()
				switch outcome.Status {
				case "ok":
					stats.OK++
				case "warn":
					stats.Warn++
				default:
					stats.Errors++
				}
				stats.NewItems += len(newItems)
				newItemsAll = append(newItemsAll, newItems...)
				mu.Unlock()
				_ = s.Store.UpdateFetchState(ctx, b.ID, outcome)
			}
		}()
	}
	for _, b := range boards {
		select {
		case <-ctx.Done():
		case jobs <- b:
		}
	}
	close(jobs)
	wg.Wait()

	if s.OnNew != nil && len(newItemsAll) > 0 {
		s.OnNew(ctx, newItemsAll)
	}
	return stats, nil
}

func (s *Scheduler) refreshOne(ctx context.Context, b *Board) (FetchOutcome, []NewItem) {
	snap, err := s.Client.Fetch(ctx, b.ID)
	if err != nil {
		s.Logger.Warn("rankings: fetch failed", "board", b.ID, "err", err.Error())
		return FetchOutcome{Status: "error", ErrMsg: err.Error()}, nil
	}
	if err := ValidateSnapshot(snap, b.ExpectedDomain); err != nil {
		s.Logger.Warn("rankings: snapshot rejected", "board", b.ID, "err", err.Error())
		return FetchOutcome{Status: "warn", ErrMsg: err.Error()}, nil
	}
	if len(snap.Items) == 0 {
		return FetchOutcome{Status: "ok", ErrMsg: ""}, nil
	}
	newItems, err := s.Store.IngestSnapshot(ctx, snap)
	if err != nil {
		return FetchOutcome{Status: "error", ErrMsg: fmt.Sprintf("ingest: %v", err)}, nil
	}
	s.Logger.Info("rankings: ingested",
		"board", b.ID, "items", len(snap.Items), "new", len(newItems))
	return FetchOutcome{Status: "ok"}, newItems
}
