// End-to-end integration test for M2.8: Cache + Resolver + Vault + Probe
// + Supervisor wired against the dev Postgres. Verifies the lifecycle
// from "client picks Channel A" → "A fails N times, gets auto-disabled"
// → "Cache invalidates, retry routes to Channel B" → "Supervisor probe
// recovers A" → "Cache picks A back up on next request".
//
// This is the M2 graduation gate: when this passes, the full M2 routing
// stack is wired correctly. M3 (admin API) will exercise the same
// components from the HTTP layer; if M2.8 is green, M3 only has to
// worry about transport bugs.

package router

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/biumind/biumind/services/model-relay/internal/health"
	"github.com/biumind/biumind/services/model-relay/internal/keys"
	"github.com/biumind/biumind/services/model-relay/internal/registry"
	"github.com/biumind/biumind/services/model-relay/internal/relay/provider"
	"github.com/biumind/biumind/services/model-relay/internal/relay/provider/openai"
)

func TestE2E_ChannelAutoDisableThenRecover(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping e2e")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgx: %v", err)
	}
	defer pool.Close()

	store := registry.NewStore(pool)

	kek := make([]byte, 32)
	_, _ = rand.Read(kek)
	env, _ := keys.NewEnvelope(kek)
	vault := registry.NewCredentialVault(store.Credentials, env)

	// Stub upstream that we can flip from "ok" to "500" mid-test.
	var mode atomic.Value
	mode.Store("ok")
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if mode.Load().(string) == "500" {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"e2e simulated outage"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "cmpl-e2e", "model": "gpt-4o-mini", "object": "chat.completion",
			"choices": []map[string]any{{
				"index":         0,
				"message":       map[string]any{"role": "assistant", "content": "ok"},
				"finish_reason": "stop",
			}},
			"usage": map[string]any{
				"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2,
			},
		})
	}))
	defer stub.Close()

	// Seed: 1 provider, 1 credential pointing at stub, 1 model with
	// 2 channels (A priority 100, B priority 50).
	pcode := fmt.Sprintf("p_e2e_%d", time.Now().UnixNano())
	prov, err := store.Providers.Insert(ctx, registry.ProviderInput{
		Code: pcode, Name: "E2E", Protocol: registry.ProtocolOpenAICompat,
	})
	if err != nil {
		t.Fatalf("provider: %v", err)
	}
	defer pool.Exec(ctx, "DELETE FROM model_relay.providers WHERE id=$1", prov.ID) //nolint:errcheck

	cred, err := vault.Save(ctx, registry.SaveInput{
		ProviderID: prov.ID, Label: "E2E", Plaintext: "sk-e2e-test-key",
		BaseURL: stub.URL,
	})
	if err != nil {
		t.Fatalf("cred: %v", err)
	}
	defer pool.Exec(ctx, "DELETE FROM model_relay.credentials WHERE id=$1", cred.ID) //nolint:errcheck

	mcode := fmt.Sprintf("m_e2e_%d", time.Now().UnixNano())
	mdl, err := store.Models.Insert(ctx, registry.ModelInput{
		Code: mcode, DisplayName: "M", MinPlan: registry.PlanFree,
		Status: registry.StatusActive,
	})
	if err != nil {
		t.Fatalf("model: %v", err)
	}
	defer pool.Exec(ctx, "DELETE FROM model_relay.models WHERE id=$1", mdl.ID) //nolint:errcheck

	if err := store.Groups.SetModelBindings(ctx, mdl.ID,
		[]uuid.UUID{registry.DefaultGroupID}); err != nil {
		t.Fatalf("bind: %v", err)
	}

	chA, _ := store.Channels.Insert(ctx, registry.ChannelInput{
		ModelID: mdl.ID, CredentialID: cred.ID, UpstreamModel: "primary",
		Priority: 100, Weight: 1, Status: registry.StatusActive,
	})
	defer pool.Exec(ctx, "DELETE FROM model_relay.channels WHERE id=$1", chA.ID) //nolint:errcheck

	chB, _ := store.Channels.Insert(ctx, registry.ChannelInput{
		ModelID: mdl.ID, CredentialID: cred.ID, UpstreamModel: "fallback",
		Priority: 50, Weight: 1, Status: registry.StatusActive,
	})
	defer pool.Exec(ctx, "DELETE FROM model_relay.channels WHERE id=$1", chB.ID) //nolint:errcheck

	// Wire the full stack
	cache := registry.NewCache(store, registry.CacheConfig{TTL: 1 * time.Minute})
	if err := cache.Start(ctx); err != nil {
		t.Fatalf("cache: %v", err)
	}
	defer cache.Close()

	adaptors := provider.NewRegistry()
	adaptors.Register(openai.New())

	probe := health.New(health.Config{
		Store: store, Vault: vault, Adaptors: adaptors,
		Timeout: 1 * time.Second,
	})

	sup := health.NewSupervisor(probe, store, health.SupervisorConfig{
		FailThreshold: 5,
		Cooldown:      1 * time.Microsecond,
		SweepInterval: 100 * time.Millisecond,
		PerSweepLimit: 10,
	})
	sup.Start(ctx)
	defer sup.Close()

	stratReg := NewRegistry()
	stratReg.Register(NewWeighted())
	resolver := NewResolver(cache, vault, stratReg, ResolverConfig{})

	// ─── Phase 1: stub healthy → resolver picks chA (top priority) ───
	out, err := resolver.Resolve(ctx, ResolveInput{
		ModelCode: mcode, UserID: uuid.New(), UserPlan: registry.PlanFree,
	})
	if err != nil {
		t.Fatalf("phase1 resolve: %v", err)
	}
	if out.Channel.ID != chA.ID {
		t.Fatalf("phase1: expected chA, got %s", out.UpstreamModel)
	}

	// ─── Phase 2: stub goes 500 → 5 RecordFailure → chA auto-disabled ───
	mode.Store("500")
	for i := 0; i < 5; i++ {
		_, _, _ = sup.RecordFailure(ctx, chA.ID, errors.New("e2e simulated 500"))
	}

	// R4-B: 翻到 auto_disabled 时会按指数退避写 cooldown_until（第一档
	// 30s）。本测试关心「上游恢复 → sweep 捞回」链路，把 cooldown 拨到
	// 过去，让 100ms 的 sweep 下一轮就能重试 chA。
	if _, err := pool.Exec(ctx,
		"UPDATE model_relay.channels SET cooldown_until = now() - interval '1 second' WHERE id = $1",
		chA.ID); err != nil {
		t.Fatalf("reset cooldown_until: %v", err)
	}

	// Wait for the NOTIFY → cache invalidation to land.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		chans, _ := cache.ChannelsForModel(ctx, mdl.ID)
		if len(chans) == 1 && chans[0].ID == chB.ID {
			break // cache reflects "only chB is active"
		}
		time.Sleep(30 * time.Millisecond)
	}

	out2, err := resolver.Resolve(ctx, ResolveInput{
		ModelCode: mcode, UserID: uuid.New(), UserPlan: registry.PlanFree,
	})
	if err != nil {
		t.Fatalf("phase2 resolve: %v", err)
	}
	if out2.Channel.ID != chB.ID {
		t.Fatalf("phase2: expected fallback to chB after chA disabled, got %s",
			out2.UpstreamModel)
	}

	// ─── Phase 3: stub recovers → supervisor sweep flips chA back ───
	mode.Store("ok")

	// Sweep runs every 100ms; recovery should happen within ~500ms
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		got, _ := store.Channels.Get(ctx, chA.ID)
		if got != nil && got.Status == registry.StatusActive {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	got, _ := store.Channels.Get(ctx, chA.ID)
	if got.Status != registry.StatusActive {
		t.Fatalf("phase3: chA did not recover, status=%s failure_count=%d",
			got.Status, got.FailureCount)
	}

	// Wait for cache to pick up the recovery NOTIFY.
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		chans, _ := cache.ChannelsForModel(ctx, mdl.ID)
		if len(chans) == 2 {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}

	out3, err := resolver.Resolve(ctx, ResolveInput{
		ModelCode: mcode, UserID: uuid.New(), UserPlan: registry.PlanFree,
	})
	if err != nil {
		t.Fatalf("phase3 resolve: %v", err)
	}
	if out3.Channel.ID != chA.ID {
		t.Fatalf("phase3: expected resolver to pick recovered chA, got %s",
			out3.UpstreamModel)
	}
	t.Logf("e2e cycle: chA → 5 fails → chA disabled → chB used → upstream recovers → chA active → chA used")
}
