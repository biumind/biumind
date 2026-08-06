// Adapter implementing the SDK-side rss.BoardsStore interface so the
// rss App can read rankings data without importing this package
// directly. Wired in cmd/app_center/main.go.

package rankings

import (
	"context"
	"errors"

	"github.com/biumind/biumind/packages/go-sdk/biu/biuapp/rss"
)

type SDKAdapter struct {
	Store *Store
}

// Compile-time interface check.
var _ rss.BoardsStore = (*SDKAdapter)(nil)

func (a *SDKAdapter) ListBoards(ctx context.Context) ([]*rss.BoardSummary, error) {
	rows, err := a.Store.ListBoards(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*rss.BoardSummary, len(rows))
	for i, b := range rows {
		out[i] = boardToSDK(b)
	}
	return out, nil
}

func (a *SDKAdapter) GetBoard(ctx context.Context, id string) (*rss.BoardSummary, error) {
	b, err := a.Store.GetBoard(ctx, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, rss.ErrBoardNotFound
		}
		return nil, err
	}
	return boardToSDK(b), nil
}

func (a *SDKAdapter) LatestSnapshot(ctx context.Context, boardID string) (*rss.BoardSnapshot, error) {
	s, err := a.Store.LatestSnapshot(ctx, boardID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, rss.ErrBoardNotFound
		}
		return nil, err
	}
	return snapshotToSDK(s), nil
}

func (a *SDKAdapter) PreviousSnapshot(ctx context.Context, boardID string) (*rss.BoardSnapshot, error) {
	s, err := a.Store.PreviousSnapshot(ctx, boardID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, rss.ErrBoardNotFound
		}
		return nil, err
	}
	return snapshotToSDK(s), nil
}

func (a *SDKAdapter) IsItemNew(ctx context.Context, boardID, title string) (bool, error) {
	return a.Store.IsItemNew(ctx, boardID, title)
}

func boardToSDK(b *Board) *rss.BoardSummary {
	return &rss.BoardSummary{
		ID:            b.ID,
		Name:          b.Name,
		Color:         b.Color,
		Enabled:       b.Enabled,
		RefreshSec:    b.RefreshSec,
		LastFetchedAt: b.LastFetchedAt,
		LastStatus:    b.LastStatus,
		LastError:     b.LastError,
	}
}

func snapshotToSDK(s *StoredSnapshot) *rss.BoardSnapshot {
	items := make([]rss.BoardItem, len(s.Items))
	for i, it := range s.Items {
		items[i] = rss.BoardItem{
			ID: it.ID, Title: it.Title, URL: it.URL,
			MobileURL: it.MobileURL, Extra: it.Extra,
		}
	}
	return &rss.BoardSnapshot{
		BoardID: s.BoardID, CapturedAt: s.CapturedAt, Items: items,
	}
}
