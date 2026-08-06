// Boards actions — read-only access to the rankings store. The store
// is provided by the wiring layer (services/app_center) via the
// BoardsStore interface; the SDK doesn't import services packages
// (one-way dependency: services may import SDK, never the reverse).

package rss

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// BoardSummary is the SDK-side projection of a rankings board row.
// Implementations live in services/app_center/internal/rankings.
type BoardSummary struct {
	ID            string
	Name          string
	Color         string
	Enabled       bool
	RefreshSec    int
	LastFetchedAt time.Time
	LastStatus    string
	LastError     string
}

// BoardItem is one item in a board snapshot.
type BoardItem struct {
	ID        string
	Title     string
	URL       string
	MobileURL string
	Extra     map[string]any
}

// BoardSnapshot is the latest captured top-N for a board.
type BoardSnapshot struct {
	BoardID    string
	CapturedAt time.Time
	Items      []BoardItem
}

// BoardsStore is the SDK-side surface for rankings. Wired by app_center.
type BoardsStore interface {
	ListBoards(ctx context.Context) ([]*BoardSummary, error)
	GetBoard(ctx context.Context, id string) (*BoardSummary, error)
	LatestSnapshot(ctx context.Context, boardID string) (*BoardSnapshot, error)
	PreviousSnapshot(ctx context.Context, boardID string) (*BoardSnapshot, error)
	IsItemNew(ctx context.Context, boardID, title string) (bool, error)
}

// ErrBoardNotFound is returned by BoardsStore.GetBoard / LatestSnapshot
// when the board doesn't exist or has no snapshots yet.
var ErrBoardNotFound = errors.New("rss: board not found")

// invokeBoardsList returns all boards with their fetch state.
func (a *App) invokeBoardsList(ctx context.Context, _ json.RawMessage) (any, error) {
	if a.boards == nil {
		return nil, errors.New("rss: rankings store not wired")
	}
	bs, err := a.boards.ListBoards(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]map[string]any, len(bs))
	for i, b := range bs {
		items[i] = boardJSON(b)
	}
	return map[string]any{"items": items}, nil
}

type boardSnapshotInput struct {
	BoardID string `json:"board_id"`
	Limit   int    `json:"limit,omitempty"`
}

// invokeBoardsSnapshot returns the latest snapshot for a board with
// per-item is_new + rank_delta vs the previous snapshot.
func (a *App) invokeBoardsSnapshot(ctx context.Context, raw json.RawMessage) (any, error) {
	if a.boards == nil {
		return nil, errors.New("rss: rankings store not wired")
	}
	var in boardSnapshotInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, fmt.Errorf("rss: bad input: %w", err)
	}
	if in.BoardID == "" {
		return nil, errors.New("rss: board_id required")
	}
	if in.Limit <= 0 || in.Limit > 100 {
		in.Limit = 30
	}

	board, err := a.boards.GetBoard(ctx, in.BoardID)
	if err != nil {
		return nil, err
	}
	latest, err := a.boards.LatestSnapshot(ctx, in.BoardID)
	if err != nil {
		if errors.Is(err, ErrBoardNotFound) {
			return map[string]any{
				"board": boardJSON(board),
				"items": []map[string]any{},
			}, nil
		}
		return nil, err
	}
	prev, _ := a.boards.PreviousSnapshot(ctx, in.BoardID)

	prevRank := map[string]int{}
	if prev != nil {
		for i, it := range prev.Items {
			prevRank[it.Title] = i
		}
	}

	items := make([]map[string]any, 0, len(latest.Items))
	for i, it := range latest.Items {
		if i >= in.Limit {
			break
		}
		isNew, _ := a.boards.IsItemNew(ctx, in.BoardID, it.Title)
		entry := map[string]any{
			"id":         it.ID,
			"title":      it.Title,
			"url":        it.URL,
			"rank":       i + 1,
			"rank_label": "#" + strconv.Itoa(i+1),
			"is_new":     isNew,
		}
		if isNew {
			entry["new_label"] = "🆕 新进榜"
		} else {
			entry["new_label"] = ""
		}
		if it.MobileURL != "" {
			entry["mobile_url"] = it.MobileURL
		}
		if it.Extra != nil {
			entry["extra"] = it.Extra
			// Promote common extras to top-level for easier templating
			if info, ok := it.Extra["info"].(string); ok && info != "" {
				entry["info"] = info
			}
		}
		if oldRank, ok := prevRank[it.Title]; ok {
			delta := oldRank - i
			entry["rank_delta"] = delta
			switch {
			case delta > 0:
				entry["delta_label"] = fmt.Sprintf("↑%d", delta)
			case delta < 0:
				entry["delta_label"] = fmt.Sprintf("↓%d", -delta)
			default:
				entry["delta_label"] = "—"
			}
		} else {
			entry["delta_label"] = ""
		}
		items = append(items, entry)
	}

	return map[string]any{
		"board":       boardJSON(board),
		"items":       items,
		"captured_at": latest.CapturedAt,
	}, nil
}

func boardJSON(b *BoardSummary) map[string]any {
	out := map[string]any{
		"id":          b.ID,
		"name":        b.Name,
		"color":       b.Color,
		"enabled":     b.Enabled,
		"refresh_sec": b.RefreshSec,
		"last_status": b.LastStatus,
	}
	if !b.LastFetchedAt.IsZero() {
		out["last_fetched_at"] = b.LastFetchedAt
	}
	if b.LastError != "" {
		out["last_error"] = b.LastError
	}
	return out
}
