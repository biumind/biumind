// Integration tests against a real Postgres (docker compose).
// Skips when DATABASE_URL is unset.

package providers

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func newTestStore(t *testing.T) (*Store, func()) {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL unset — skipping integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	// P3: Store no longer holds a cipher — keys live in identity.
	return New(pool), pool.Close
}

func TestProviderCRUD(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	uid := uuid.New()

	// Create provider metadata (P3: no key field — credentials live in
	// identity.user_api_keys now).
	created, err := s.Create(ctx, CreateInput{
		UserID:      uid,
		ProviderID:  "anthropic",
		DisplayName: "Anthropic",
		Source:      SourceBuiltin,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.ProviderID != "anthropic" || !created.Enabled {
		t.Errorf("create: unexpected row %+v", created)
	}

	// Duplicate (same user_id + provider_id) → ErrConflict
	_, err = s.Create(ctx, CreateInput{
		UserID:     uid,
		ProviderID: "anthropic",
		Source:     SourceBuiltin,
	})
	if !errors.Is(err, ErrConflict) {
		t.Errorf("expected ErrConflict on duplicate, got %v", err)
	}

	// GetByID
	got, err := s.GetByID(ctx, uid, created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ProviderID != "anthropic" {
		t.Errorf("get: provider mismatch")
	}

	// Cross-tenant get → ErrNotFound (NOT 403)
	_, err = s.GetByID(ctx, uuid.New(), created.ID)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-tenant: expected ErrNotFound, got %v", err)
	}

	// GetByProviderID — agentplane.ResolveBYOKCreds reads metadata here
	// (the actual key now comes from identity, P3).
	byPid, err := s.GetByProviderID(ctx, uid, "anthropic")
	if err != nil {
		t.Fatalf("byPid: %v", err)
	}
	if byPid.ID != created.ID {
		t.Errorf("byPid mismatch")
	}

	// Update display_name + base_url
	newName := "Anthropic (work)"
	base := "https://api.anthropic.com"
	updated, err := s.Update(ctx, UpdateInput{
		UserID:      uid,
		ID:          created.ID,
		DisplayName: &newName,
		BaseURL:     &base,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.DisplayName != newName || updated.BaseURL == nil || *updated.BaseURL != base {
		t.Errorf("update: unexpected row %+v", updated)
	}

	// List — auto-seeds the BiuMind official row, so we expect 2:
	// the explicitly-created one plus 'biumind-official'.
	all, err := s.List(ctx, uid)
	if err != nil || len(all) != 2 {
		t.Errorf("list: err=%v len=%d", err, len(all))
	}
	var officialSeen bool
	for _, p := range all {
		if p.Source == SourceOfficial && p.ProviderID == "biumind-official" {
			officialSeen = true
		}
	}
	if !officialSeen {
		t.Errorf("list missing auto-seeded official provider")
	}

	// Delete
	if err := s.Delete(ctx, uid, created.ID); err != nil {
		t.Errorf("delete: %v", err)
	}
	if err := s.Delete(ctx, uid, created.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("second delete should ErrNotFound, got %v", err)
	}
}
