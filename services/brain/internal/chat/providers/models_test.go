package providers

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestModelUpsertAndList(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	uid := uuid.New()

	cw := 200_000
	pricing := map[string]any{"input_per_m_usd": 5.0, "output_per_m_usd": 25.0}
	abilities := map[string]bool{"vision": true}

	m1, err := s.UpsertModel(ctx, ModelInput{
		UserID:        uid,
		ProviderID:    "anthropic",
		ModelID:       "claude-test",
		DisplayName:   "Claude Test",
		Type:          ModelTypeChat,
		Abilities:     abilities,
		ContextWindow: &cw,
		Pricing:       pricing,
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if m1.DisplayName != "Claude Test" || m1.ContextWindow == nil ||
		*m1.ContextWindow != 200_000 || !m1.Abilities["vision"] {
		t.Errorf("scan mismatch: %+v", m1)
	}

	// Re-upsert with new display name should update, not duplicate.
	m2, err := s.UpsertModel(ctx, ModelInput{
		UserID:      uid,
		ProviderID:  "anthropic",
		ModelID:     "claude-test",
		DisplayName: "Claude Test Renamed",
		Type:        ModelTypeChat,
	})
	if err != nil {
		t.Fatalf("upsert2: %v", err)
	}
	if m2.ID != m1.ID {
		t.Errorf("upsert created duplicate: %s vs %s", m2.ID, m1.ID)
	}
	if m2.DisplayName != "Claude Test Renamed" {
		t.Errorf("display name not updated")
	}

	// List by provider
	rows, err := s.ListModels(ctx, ListModelsInput{
		UserID: uid, ProviderID: "anthropic",
	})
	if err != nil || len(rows) != 1 {
		t.Errorf("list: err=%v len=%d", err, len(rows))
	}

	// Type filter
	t2, _ := s.UpsertModel(ctx, ModelInput{
		UserID: uid, ProviderID: "openai",
		ModelID: "gpt-image-test", Type: ModelTypeImage,
	})
	imgs, _ := s.ListModels(ctx, ListModelsInput{
		UserID: uid, Type: ModelTypeImage,
	})
	if len(imgs) != 1 || imgs[0].ID != t2.ID {
		t.Errorf("image filter: got %d", len(imgs))
	}

	// Update enabled
	disabled := false
	upd, err := s.UpdateModel(ctx, UpdateModelInput{
		UserID: uid, ID: m1.ID, Enabled: &disabled,
	})
	if err != nil || upd.Enabled {
		t.Errorf("update enabled failed: %+v err=%v", upd, err)
	}

	// Filter enabled=true should now exclude m1
	yes := true
	rows2, _ := s.ListModels(ctx, ListModelsInput{
		UserID: uid, ProviderID: "anthropic", Enabled: &yes,
	})
	if len(rows2) != 0 {
		t.Errorf("expected no enabled anthropic rows, got %d", len(rows2))
	}

	// Delete
	if err := s.DeleteModel(ctx, uid, m1.ID); err != nil {
		t.Errorf("delete: %v", err)
	}
	if err := s.DeleteModel(ctx, uid, m1.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("second delete: %v", err)
	}
}

func TestEnsureOfficialIdempotent(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	uid := uuid.New()

	if err := s.EnsureOfficial(ctx, uid); err != nil {
		t.Fatalf("first: %v", err)
	}
	// Second call must not error and must not create a duplicate.
	if err := s.EnsureOfficial(ctx, uid); err != nil {
		t.Fatalf("second: %v", err)
	}
	all, _ := s.List(ctx, uid)
	count := 0
	for _, p := range all {
		if p.ProviderID == "biumind-official" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected 1 official row, got %d", count)
	}
}
