// T10.4.3 — 跨设备续读 action 对。位置是「我个人读到哪」,按 caller 的
// user_id 键,与 feed scope(user/org)无关,故直接用 callerScope 取 userID。
//
//   entry_progress_set({entry_id, pct})  — 滚动时节流上报,UPSERT
//   entry_progress_get({entry_id})       — 打开 reader 时取,无记录则 pct=0

package rss

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
)

func (a *App) invokeEntryProgressSet(ctx context.Context, raw json.RawMessage) (any, error) {
	var in struct {
		EntryID string  `json:"entry_id"`
		Pct     float64 `json:"pct"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, fmt.Errorf("rss: bad input: %w", err)
	}
	id, err := uuid.Parse(in.EntryID)
	if err != nil {
		return nil, fmt.Errorf("rss: bad entry id: %w", err)
	}
	_, userID, err := callerScope(ctx)
	if err != nil {
		return nil, err
	}
	if err := a.pg.SetReadingProgress(ctx, userID, id, in.Pct); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func (a *App) invokeEntryProgressGet(ctx context.Context, raw json.RawMessage) (any, error) {
	var in struct {
		EntryID string `json:"entry_id"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, fmt.Errorf("rss: bad input: %w", err)
	}
	id, err := uuid.Parse(in.EntryID)
	if err != nil {
		return nil, fmt.Errorf("rss: bad entry id: %w", err)
	}
	_, userID, err := callerScope(ctx)
	if err != nil {
		return nil, err
	}
	pct, found, err := a.pg.GetReadingProgress(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	return map[string]any{"pct": pct, "found": found}, nil
}
