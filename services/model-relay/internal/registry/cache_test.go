package registry

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestCacheReadThrough verifies the cold-start path: a freshly-built
// Cache loads the underlying tables on first read.
func TestCacheReadThrough(t *testing.T) {
	pool := openDB(t)
	store := NewStore(pool)
	ctx := context.Background()

	// Seed data
	pcode := uniqueCode(t, "p_cache")
	prov, _ := store.Providers.Insert(ctx, ProviderInput{
		Code: pcode, Name: "P", Protocol: ProtocolOpenAICompat,
	})
	defer pool.Exec(ctx, "DELETE FROM model_relay.providers WHERE id=$1", prov.ID) //nolint:errcheck

	cred, _ := store.Credentials.Insert(ctx, CredentialInput{
		ProviderID: prov.ID, Label: "L",
		Ciphertext: []byte("c"), WrappedDEK: []byte("d"), IV: []byte("i"), WrapIV: []byte("w"),
		KeyPreview: "preview", Status: StatusActive,
	})
	defer pool.Exec(ctx, "DELETE FROM model_relay.credentials WHERE id=$1", cred.ID) //nolint:errcheck

	mcode := uniqueCode(t, "m_cache")
	m, _ := store.Models.Insert(ctx, ModelInput{
		Code: mcode, DisplayName: "M", MinPlan: PlanFree, Status: StatusActive,
	})
	defer pool.Exec(ctx, "DELETE FROM model_relay.models WHERE id=$1", m.ID) //nolint:errcheck

	_ = store.Groups.SetModelBindings(ctx, m.ID, []uuid.UUID{DefaultGroupID})

	ch, _ := store.Channels.Insert(ctx, ChannelInput{
		ModelID: m.ID, CredentialID: cred.ID, UpstreamModel: "u",
		Priority: 100, Weight: 1, Status: StatusActive,
	})
	defer pool.Exec(ctx, "DELETE FROM model_relay.channels WHERE id=$1", ch.ID) //nolint:errcheck

	cache := NewCache(store, CacheConfig{TTL: 5 * time.Second})
	if err := cache.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer cache.Close()

	// First read populates the model cache
	got, err := cache.GetModelByCode(ctx, mcode)
	if err != nil || got.ID != m.ID {
		t.Fatalf("get_by_code: %+v err=%v", got, err)
	}

	// Channels for model
	chans, err := cache.ChannelsForModel(ctx, m.ID)
	if err != nil || len(chans) != 1 || chans[0].ID != ch.ID {
		t.Fatalf("channels: %+v err=%v", chans, err)
	}

	// Credential
	gotCred, err := cache.GetCredential(ctx, cred.ID)
	if err != nil || gotCred.KeyPreview != "preview" {
		t.Fatalf("credential: %+v err=%v", gotCred, err)
	}

	// FX rate from seed
	rate, err := cache.FxRate(ctx, CurrencyUSD, CurrencyCNY)
	if err != nil || rate < 6 || rate > 8 {
		t.Fatalf("fx: %v err=%v", rate, err)
	}

	// Self-reflexive short-circuits without DB
	r2, err := cache.FxRate(ctx, CurrencyUSD, CurrencyUSD)
	if err != nil || r2 != 1.0 {
		t.Fatalf("self-fx: %v err=%v", r2, err)
	}

	// Groups for model
	gids, err := cache.GroupsForModel(ctx, m.ID)
	if err != nil || len(gids) != 1 || gids[0] != DefaultGroupID {
		t.Fatalf("groups: %+v err=%v", gids, err)
	}
}

// TestCacheNotifyInvalidation verifies a NOTIFY-driven mutation is
// reflected in the cache's next read. Critical correctness test for
// the LISTEN loop.
func TestCacheNotifyInvalidation(t *testing.T) {
	pool := openDB(t)
	store := NewStore(pool)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	mcode := uniqueCode(t, "m_notify")
	m, _ := store.Models.Insert(ctx, ModelInput{
		Code: mcode, DisplayName: "Original", MinPlan: PlanFree, Status: StatusActive,
	})
	defer pool.Exec(context.Background(), "DELETE FROM model_relay.models WHERE id=$1", m.ID) //nolint:errcheck

	// Long TTL so we KNOW any cache update came from NOTIFY, not TTL
	cache := NewCache(store, CacheConfig{TTL: 5 * time.Minute})
	if err := cache.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer cache.Close()

	// Prime cache
	got, err := cache.GetModelByCode(ctx, mcode)
	if err != nil || got.DisplayName != "Original" {
		t.Fatalf("prime: %+v err=%v", got, err)
	}

	// Mutate via Repo — triggers fire NOTIFY
	_, err = store.Models.Update(ctx, m.ID, ModelInput{
		Code: mcode, DisplayName: "Renamed", MinPlan: PlanFree, Status: StatusActive,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	// Wait up to 1s for the notification to arrive + flip dirty bit.
	// Then a fresh read should see "Renamed".
	deadline := time.Now().Add(1 * time.Second)
	var seen *Model
	for time.Now().Before(deadline) {
		seen, err = cache.GetModelByCode(ctx, mcode)
		if err == nil && seen.DisplayName == "Renamed" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if seen == nil || seen.DisplayName != "Renamed" {
		t.Fatalf("cache did not pick up notify within 1s: got=%+v", seen)
	}
}

// TestCacheTTLFloor verifies the 60s TTL fallback works even when no
// NOTIFY arrives. We use a tiny TTL to keep the test fast.
func TestCacheTTLFloor(t *testing.T) {
	pool := openDB(t)
	store := NewStore(pool)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cache := NewCache(store, CacheConfig{TTL: 200 * time.Millisecond})
	if err := cache.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer cache.Close()

	// Prime fx_rates cache
	_, err := cache.FxRate(ctx, CurrencyUSD, CurrencyCNY)
	if err != nil {
		t.Fatalf("first read: %v", err)
	}

	// Wait past TTL — next read should refresh from DB. Verify by
	// peeking the load-time which should advance.
	cache.mu.RLock()
	loaded1 := cache.fxRatesLoadAt
	cache.mu.RUnlock()

	time.Sleep(300 * time.Millisecond)
	_, _ = cache.FxRate(ctx, CurrencyUSD, CurrencyCNY)

	cache.mu.RLock()
	loaded2 := cache.fxRatesLoadAt
	cache.mu.RUnlock()

	if !loaded2.After(loaded1) {
		t.Fatalf("expected fx_rates to reload past TTL: loaded1=%v loaded2=%v",
			loaded1, loaded2)
	}
}
