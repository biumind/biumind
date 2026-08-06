// Package sidebar persists user-customized sidebar layouts to
// app_center.sidebar_layouts and provides a small CRUD surface to
// the HTTP layer.
//
// Layout shape (matches Design §10A.6):
//
//   {
//     scope:   "desktop" | "mobile",
//     items:   [
//       { kind: "system", ref: "chat",            hidden: false },
//       { kind: "app",    ref: "<install_id>",    badge: true   },
//       ...
//     ],
//     version: 1,
//   }
//
// Optimistic concurrency: every PUT carries expected_version. If the
// row's current version differs (another device wrote in between),
// the call returns ErrVersionConflict and the client refetches.
//
// SECURITY: scope_id == userID is enforced at the SQL layer (the
// PRIMARY KEY) plus by the Authz check at the HTTP boundary; we
// don't re-validate here, but we DO refuse calls without a userID
// because forgetting to thread it would expose someone else's
// layout.

package sidebar

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/biumind/biumind/services/app_center/internal/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ─── Errors ────────────────────────────────────────────────────────

var (
	// ErrVersionConflict — the supplied expected_version doesn't
	// match the current row. Caller GETs to pick up the latest
	// state, then re-attempts.
	ErrVersionConflict = errors.New("sidebar: version conflict")

	// ErrInvalidScope — only "desktop" / "mobile" are accepted.
	ErrInvalidScope = errors.New("sidebar: scope must be desktop or mobile")

	// ErrTooManyItems — guards against pathologically-long layouts
	// (a malicious client trying to wedge 10k entries into the row).
	ErrTooManyItems = errors.New("sidebar: too many items")
)

// MaxItemsDesktop / MaxItemsMobile — hard caps from Design §10A.6.
const (
	MaxItemsDesktop = 30
	MaxItemsMobile  = 20
)

// ─── Domain types ──────────────────────────────────────────────────

// Item is one row in items[]. kind ∈ {system, app}; ref points at a
// system-id (chat / wiki / ...) or an installation_id.
type Item struct {
	Kind   string `json:"kind"` // "system" | "app"
	Ref    string `json:"ref"`  // "wiki" / "<uuid>"
	Hidden bool   `json:"hidden,omitempty"`
	Badge  bool   `json:"badge,omitempty"`
}

// Layout is the full row.
type Layout struct {
	UserID          uuid.UUID `json:"user_id"`
	Scope           string    `json:"scope"` // "desktop" | "mobile"
	Items           []Item    `json:"items"`
	Version         int       `json:"version"`
	UpdatedAt       time.Time `json:"updated_at"`
	UpdatedByDevice string    `json:"updated_by_device"`
}

// ─── Service ───────────────────────────────────────────────────────

type Service struct {
	Pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Service { return &Service{Pool: pool} }

// Get returns the layout, creating an empty one on first read so
// clients can edit-and-PUT without a separate "create" step. The
// returned Version starts at 1.
func (s *Service) Get(ctx context.Context, userID uuid.UUID, scope string) (*Layout, error) {
	if userID == uuid.Nil {
		return nil, errors.New("sidebar: user_id required")
	}
	if scope != "desktop" && scope != "mobile" {
		return nil, ErrInvalidScope
	}
	var (
		itemsRaw []byte
		version  int
		updated  time.Time
		device   string
	)
	err := s.Pool.QueryRow(ctx, `
		SELECT items, version, updated_at, updated_by_device
		  FROM app_center.sidebar_layouts
		 WHERE user_id = $1 AND scope = $2
	`, userID, scope).Scan(&itemsRaw, &version, &updated, &device)
	if errors.Is(err, pgx.ErrNoRows) {
		// No row yet → Version=0 signals "first write" so a subsequent
		// PUT with expected_version=0 is accepted (Put treats 0 as
		// "no prior state, accept any"). Returning Version=1 here
		// would race with Put's currentVersion=0 path and hit
		// ErrVersionConflict on the very first PUT — that's wrong.
		return &Layout{
			UserID:  userID,
			Scope:   scope,
			Items:   []Item{},
			Version: 0,
		}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("sidebar: get: %w", err)
	}

	var items []Item
	if len(itemsRaw) > 0 {
		if err := json.Unmarshal(itemsRaw, &items); err != nil {
			return nil, fmt.Errorf("sidebar: decode items: %w", err)
		}
	}
	return &Layout{
		UserID:          userID,
		Scope:           scope,
		Items:           items,
		Version:         version,
		UpdatedAt:       updated,
		UpdatedByDevice: device,
	}, nil
}

// Put writes the layout. expectedVersion must match the current row
// (or 0 / 1 for the first write); on mismatch returns
// ErrVersionConflict and the row is unchanged. On success the
// returned Layout carries the bumped version.
//
// We UPSERT inside a transaction so the version check + write are
// atomic across concurrent PUTs from the same user's two devices.
//
// device is a free-form identifier for diagnostics ("MacBook-Air");
// surfaced in the "another device just edited" UX.
func (s *Service) Put(ctx context.Context, userID uuid.UUID, scope string, items []Item, expectedVersion int, device string) (*Layout, error) {
	if userID == uuid.Nil {
		return nil, errors.New("sidebar: user_id required")
	}
	if scope != "desktop" && scope != "mobile" {
		return nil, ErrInvalidScope
	}
	maxItems := MaxItemsDesktop
	if scope == "mobile" {
		maxItems = MaxItemsMobile
	}
	if len(items) > maxItems {
		return nil, ErrTooManyItems
	}
	for _, it := range items {
		if it.Kind != "system" && it.Kind != "app" {
			return nil, fmt.Errorf("sidebar: invalid item kind %q", it.Kind)
		}
		if it.Ref == "" {
			return nil, errors.New("sidebar: item ref required")
		}
	}

	itemsJSON, err := json.Marshal(items)
	if err != nil {
		return nil, fmt.Errorf("sidebar: marshal: %w", err)
	}

	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("sidebar: begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	now := time.Now().UTC()

	// Read current version (FOR UPDATE so a concurrent PUT from the
	// same user is serialised — second one sees the bumped version
	// and triggers a conflict).
	var currentVersion int
	err = tx.QueryRow(ctx, `
		SELECT version FROM app_center.sidebar_layouts
		 WHERE user_id = $1 AND scope = $2 FOR UPDATE
	`, userID, scope).Scan(&currentVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		// First write — accept any expected_version.
		currentVersion = 0
	} else if err != nil {
		return nil, fmt.Errorf("sidebar: probe version: %w", err)
	}

	// expected_version=0 disables the check (used by reset / first
	// write). Otherwise must match current.
	if expectedVersion != 0 && expectedVersion != currentVersion {
		return nil, ErrVersionConflict
	}

	newVersion := currentVersion + 1
	if newVersion < 1 {
		newVersion = 1
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO app_center.sidebar_layouts
			(user_id, scope, items, version, updated_at, updated_by_device)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (user_id, scope) DO UPDATE
		   SET items             = EXCLUDED.items,
		       version           = EXCLUDED.version,
		       updated_at        = EXCLUDED.updated_at,
		       updated_by_device = EXCLUDED.updated_by_device
	`, userID, scope, itemsJSON, newVersion, now, device); err != nil {
		return nil, fmt.Errorf("sidebar: upsert: %w", err)
	}

	// Audit. Sidebar events scope on the user (no install or app id
	// is per-row; the layout IS the resource). Realtime fanout
	// reads from app_center.events via the outbox poller / LISTEN
	// trigger and pushes to topic `sidebar:user:<uid>`.
	if err := events.Write(ctx, tx, events.Event{
		ScopeKind: "user",
		ScopeID:   userID.String(),
		ActorType: events.ActorUser,
		ActorID:   userID.String(),
		Type:      events.SidebarLayoutChanged,
		Payload: map[string]any{
			"scope":   scope,
			"version": newVersion,
			"device":  device,
		},
	}); err != nil {
		return nil, fmt.Errorf("sidebar: events: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("sidebar: commit: %w", err)
	}

	return &Layout{
		UserID:          userID,
		Scope:           scope,
		Items:           items,
		Version:         newVersion,
		UpdatedAt:       now,
		UpdatedByDevice: device,
	}, nil
}

// Reset deletes the user's layout for a scope, returning a fresh
// empty (version=1) layout. Sweeps the row rather than UPSERT-ing
// empty so version doesn't keep climbing across resets.
func (s *Service) Reset(ctx context.Context, userID uuid.UUID, scope string, device string) (*Layout, error) {
	if userID == uuid.Nil {
		return nil, errors.New("sidebar: user_id required")
	}
	if scope != "desktop" && scope != "mobile" {
		return nil, ErrInvalidScope
	}
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("sidebar: begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if _, err := tx.Exec(ctx, `
		DELETE FROM app_center.sidebar_layouts
		 WHERE user_id = $1 AND scope = $2
	`, userID, scope); err != nil {
		return nil, fmt.Errorf("sidebar: reset: %w", err)
	}
	if err := events.Write(ctx, tx, events.Event{
		ScopeKind: "user",
		ScopeID:   userID.String(),
		ActorType: events.ActorUser,
		ActorID:   userID.String(),
		Type:      events.SidebarLayoutChanged,
		Payload: map[string]any{
			"scope":  scope,
			"action": "reset",
			"device": device,
		},
	}); err != nil {
		return nil, fmt.Errorf("sidebar: events: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("sidebar: commit: %w", err)
	}
	return &Layout{
		UserID:  userID,
		Scope:   scope,
		Items:   []Item{},
		Version: 1,
	}, nil
}
