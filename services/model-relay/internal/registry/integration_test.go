// Integration tests for the registry package. They run against a real
// Postgres with the model_relay migrations applied. Skip when
// DATABASE_URL is unset, mirroring services/app_center/internal/installs.
//
// The tests are written so each can stand alone — every test seeds its
// own provider/credential/model rows with unique codes and tears them
// down at the end. Parallel-safe because uuid PKs and unique-coded
// rows don't collide.

package registry

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func openDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping registry integration tests")
	}
	p, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(p.Close)

	// Verify model_relay migrations have been applied.
	for _, table := range []string{
		"model_relay.providers",
		"model_relay.credentials",
		"model_relay.models",
		"model_relay.channels",
		"model_relay.pricing",
		"model_relay.fx_rates",
		"model_relay.model_groups",
		"model_relay.model_group_bindings",
		"model_relay.usage_log",
	} {
		var exists bool
		if err := p.QueryRow(context.Background(),
			`SELECT EXISTS (SELECT 1 FROM information_schema.tables
			    WHERE table_schema = split_part($1, '.', 1)
			      AND table_name = split_part($1, '.', 2))`,
			table,
		).Scan(&exists); err != nil {
			t.Fatalf("table check %s: %v", table, err)
		}
		if !exists {
			t.Skipf("%s missing; apply services/model-relay/migrations", table)
		}
	}
	return p
}

// uniqueCode returns a per-test code suffix so tests can run in parallel
// and re-runs don't collide on the unique constraint.
func uniqueCode(t *testing.T, prefix string) string {
	t.Helper()
	return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
}

func TestProvidersCRUD(t *testing.T) {
	pool := openDB(t)
	store := NewStore(pool)
	ctx := context.Background()

	code := uniqueCode(t, "test_openai")
	defer pool.Exec(ctx, "DELETE FROM model_relay.providers WHERE code=$1", code) //nolint:errcheck

	p, err := store.Providers.Insert(ctx, ProviderInput{
		Code:     code,
		Name:     "Test OpenAI",
		Protocol: ProtocolOpenAICompat,
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if p.ID == uuid.Nil || p.Status != StatusActive {
		t.Fatalf("unexpected: %+v", p)
	}

	// Conflict on duplicate code
	_, err = store.Providers.Insert(ctx, ProviderInput{
		Code:     code,
		Name:     "dup",
		Protocol: ProtocolOpenAICompat,
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("expected conflict, got %v", err)
	}

	got, err := store.Providers.Get(ctx, p.ID)
	if err != nil || got.Code != code {
		t.Fatalf("get: %+v err=%v", got, err)
	}

	got2, err := store.Providers.GetByCode(ctx, code)
	if err != nil || got2.ID != p.ID {
		t.Fatalf("get_by_code: %+v err=%v", got2, err)
	}

	updated, err := store.Providers.Update(ctx, p.ID, ProviderInput{
		Code:        code,
		Name:        "Renamed",
		Protocol:    ProtocolOpenAICompat,
		Description: "desc",
		Status:      StatusDisabled,
	})
	if err != nil || updated.Name != "Renamed" || updated.Status != StatusDisabled {
		t.Fatalf("update: %+v err=%v", updated, err)
	}

	if err := store.Providers.Delete(ctx, p.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := store.Providers.Get(ctx, p.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected not_found post-delete, got %v", err)
	}
}

func TestCredentialsCRUD_NoEnvelopeYet(t *testing.T) {
	pool := openDB(t)
	store := NewStore(pool)
	ctx := context.Background()

	pcode := uniqueCode(t, "anthropic_t")
	provider, err := store.Providers.Insert(ctx, ProviderInput{
		Code: pcode, Name: "Anthropic Test", Protocol: ProtocolAnthropic,
	})
	if err != nil {
		t.Fatalf("provider insert: %v", err)
	}
	defer pool.Exec(ctx, "DELETE FROM model_relay.providers WHERE id=$1", provider.ID) //nolint:errcheck

	// Repo doesn't enforce envelope crypto — just stores bytes verbatim.
	// M2.3 will add the higher-level Save() that runs envelope.Encrypt
	// before this insert.
	cred, err := store.Credentials.Insert(ctx, CredentialInput{
		ProviderID:     provider.ID,
		Label:          "Main account",
		Ciphertext:     []byte("dummy_ciphertext"),
		WrappedDEK:     []byte("dummy_wrapped"),
		IV:             []byte("dummy_iv___"),
		WrapIV:         []byte("dummy_wiv__"),
		KeyPreview:     "sk-...abc1",
		BaseURL:        "https://api.anthropic.com",
		HeaderOverride: map[string]string{"X-Custom": "v1"},
	})
	if err != nil {
		t.Fatalf("credential insert: %v", err)
	}
	defer pool.Exec(ctx, "DELETE FROM model_relay.credentials WHERE id=$1", cred.ID) //nolint:errcheck

	if cred.KeyPreview != "sk-...abc1" {
		t.Fatalf("preview lost: %q", cred.KeyPreview)
	}
	if cred.HeaderOverride["X-Custom"] != "v1" {
		t.Fatalf("header roundtrip lost: %v", cred.HeaderOverride)
	}

	// SafeView must scrub envelope bytes
	safe := NewCredentialSafe(cred)
	if safe.KeyPreview != "sk-...abc1" {
		t.Fatalf("safe preview lost")
	}

	// ListSafe should return scrubbed
	safes, err := store.Credentials.ListSafe(ctx, CredentialFilter{ProviderID: provider.ID})
	if err != nil || len(safes) != 1 {
		t.Fatalf("list_safe: %d %v", len(safes), err)
	}

	// Update label only (no rotation)
	upd, err := store.Credentials.Update(ctx, cred.ID, CredentialUpdate{
		Label:          "Renamed",
		BaseURL:        cred.BaseURL,
		HeaderOverride: cred.HeaderOverride,
		Status:         StatusActive,
	})
	if err != nil || upd.Label != "Renamed" {
		t.Fatalf("update: %+v err=%v", upd, err)
	}
	// Ciphertext should be unchanged
	if string(upd.Ciphertext) != "dummy_ciphertext" {
		t.Fatalf("ciphertext mutated on label-only update")
	}

	// Test result patch
	if err := store.Credentials.PatchTestResult(ctx, cred.ID, "", true); err != nil {
		t.Fatalf("patch test result: %v", err)
	}
	got, _ := store.Credentials.Get(ctx, cred.ID)
	if got.LastTestAt == nil || got.LastTestError != "" {
		t.Fatalf("test stamp lost: %+v", got)
	}
}

func TestModelsAndGroupBindings(t *testing.T) {
	pool := openDB(t)
	store := NewStore(pool)
	ctx := context.Background()

	mcode := uniqueCode(t, "test-claude-sonnet")
	m, err := store.Models.Insert(ctx, ModelInput{
		Code:          mcode,
		DisplayName:   "Test Sonnet",
		Family:        "claude",
		ContextWindow: 200_000,
		MaxOutput:     8192,
		Capabilities:  Capabilities{Vision: true, Tools: true, Cache: true},
		MinPlan:       PlanPro,
		Status:        StatusActive,
	})
	if err != nil {
		t.Fatalf("model insert: %v", err)
	}
	defer pool.Exec(ctx, "DELETE FROM model_relay.models WHERE id=$1", m.ID) //nolint:errcheck

	if !m.Capabilities.Vision || !m.Capabilities.Tools || !m.Capabilities.Cache {
		t.Fatalf("capabilities roundtrip lost: %+v", m.Capabilities)
	}

	// Bind to default group
	if err := store.Groups.SetModelBindings(ctx, m.ID, []uuid.UUID{DefaultGroupID}); err != nil {
		t.Fatalf("bind: %v", err)
	}

	groups, err := store.Groups.ListGroupsForModel(ctx, m.ID)
	if err != nil || len(groups) != 1 || groups[0].ID != DefaultGroupID {
		t.Fatalf("groups for model: %+v err=%v", groups, err)
	}

	// Visible to pro user → should include this model
	visible, err := store.Models.ListVisibleTo(ctx, PlanPro, []uuid.UUID{DefaultGroupID})
	if err != nil {
		t.Fatalf("list visible: %v", err)
	}
	found := false
	for _, mm := range visible {
		if mm.ID == m.ID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected pro model to be visible to pro user")
	}

	// Free user should NOT see pro model
	visibleFree, err := store.Models.ListVisibleTo(ctx, PlanFree, []uuid.UUID{DefaultGroupID})
	if err != nil {
		t.Fatalf("list visible free: %v", err)
	}
	for _, mm := range visibleFree {
		if mm.ID == m.ID {
			t.Fatalf("free user should not see pro model %s", mm.Code)
		}
	}
}

func TestChannelsAutoDisable(t *testing.T) {
	pool := openDB(t)
	store := NewStore(pool)
	ctx := context.Background()

	// Setup: provider + credential + model
	pcode := uniqueCode(t, "p_autoban")
	prov, _ := store.Providers.Insert(ctx, ProviderInput{
		Code: pcode, Name: "P", Protocol: ProtocolOpenAICompat,
	})
	defer pool.Exec(ctx, "DELETE FROM model_relay.providers WHERE id=$1", prov.ID) //nolint:errcheck

	cred, _ := store.Credentials.Insert(ctx, CredentialInput{
		ProviderID: prov.ID, Label: "L",
		Ciphertext: []byte("c"), WrappedDEK: []byte("d"), IV: []byte("i"), WrapIV: []byte("w"),
		KeyPreview: "sk-x",
	})
	defer pool.Exec(ctx, "DELETE FROM model_relay.credentials WHERE id=$1", cred.ID) //nolint:errcheck

	mcode := uniqueCode(t, "m_autoban")
	m, _ := store.Models.Insert(ctx, ModelInput{
		Code: mcode, DisplayName: "M", MinPlan: PlanFree, Status: StatusActive,
	})
	defer pool.Exec(ctx, "DELETE FROM model_relay.models WHERE id=$1", m.ID) //nolint:errcheck

	ch, err := store.Channels.Insert(ctx, ChannelInput{
		ModelID:       m.ID,
		CredentialID:  cred.ID,
		UpstreamModel: "gpt-4o",
		Priority:      100,
		Weight:        1,
		Status:        StatusActive,
	})
	if err != nil {
		t.Fatalf("channel insert: %v", err)
	}
	defer pool.Exec(ctx, "DELETE FROM model_relay.channels WHERE id=$1", ch.ID) //nolint:errcheck

	// 4 failures should NOT auto-disable
	for i := 0; i < 4; i++ {
		fc, status, err := store.Channels.RecordFailure(ctx, ch.ID, "boom", 5)
		if err != nil {
			t.Fatalf("record failure %d: %v", i, err)
		}
		if fc != i+1 {
			t.Fatalf("expected fc=%d got %d", i+1, fc)
		}
		if status != StatusActive {
			t.Fatalf("flipped too early at fc=%d", fc)
		}
	}

	// 5th failure flips to auto_disabled
	fc, status, err := store.Channels.RecordFailure(ctx, ch.ID, "boom", 5)
	if err != nil {
		t.Fatalf("5th: %v", err)
	}
	if fc != 5 || status != StatusAutoDisabled {
		t.Fatalf("expected auto_disabled at fc=5, got fc=%d status=%s", fc, status)
	}

	// ListAutoDisabled with 0 cooldown returns the channel
	dis, err := store.Channels.ListAutoDisabled(ctx, 0)
	if err != nil {
		t.Fatalf("list auto-disabled: %v", err)
	}
	foundDis := false
	for _, c := range dis {
		if c.ID == ch.ID {
			foundDis = true
		}
	}
	if !foundDis {
		t.Fatalf("expected auto-disabled channel in list")
	}

	// Recovery via RecordSuccess
	if err := store.Channels.RecordSuccess(ctx, ch.ID, 250); err != nil {
		t.Fatalf("record success: %v", err)
	}
	got, _ := store.Channels.Get(ctx, ch.ID)
	if got.Status != StatusActive || got.FailureCount != 0 {
		t.Fatalf("did not recover: %+v", got)
	}
	if got.LatencyP50Ms != 250 {
		t.Fatalf("expected first sample to set latency, got %d", got.LatencyP50Ms)
	}
}

func TestPricingHistoryAndCurrent(t *testing.T) {
	pool := openDB(t)
	store := NewStore(pool)
	ctx := context.Background()

	mcode := uniqueCode(t, "m_pricing")
	m, _ := store.Models.Insert(ctx, ModelInput{
		Code: mcode, DisplayName: "M", MinPlan: PlanFree, Status: StatusActive,
	})
	defer pool.Exec(ctx, "DELETE FROM model_relay.models WHERE id=$1", m.ID) //nolint:errcheck

	// Three pricing rows over time; GetCurrent should pick the latest
	// effective_at <= now()
	t0 := time.Now().Add(-2 * time.Hour)
	t1 := time.Now().Add(-1 * time.Hour)
	t2 := time.Now()

	for i, tt := range []struct {
		at        time.Time
		inputCost float64
	}{{t0, 1.0}, {t1, 1.5}, {t2, 2.0}} {
		_, err := store.Pricing.Set(ctx, PricingInput{
			ModelID:       m.ID,
			Currency:      CurrencyUSD,
			InputPerMTok:  tt.inputCost,
			OutputPerMTok: tt.inputCost * 2,
			EffectiveAt:   tt.at,
		})
		if err != nil {
			t.Fatalf("set pricing %d: %v", i, err)
		}
	}

	cur, err := store.Pricing.GetCurrent(ctx, m.ID)
	if err != nil || cur.InputPerMTok != 2.0 {
		t.Fatalf("get current: %+v err=%v", cur, err)
	}

	past, err := store.Pricing.GetAt(ctx, m.ID, t0.Add(30*time.Minute))
	if err != nil || past.InputPerMTok != 1.0 {
		t.Fatalf("get at t0+30m: %+v err=%v", past, err)
	}

	hist, err := store.Pricing.History(ctx, m.ID)
	if err != nil || len(hist) != 3 {
		t.Fatalf("history: %d %v", len(hist), err)
	}
	if hist[0].InputPerMTok != 2.0 || hist[2].InputPerMTok != 1.0 {
		t.Fatalf("history not desc: %+v", hist)
	}
}

func TestFxRatesSeedAndUpsert(t *testing.T) {
	pool := openDB(t)
	store := NewStore(pool)
	ctx := context.Background()

	// Seed rows from 00003 should exist
	rate, err := store.FxRates.GetRate(ctx, CurrencyUSD, CurrencyCNY)
	if err != nil {
		t.Fatalf("get USD→CNY: %v", err)
	}
	if rate < 6 || rate > 8 {
		t.Fatalf("seed USD→CNY out of range: %v", rate)
	}

	self, err := store.FxRates.GetRate(ctx, CurrencyUSD, CurrencyUSD)
	if err != nil || self != 1.0 {
		t.Fatalf("self-reflexive: %v err=%v", self, err)
	}

	// Upsert overwrites and returns new
	new, err := store.FxRates.Upsert(ctx, FxRateUpsert{
		FromCurrency: CurrencyUSD, ToCurrency: CurrencyCNY,
		Rate: 7.30, Source: "manual",
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if new.Rate != 7.30 {
		t.Fatalf("upsert rate not stored")
	}

	// Restore seed value so later runs aren't surprised
	_, _ = store.FxRates.Upsert(ctx, FxRateUpsert{
		FromCurrency: CurrencyUSD, ToCurrency: CurrencyCNY,
		Rate: 7.20, Source: "manual",
	})
}

func TestUsageLogAppendAndQuery(t *testing.T) {
	pool := openDB(t)
	store := NewStore(pool)
	ctx := context.Background()

	userID := uuid.New()
	modelID := uuid.New()
	channelID := uuid.New()

	defer pool.Exec(ctx, "DELETE FROM model_relay.usage_log WHERE user_id=$1", userID) //nolint:errcheck

	for i := 0; i < 3; i++ {
		err := store.UsageLog.Append(ctx, UsageLogInput{
			UserID:             userID,
			ModelID:            modelID,
			ChannelID:          channelID,
			ModelCode:          "test-model",
			UpstreamModel:      "gpt-4o",
			UserPlan:           PlanPro,
			InputTokens:        100,
			OutputTokens:       50,
			CostOriginCurrency: CurrencyUSD,
			CostOriginAmount:   0.0125,
			CostSettleCurrency: CurrencyCNY,
			CostSettleAmount:   0.09,
			FxRate:             7.20,
			LatencyMs:          800,
			Status:             UsageOK,
		})
		if err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	rows, err := store.UsageLog.RecentByUser(ctx, userID, 10)
	if err != nil || len(rows) != 3 {
		t.Fatalf("recent_by_user: %d %v", len(rows), err)
	}

	if rows[0].FxRate != 7.20 || rows[0].CostSettleCurrency != CurrencyCNY {
		t.Fatalf("dual currency lost: %+v", rows[0])
	}
}
