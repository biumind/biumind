// M11.3 — public read-only shared views.
//
// A user can mint an unguessable token for any of their views (Today /
// radar / saved / inbox). The token is rendered by a PUBLIC route
// (GET /share/rss/{token}, see services/app_center/internal/api/share.go)
// with no login — the token IS the auth, the same pattern app_center's
// webhook receiver uses (the HMAC is its auth). Shares carry a 30-day
// default expiry and can be revoked.
//
// We persist the *view definition* (kind + filter + tenant), not a
// snapshot: the public page renders live data each load, so the share
// stays fresh as the source updates.

package rss

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// validShareKinds bounds view_kind so the public renderer only has to
// handle a known set.
var validShareKinds = map[string]bool{
	"today": true, "radar": true, "saved": true, "inbox": true,
}

func newShareToken() (string, error) {
	var b [18]byte // 18 bytes → 24 base64url chars, unguessable
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

func (a *App) invokeSharesCreate(ctx context.Context, raw json.RawMessage) (any, error) {
	if a.pg == nil {
		return nil, errors.New("rss: pg not wired")
	}
	var in struct {
		ViewKind      string          `json:"view_kind"`
		Filter        json.RawMessage `json:"filter,omitempty"`
		ExpiresInDays int             `json:"expires_in_days,omitempty"`
		Scope         string          `json:"scope,omitempty"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, fmt.Errorf("rss: bad input: %w", err)
	}
	if !validShareKinds[in.ViewKind] {
		return nil, fmt.Errorf("rss: unknown view_kind %q", in.ViewKind)
	}
	// Sharing an org view requires org read access (members can share).
	scope, scopeID, err := a.resolveScope(ctx, in.Scope, false)
	if err != nil {
		return nil, err
	}
	claims, _ := callerClaims(ctx)
	days := in.ExpiresInDays
	if days <= 0 {
		days = 30
	}
	filter := in.Filter
	if len(filter) == 0 {
		filter = json.RawMessage(`{}`)
	}
	token, err := newShareToken()
	if err != nil {
		return nil, fmt.Errorf("rss: token gen: %w", err)
	}
	expiresAt := time.Now().Add(time.Duration(days) * 24 * time.Hour)
	if _, err := a.pg.pool.Exec(ctx, `
		INSERT INTO rss.shared_views
		  (token, owner_user_id, owner_org_id, view_kind, filter_json, scope, scope_id, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		token, claims.UserID, claims.OrgID, in.ViewKind, []byte(filter),
		scope, scopeID, expiresAt); err != nil {
		return nil, fmt.Errorf("rss: insert share: %w", err)
	}
	url := "/share/rss/" + token
	if a.shareBaseURL != "" {
		url = strings.TrimRight(a.shareBaseURL, "/") + url
	}
	return map[string]any{
		"token":      token,
		"url":        url,
		"view_kind":  in.ViewKind,
		"expires_at": expiresAt,
	}, nil
}

func (a *App) invokeSharesList(ctx context.Context, _ json.RawMessage) (any, error) {
	if a.pg == nil {
		return nil, errors.New("rss: pg not wired")
	}
	claims, err := callerClaims(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := a.pg.pool.Query(ctx, `
		SELECT token, view_kind, scope, created_at, expires_at, revoked_at
		  FROM rss.shared_views
		 WHERE owner_user_id = $1
		 ORDER BY created_at DESC
		 LIMIT 200`, claims.UserID)
	if err != nil {
		return nil, fmt.Errorf("rss: shares query: %w", err)
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var (
			token, viewKind, scope string
			createdAt, expiresAt   time.Time
			revokedAt              *time.Time
		)
		if err := rows.Scan(&token, &viewKind, &scope, &createdAt, &expiresAt, &revokedAt); err != nil {
			return nil, err
		}
		active := revokedAt == nil && time.Now().Before(expiresAt)
		url := "/share/rss/" + token
		if a.shareBaseURL != "" {
			url = strings.TrimRight(a.shareBaseURL, "/") + url
		}
		items = append(items, map[string]any{
			"token":      token,
			"url":        url,
			"view_kind":  viewKind,
			"scope":      scope,
			"created_at": createdAt,
			"expires_at": expiresAt,
			"active":     active,
		})
	}
	return map[string]any{"items": items}, nil
}

func (a *App) invokeSharesRevoke(ctx context.Context, raw json.RawMessage) (any, error) {
	if a.pg == nil {
		return nil, errors.New("rss: pg not wired")
	}
	var in struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, fmt.Errorf("rss: bad input: %w", err)
	}
	if in.Token == "" {
		return nil, errors.New("rss: token required")
	}
	claims, err := callerClaims(ctx)
	if err != nil {
		return nil, err
	}
	// Scope the revoke to the owner so a user can't revoke another's share.
	tag, err := a.pg.pool.Exec(ctx, `
		UPDATE rss.shared_views
		   SET revoked_at = now()
		 WHERE token = $1 AND owner_user_id = $2 AND revoked_at IS NULL`,
		in.Token, claims.UserID)
	if err != nil {
		return nil, fmt.Errorf("rss: revoke: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrNotFound
	}
	return map[string]any{"ok": true}, nil
}
