// ModelResolver is the per-request glue between cache (registry data),
// strategy (channel selection), and vault (credential decryption).
//
// One Resolve call = one channel pick. Retry semantics live in the
// caller: on upstream failure, the caller adds the failed channel to
// ResolveInput.Exclude with the error and calls Resolve again.
//
// Visibility filtering happens here (not in the strategy) because it's
// a pre-condition: a strategy that doesn't see hidden models can't
// accidentally route to one. The two filters in order:
//
//   1. plan_rank(user) >= plan_rank(model.min_plan)
//   2. user's visible groups ∩ model's bound groups != ∅
//
// MVP short-circuit: every user is implicitly a member of the
// 'default' group. ResolverConfig.UserGroupLookup overrides this for
// Phase 3 multi-tenant.

package router

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/biumind/biumind/services/model-relay/internal/registry"
)

// Sentinel errors. Resolver errors translate to HTTP responses upstream
// (see internal/api/messages.go for the code mapping the client keys on):
//   ErrModelDisabled    → model_disabled        (admin 把模型 status 置非 active)
//   ErrModelNotFound    → model_not_found        (model code 不存在)
//   ErrModelHidden      → model_hidden_for_plan  (plan/分组不可见)
//   ErrNoActiveChannel  → model_no_channel       (无活跃 channel)
//   ErrAllChannelsFailed → channel_quota_exhausted / model_no_channel
//   ErrCredentialUnavailable → model_credential_unavailable
// ErrModelDisabled 与 ErrModelNotFound 分开,是为了让客户端把「模型已停用,
// 请重新选择」与「模型不存在」给出不同文案 —— 两者的用户动作其实一样(换个
// 模型),但停用是 admin 侧可逆操作,文案更准能减少困惑。
var (
	ErrModelDisabled         = errors.New("resolver: model disabled")
	ErrModelNotFound         = errors.New("resolver: model not found")
	ErrModelHidden           = errors.New("resolver: model not visible to user")
	ErrNoActiveChannel       = errors.New("resolver: no active channel for model")
	ErrAllChannelsFailed     = errors.New("resolver: all channels failed")
	ErrCredentialUnavailable = errors.New("resolver: credential unavailable")
)

// ResolverConfig wires the resolver. UserGroupLookup is optional; nil
// means "every user implicitly belongs to default group" (MVP). Set to
// a real DB-backed lookup for Phase 3 multi-tenant.
type ResolverConfig struct {
	UserGroupLookup func(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error)
	Logger          *slog.Logger
}

// Resolver is constructed once at startup. Not safe for in-place
// reconfiguration — replace the whole struct if you need to swap a
// strategy registry. Resolve is goroutine-safe.
type Resolver struct {
	cache      *registry.Cache
	vault      *registry.CredentialVault
	strategies *Registry
	cfg        ResolverConfig
}

// NewResolver wires the dependencies. cache and strategies must be
// non-nil; vault may be nil in tests that don't decrypt (just channel
// pick verification).
func NewResolver(cache *registry.Cache, vault *registry.CredentialVault, strategies *Registry, cfg ResolverConfig) *Resolver {
	if cache == nil {
		panic("router.NewResolver: cache required")
	}
	if strategies == nil {
		panic("router.NewResolver: strategies required")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Resolver{
		cache:      cache,
		vault:      vault,
		strategies: strategies,
		cfg:        cfg,
	}
}

// ResolveInput captures the per-request context. ModelCode is the
// alias the client requested (e.g. "claude-sonnet-4"); Resolver maps
// it to a Channel via the cache + strategy.
type ResolveInput struct {
	ModelCode string
	UserID    uuid.UUID
	UserPlan  registry.Plan
	RequestID string

	// Exclude carries failed channels from previous retries. The
	// strategy skips them. Empty on first attempt.
	Exclude map[uuid.UUID]error

	// Attempt is 0 on first call, 1 on first retry, ...
	Attempt int
}

// ResolveOutput is the resolved request: channel + decrypted credential
// material ready to hand to a provider adaptor. Plaintext is fresh on
// every call; caller may zero it after use without affecting cache state.
type ResolveOutput struct {
	Model            *registry.Model
	Channel          *registry.Channel
	Provider         *registry.Provider
	ProviderProtocol registry.ProviderProtocol
	Plaintext        []byte
	UpstreamModel    string
	BaseURL          string
	Header           map[string]string
}

// Resolve performs the full lookup. On any error the returned
// ResolveOutput is nil and Plaintext bytes never escape the function.
func (r *Resolver) Resolve(ctx context.Context, in ResolveInput) (*ResolveOutput, error) {
	if in.ModelCode == "" {
		return nil, fmt.Errorf("resolver: model_code required")
	}

	r.cfg.Logger.DebugContext(ctx, "router resolve: enter",
		"model", in.ModelCode, "user_id", in.UserID,
		"plan", in.UserPlan, "attempt", in.Attempt,
		"excluded", len(in.Exclude), "request_id", in.RequestID)

	// 1. Find the model by code.
	model, err := r.cache.GetModelByCode(ctx, in.ModelCode)
	if err != nil {
		if errors.Is(err, registry.ErrNotFound) {
			return nil, fmt.Errorf("%w: %s", ErrModelNotFound, in.ModelCode)
		}
		return nil, fmt.Errorf("resolver.get_model: %w", err)
	}
	if model.Status != registry.StatusActive {
		// cache should pre-filter, but defend in depth: a stale cache
		// entry between status flip and NOTIFY arrival could surface
		// here. 模型存在但被 admin 停用 → ErrModelDisabled(区别于真·not
		// found),让客户端给出「已停用,请重新选择」的精准文案。
		return nil, fmt.Errorf("%w: %s status=%s", ErrModelDisabled, model.Code, model.Status)
	}

	// 2. Visibility filter — plan rank.
	if planRank(in.UserPlan) < planRank(model.MinPlan) {
		return nil, fmt.Errorf("%w: %s requires %s, user has %s",
			ErrModelHidden, model.Code, model.MinPlan, in.UserPlan)
	}

	// 3. Visibility filter — group intersection.
	visible, err := r.userVisibleGroups(ctx, in.UserID)
	if err != nil {
		return nil, fmt.Errorf("resolver.user_groups: %w", err)
	}
	bound, err := r.cache.GroupsForModel(ctx, model.ID)
	if err != nil {
		return nil, fmt.Errorf("resolver.model_groups: %w", err)
	}
	if !groupsIntersect(visible, bound) {
		return nil, fmt.Errorf("%w: %s not bound to any group user can see",
			ErrModelHidden, model.Code)
	}

	// 4. Active channels for this model.
	candidates, err := r.cache.ChannelsForModel(ctx, model.ID)
	if err != nil {
		return nil, fmt.Errorf("resolver.channels: %w", err)
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrNoActiveChannel, model.Code)
	}

	// 5. Pick a channel via the configured strategy.
	strategy, ok := r.strategies.Get(string(model.RoutingStrategy))
	if !ok {
		// Schema only allows known strategies, but if a future enum
		// value lands without a registered impl, fall back to weighted.
		r.cfg.Logger.Warn("resolver: unknown routing_strategy, falling back to weighted",
			"strategy", model.RoutingStrategy, "model", model.Code)
		strategy, ok = r.strategies.Get("weighted")
		if !ok {
			return nil, fmt.Errorf("resolver: no weighted strategy registered")
		}
	}
	channel, err := strategy.Pick(ctx, PickInput{
		ModelID:    model.ID,
		Candidates: candidates,
		Exclude:    in.Exclude,
		Attempt:    in.Attempt,
		UserID:     in.UserID,
		UserPlan:   in.UserPlan,
		RequestID:  in.RequestID,
	})
	if err == nil && channel != nil {
		r.cfg.Logger.DebugContext(ctx, "router resolve: channel selected",
			"model", in.ModelCode, "channel_id", channel.ID,
			"upstream_model", channel.UpstreamModel,
			"strategy", string(model.RoutingStrategy),
			"candidates", len(candidates), "attempt", in.Attempt,
			"request_id", in.RequestID)
	}
	if err != nil {
		switch {
		case errors.Is(err, ErrNoCandidates):
			return nil, fmt.Errorf("%w: %s", ErrNoActiveChannel, model.Code)
		case errors.Is(err, ErrAllExcluded):
			return nil, fmt.Errorf("%w: %s", ErrAllChannelsFailed, model.Code)
		default:
			return nil, fmt.Errorf("resolver.strategy.pick: %w", err)
		}
	}

	// 6. Fetch credential and decrypt. Cache holds the cipher bytes;
	// we never re-query DB at this point.
	cred, err := r.cache.GetCredential(ctx, channel.CredentialID)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCredentialUnavailable, err)
	}
	prov, err := r.cache.GetProvider(ctx, cred.ProviderID)
	if err != nil {
		return nil, fmt.Errorf("%w: provider %v: %v",
			ErrCredentialUnavailable, cred.ProviderID, err)
	}
	if r.vault == nil {
		// Test mode — caller didn't wire a vault. Surface the cipher
		// state so unit tests can still observe routing decisions.
		return &ResolveOutput{
			Model:            model,
			Channel:          channel,
			Provider:         prov,
			ProviderProtocol: prov.Protocol,
			UpstreamModel:    channel.UpstreamModel,
			BaseURL:          cred.BaseURL,
			Header:           cred.HeaderOverride,
		}, nil
	}
	plaintext, err := r.vault.RevealFromCached(cred)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCredentialUnavailable, err)
	}

	return &ResolveOutput{
		Model:            model,
		Channel:          channel,
		Provider:         prov,
		ProviderProtocol: prov.Protocol,
		Plaintext:        plaintext,
		UpstreamModel:    channel.UpstreamModel,
		BaseURL:          cred.BaseURL,
		Header:           cred.HeaderOverride,
	}, nil
}

// ─── helpers ──────────────────────────────────────────────────────

// planRank encodes the plan tier ordering. Mirrors the CASE expression
// in registry.Models.ListVisibleTo so the in-memory check matches the
// SQL filter exactly.
func planRank(p registry.Plan) int {
	switch p {
	case registry.PlanFree:
		return 0
	case registry.PlanPro:
		return 1
	case registry.PlanTeam:
		return 2
	default:
		return -1 // unknown plan: deny everything
	}
}

func groupsIntersect(a, b []uuid.UUID) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	// Small-set intersection: O(len(a) * len(b)). Both lists typically
	// have 1-3 items in MVP, so a hash-set wrapper would cost more in
	// allocations than the linear scan saves.
	for _, x := range a {
		for _, y := range b {
			if x == y {
				return true
			}
		}
	}
	return false
}

// userVisibleGroups returns the group ids a user can see. Defaults to
// just DefaultGroupID for MVP; ResolverConfig.UserGroupLookup overrides
// for Phase 3 multi-tenant.
func (r *Resolver) userVisibleGroups(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	if r.cfg.UserGroupLookup == nil {
		// MVP: implicit default-group membership for everyone.
		return []uuid.UUID{registry.DefaultGroupID}, nil
	}
	groups, err := r.cfg.UserGroupLookup(ctx, userID)
	if err != nil {
		return nil, err
	}
	// Always ensure DefaultGroupID is present — even Phase 3 keeps the
	// "everyone sees default" floor unless an admin explicitly opts a
	// user out (handled by the lookup function returning a list that
	// excludes default; not done in MVP).
	for _, g := range groups {
		if g == registry.DefaultGroupID {
			return groups, nil
		}
	}
	return append(groups, registry.DefaultGroupID), nil
}
