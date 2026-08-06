// M11.5 — RSS user preferences.
//
// A single per-user jsonb blob (rss.user_preferences) backs the Settings
// page: default refresh frequency, AI digest budget toggle, notification
// channels, theme, newsletter alias, etc. We read/write the whole object
// — preferences churn with the product, and jsonb means adding a field
// never needs a migration (same reasoning as the client cache storing
// raw payloads).
//
// Preferences are inherently personal — no org scope here.

package rss

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

func (a *App) invokeUserPrefsGet(ctx context.Context, _ json.RawMessage) (any, error) {
	if a.pg == nil {
		return nil, errors.New("rss: pg not wired")
	}
	claims, err := callerClaims(ctx)
	if err != nil {
		return nil, err
	}
	var raw []byte
	err = a.pg.pool.QueryRow(ctx,
		`SELECT config FROM rss.user_preferences WHERE user_id=$1`, claims.UserID).
		Scan(&raw)
	if err != nil {
		// No row yet → empty prefs (not an error; first visit).
		return map[string]any{"config": map[string]any{}}, nil
	}
	var cfg map[string]any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &cfg)
	}
	if cfg == nil {
		cfg = map[string]any{}
	}
	return map[string]any{"config": cfg}, nil
}

func (a *App) invokeUserPrefsUpdate(ctx context.Context, rawIn json.RawMessage) (any, error) {
	if a.pg == nil {
		return nil, errors.New("rss: pg not wired")
	}
	claims, err := callerClaims(ctx)
	if err != nil {
		return nil, err
	}
	var in struct {
		Config map[string]any `json:"config"`
	}
	if err := json.Unmarshal(rawIn, &in); err != nil {
		return nil, fmt.Errorf("rss: bad input: %w", err)
	}
	if in.Config == nil {
		in.Config = map[string]any{}
	}
	cfgJSON, err := json.Marshal(in.Config)
	if err != nil {
		return nil, fmt.Errorf("rss: marshal config: %w", err)
	}
	if _, err := a.pg.pool.Exec(ctx, `
		INSERT INTO rss.user_preferences (user_id, config, updated_at)
		VALUES ($1, $2, now())
		ON CONFLICT (user_id) DO UPDATE
		   SET config = EXCLUDED.config, updated_at = now()`,
		claims.UserID, cfgJSON); err != nil {
		return nil, fmt.Errorf("rss: upsert prefs: %w", err)
	}
	return map[string]any{"ok": true, "config": in.Config}, nil
}
