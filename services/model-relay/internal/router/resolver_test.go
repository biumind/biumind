// Resolver integration tests against dev Postgres. These exercise the
// full chain: cache loaded from DB, model+channel+credential roundtrip,
// strategy pick, and credential decrypt.
//
// Skip when DATABASE_URL is unset, mirroring registry/integration_test.go.

package router

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/biumind/biumind/services/model-relay/internal/keys"
	"github.com/biumind/biumind/services/model-relay/internal/registry"
)

func openDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping resolver integration tests")
	}
	p, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(p.Close)
	return p
}

// fixture builds a minimal end-to-end seed: 1 provider + 1 credential
// + 1 model bound to default group + N channels. Returns clean-up.
type fixture struct {
	pool      *pgxpool.Pool
	store     *registry.Store
	vault     *registry.CredentialVault
	cache     *registry.Cache
	resolver  *Resolver
	provider  *registry.Provider
	cred      *registry.CredentialSafe
	model     *registry.Model
	channels  []registry.Channel
	plaintext string
	t         *testing.T
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	pool := openDB(t)
	store := registry.NewStore(pool)
	ctx := context.Background()

	// Envelope with a fresh random KEK
	kek := make([]byte, 32)
	if _, err := rand.Read(kek); err != nil {
		t.Fatalf("rand kek: %v", err)
	}
	env, err := keys.NewEnvelope(kek)
	if err != nil {
		t.Fatalf("envelope: %v", err)
	}
	vault := registry.NewCredentialVault(store.Credentials, env)

	// Provider
	pcode := fmt.Sprintf("p_resolver_%d", time.Now().UnixNano())
	prov, err := store.Providers.Insert(ctx, registry.ProviderInput{
		Code: pcode, Name: "P", Protocol: registry.ProtocolOpenAICompat,
	})
	if err != nil {
		t.Fatalf("provider: %v", err)
	}

	// Credential via vault (so envelope is real)
	const plaintext = "sk-resolver-test-key-aaaaaa"
	safe, err := vault.Save(ctx, registry.SaveInput{
		ProviderID: prov.ID, Label: "L", Plaintext: plaintext,
		BaseURL: "https://api.example", HeaderOverride: map[string]string{"X": "1"},
	})
	if err != nil {
		t.Fatalf("save credential: %v", err)
	}

	// Model (Pro tier, active)
	mcode := fmt.Sprintf("m_resolver_%d", time.Now().UnixNano())
	mdl, err := store.Models.Insert(ctx, registry.ModelInput{
		Code: mcode, DisplayName: "M", MinPlan: registry.PlanPro,
		Status: registry.StatusActive,
	})
	if err != nil {
		t.Fatalf("model: %v", err)
	}
	if err := store.Groups.SetModelBindings(ctx, mdl.ID,
		[]uuid.UUID{registry.DefaultGroupID}); err != nil {
		t.Fatalf("bind default: %v", err)
	}

	// Two channels at different priorities
	ch1, _ := store.Channels.Insert(ctx, registry.ChannelInput{
		ModelID: mdl.ID, CredentialID: safe.ID,
		UpstreamModel: "upstream-1",
		Priority:      100, Weight: 1, Status: registry.StatusActive,
	})
	ch2, _ := store.Channels.Insert(ctx, registry.ChannelInput{
		ModelID: mdl.ID, CredentialID: safe.ID,
		UpstreamModel: "upstream-2",
		Priority:      50, Weight: 1, Status: registry.StatusActive,
	})

	// Cache + resolver
	cache := registry.NewCache(store, registry.CacheConfig{TTL: 5 * time.Minute})
	if err := cache.Start(ctx); err != nil {
		t.Fatalf("cache start: %v", err)
	}

	reg := NewRegistry()
	reg.Register(NewWeighted())

	resolver := NewResolver(cache, vault, reg, ResolverConfig{})

	fx := &fixture{
		pool:      pool,
		store:     store,
		vault:     vault,
		cache:     cache,
		resolver:  resolver,
		provider:  prov,
		cred:      safe,
		model:     mdl,
		channels:  []registry.Channel{*ch1, *ch2},
		plaintext: plaintext,
		t:         t,
	}
	t.Cleanup(fx.close)
	return fx
}

func (fx *fixture) close() {
	fx.cache.Close()
	ctx := context.Background()
	for _, ch := range fx.channels {
		_, _ = fx.pool.Exec(ctx, "DELETE FROM model_relay.channels WHERE id=$1", ch.ID)
	}
	_, _ = fx.pool.Exec(ctx, "DELETE FROM model_relay.models WHERE id=$1", fx.model.ID)
	_, _ = fx.pool.Exec(ctx, "DELETE FROM model_relay.credentials WHERE id=$1", fx.cred.ID)
	_, _ = fx.pool.Exec(ctx, "DELETE FROM model_relay.providers WHERE id=$1", fx.provider.ID)
}

func TestResolver_HappyPath(t *testing.T) {
	fx := newFixture(t)

	out, err := fx.resolver.Resolve(context.Background(), ResolveInput{
		ModelCode: fx.model.Code,
		UserID:    uuid.New(),
		UserPlan:  registry.PlanPro,
		RequestID: "req-1",
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if out.Model.Code != fx.model.Code {
		t.Fatalf("wrong model: %s", out.Model.Code)
	}
	// Top priority = 100 → upstream-1 always wins
	if out.UpstreamModel != "upstream-1" {
		t.Fatalf("strategy didn't honor priority: %s", out.UpstreamModel)
	}
	if string(out.Plaintext) != fx.plaintext {
		t.Fatalf("decrypted plaintext mismatch")
	}
	if out.BaseURL != "https://api.example" {
		t.Fatalf("base_url lost: %q", out.BaseURL)
	}
	if out.Header["X"] != "1" {
		t.Fatalf("header lost: %v", out.Header)
	}
}

func TestResolver_PlanGate(t *testing.T) {
	fx := newFixture(t)

	// Free user requesting a Pro-only model → ErrModelHidden
	_, err := fx.resolver.Resolve(context.Background(), ResolveInput{
		ModelCode: fx.model.Code,
		UserID:    uuid.New(),
		UserPlan:  registry.PlanFree,
	})
	if !errors.Is(err, ErrModelHidden) {
		t.Fatalf("expected ErrModelHidden, got %v", err)
	}

	// Team user → also allowed (rank > min_plan)
	out, err := fx.resolver.Resolve(context.Background(), ResolveInput{
		ModelCode: fx.model.Code,
		UserID:    uuid.New(),
		UserPlan:  registry.PlanTeam,
	})
	if err != nil {
		t.Fatalf("team should be allowed: %v", err)
	}
	if out == nil {
		t.Fatalf("nil out")
	}
}

func TestResolver_UnknownModel(t *testing.T) {
	fx := newFixture(t)
	_, err := fx.resolver.Resolve(context.Background(), ResolveInput{
		ModelCode: "definitely-not-a-real-model",
		UserID:    uuid.New(),
		UserPlan:  registry.PlanPro,
	})
	if !errors.Is(err, ErrModelNotFound) {
		t.Fatalf("expected ErrModelNotFound, got %v", err)
	}
}

// 模型存在但被 admin 停用(status=disabled)→ ErrModelDisabled,区别于真·not
// found。cache 不按 status 过滤(reloadModels 用 ModelFilter{}),disabled 模型
// 仍会进 modelsByCode 透给 resolver,故这条分支是真实路径而非仅 defense-in-depth。
// 客户端据此码给「模型已停用,请重新选择」的精准降级文案。
func TestResolver_DisabledModel(t *testing.T) {
	fx := newFixture(t)
	ctx := context.Background()

	mcode := fmt.Sprintf("m_disabled_%d", time.Now().UnixNano())
	mdl, err := fx.store.Models.Insert(ctx, registry.ModelInput{
		Code: mcode, DisplayName: "Disabled", MinPlan: registry.PlanFree,
		Status: registry.StatusDisabled,
	})
	if err != nil {
		t.Fatalf("model: %v", err)
	}
	defer fx.pool.Exec(ctx, "DELETE FROM model_relay.models WHERE id=$1", mdl.ID) //nolint:errcheck

	// Wait for cache invalidation (mirror TestResolver_ModelWithNoChannels).
	time.Sleep(150 * time.Millisecond)

	_, err = fx.resolver.Resolve(ctx, ResolveInput{
		ModelCode: mcode, UserID: uuid.New(), UserPlan: registry.PlanFree,
	})
	if !errors.Is(err, ErrModelDisabled) {
		t.Fatalf("expected ErrModelDisabled, got %v", err)
	}
	// 必须区别于 ErrModelNotFound —— 否则客户端无法分辨「停用」与「不存在」。
	if errors.Is(err, ErrModelNotFound) {
		t.Fatalf("disabled model must NOT be ErrModelNotFound: %v", err)
	}
}

func TestResolver_RetryFailoverThroughExclude(t *testing.T) {
	fx := newFixture(t)
	ctx := context.Background()

	// First attempt: top priority pick (upstream-1)
	out1, err := fx.resolver.Resolve(ctx, ResolveInput{
		ModelCode: fx.model.Code,
		UserID:    uuid.New(),
		UserPlan:  registry.PlanPro,
	})
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	if out1.UpstreamModel != "upstream-1" {
		t.Fatalf("first attempt should pick top priority")
	}

	// Simulate retry — exclude upstream-1
	out2, err := fx.resolver.Resolve(ctx, ResolveInput{
		ModelCode: fx.model.Code,
		UserID:    uuid.New(),
		UserPlan:  registry.PlanPro,
		Exclude:   map[uuid.UUID]error{out1.Channel.ID: errors.New("upstream 503")},
		Attempt:   1,
	})
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if out2.UpstreamModel != "upstream-2" {
		t.Fatalf("retry should pick lower priority: %s", out2.UpstreamModel)
	}

	// Both excluded — ErrAllChannelsFailed
	_, err = fx.resolver.Resolve(ctx, ResolveInput{
		ModelCode: fx.model.Code,
		UserID:    uuid.New(),
		UserPlan:  registry.PlanPro,
		Exclude: map[uuid.UUID]error{
			out1.Channel.ID: errors.New("a"),
			out2.Channel.ID: errors.New("b"),
		},
		Attempt: 2,
	})
	if !errors.Is(err, ErrAllChannelsFailed) {
		t.Fatalf("expected ErrAllChannelsFailed, got %v", err)
	}
}

func TestResolver_ModelWithNoChannels(t *testing.T) {
	fx := newFixture(t)
	ctx := context.Background()

	// Create a model with NO channels
	mcode := fmt.Sprintf("m_empty_%d", time.Now().UnixNano())
	emptyMdl, err := fx.store.Models.Insert(ctx, registry.ModelInput{
		Code: mcode, DisplayName: "Empty", MinPlan: registry.PlanFree,
		Status: registry.StatusActive,
	})
	if err != nil {
		t.Fatalf("model: %v", err)
	}
	defer fx.pool.Exec(ctx, "DELETE FROM model_relay.models WHERE id=$1", emptyMdl.ID) //nolint:errcheck
	if err := fx.store.Groups.SetModelBindings(ctx, emptyMdl.ID,
		[]uuid.UUID{registry.DefaultGroupID}); err != nil {
		t.Fatalf("bind: %v", err)
	}

	// Wait for cache invalidation
	time.Sleep(150 * time.Millisecond)

	_, err = fx.resolver.Resolve(ctx, ResolveInput{
		ModelCode: mcode, UserID: uuid.New(), UserPlan: registry.PlanFree,
	})
	if !errors.Is(err, ErrNoActiveChannel) {
		t.Fatalf("expected ErrNoActiveChannel, got %v", err)
	}
}

// Group filter: model bound to a non-default group → free user can't see.
func TestResolver_GroupFilter(t *testing.T) {
	fx := newFixture(t)
	ctx := context.Background()

	// Make a private group + bind model exclusively to it (remove default)
	gcode := fmt.Sprintf("g_priv_%d", time.Now().UnixNano())
	priv, err := fx.store.Groups.Insert(ctx, registry.ModelGroupInput{
		Code: gcode, Name: "Private", OwnerType: registry.OwnerSystem,
	})
	if err != nil {
		t.Fatalf("group: %v", err)
	}
	defer fx.pool.Exec(ctx, "DELETE FROM model_relay.model_groups WHERE id=$1", priv.ID) //nolint:errcheck

	// Replace bindings: only private, no default
	if err := fx.store.Groups.SetModelBindings(ctx, fx.model.ID,
		[]uuid.UUID{priv.ID}); err != nil {
		t.Fatalf("rebind: %v", err)
	}
	// Restore default at end so other tests aren't affected
	defer fx.store.Groups.SetModelBindings(ctx, fx.model.ID, []uuid.UUID{registry.DefaultGroupID}) //nolint:errcheck

	time.Sleep(150 * time.Millisecond)

	_, err = fx.resolver.Resolve(ctx, ResolveInput{
		ModelCode: fx.model.Code,
		UserID:    uuid.New(),
		UserPlan:  registry.PlanPro,
	})
	if !errors.Is(err, ErrModelHidden) {
		t.Fatalf("expected ErrModelHidden when model not in user's groups, got %v", err)
	}

	// User with explicit private group membership → can see it
	resolverWithLookup := NewResolver(fx.cache, fx.vault,
		mustRegistryWithWeighted(), ResolverConfig{
			UserGroupLookup: func(ctx context.Context, _ uuid.UUID) ([]uuid.UUID, error) {
				return []uuid.UUID{priv.ID}, nil
			},
		})
	out, err := resolverWithLookup.Resolve(ctx, ResolveInput{
		ModelCode: fx.model.Code,
		UserID:    uuid.New(),
		UserPlan:  registry.PlanPro,
	})
	if err != nil {
		t.Fatalf("user with private group access should resolve: %v", err)
	}
	if out.Model.Code != fx.model.Code {
		t.Fatalf("wrong model resolved")
	}
}

func mustRegistryWithWeighted() *Registry {
	r := NewRegistry()
	r.Register(NewWeighted())
	return r
}
