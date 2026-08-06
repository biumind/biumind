// SDK adapter — bridges this package's concrete *Store to the
// rss.RadarStore interface (defined in the SDK). Wired in main.go.

package radar

import (
	"context"

	"github.com/google/uuid"

	"github.com/biumind/biumind/packages/go-sdk/biu/biuapp/rss"
)

type SDKAdapter struct {
	Store *Store
}

var _ rss.RadarStore = (*SDKAdapter)(nil)

func (a *SDKAdapter) CreateRule(ctx context.Context, scope, scopeID string, in rss.CreateRuleInput) (*rss.RuleSummary, error) {
	r, err := a.Store.CreateRule(ctx, CreateRuleInput{
		Scope: scope, ScopeID: scopeID,
		Name:              in.Name,
		MatchAny:          in.MatchAny,
		MatchAll:          in.MatchAll,
		Exclude:           in.Exclude,
		Sources:           in.Sources,
		OnHitBadge:        in.OnHitBadge,
		OnHitNotify:       in.OnHitNotify,
		CooldownSec:       in.CooldownSec,
		SemanticQuery:     in.SemanticQuery,
		SemanticThreshold: in.SemanticThreshold,
		Actions:           in.Actions,
	})
	if err != nil {
		return nil, err
	}
	return ruleToSDK(r), nil
}

func (a *SDKAdapter) ListRules(ctx context.Context, scope, scopeID string) ([]*rss.RuleSummary, error) {
	rs, err := a.Store.ListRules(ctx, scope, scopeID)
	if err != nil {
		return nil, err
	}
	out := make([]*rss.RuleSummary, len(rs))
	for i, r := range rs {
		out[i] = ruleToSDK(r)
	}
	return out, nil
}

func (a *SDKAdapter) UpdateRule(ctx context.Context, scope, scopeID string, id uuid.UUID, in rss.UpdateRuleInput) (*rss.RuleSummary, error) {
	r, err := a.Store.UpdateRule(ctx, scope, scopeID, id, UpdateRuleInput{
		Name:              in.Name,
		MatchAny:          in.MatchAny,
		MatchAll:          in.MatchAll,
		Exclude:           in.Exclude,
		Sources:           in.Sources,
		OnHitBadge:        in.OnHitBadge,
		OnHitNotify:       in.OnHitNotify,
		CooldownSec:       in.CooldownSec,
		Enabled:           in.Enabled,
		SemanticQuery:     in.SemanticQuery,
		SemanticThreshold: in.SemanticThreshold,
		Actions:           in.Actions,
	})
	if err != nil {
		return nil, err
	}
	return ruleToSDK(r), nil
}

func (a *SDKAdapter) DeleteRule(ctx context.Context, scope, scopeID string, id uuid.UUID) error {
	return a.Store.DeleteRule(ctx, scope, scopeID, id)
}

func (a *SDKAdapter) ListHits(ctx context.Context, scope, scopeID string, opts rss.ListHitsOpts) ([]*rss.HitSummary, error) {
	hits, err := a.Store.ListHitsWithRule(ctx, scope, scopeID, ListHitsOpts{
		RuleID: opts.RuleID, UnreadOnly: opts.UnreadOnly, Limit: opts.Limit,
	})
	if err != nil {
		return nil, err
	}
	return hits, nil
}

func (a *SDKAdapter) MarkHitRead(ctx context.Context, scope, scopeID string, id int64) error {
	return a.Store.MarkHitRead(ctx, scope, scopeID, id)
}

func (a *SDKAdapter) UnreadCount(ctx context.Context, scope, scopeID string) (int, error) {
	return a.Store.UnreadCount(ctx, scope, scopeID)
}

func (a *SDKAdapter) UnreadMaxSeverity(ctx context.Context, scope, scopeID string) (string, error) {
	return a.Store.UnreadMaxSeverity(ctx, scope, scopeID)
}

// LLMSDKAdapter bridges *LLMClient → rss.LLMAdvisor. Wired at boot
// when MODEL_RELAY_URL is set.

type LLMSDKAdapter struct {
	Client *LLMClient
}

var _ rss.LLMAdvisor = (*LLMSDKAdapter)(nil)

func (a *LLMSDKAdapter) Rephrase(ctx context.Context, token, title, personaPrompt, body string) (string, error) {
	return a.Client.Rephrase(ctx, token, title, personaPrompt, body)
}

func (a *LLMSDKAdapter) FromNL(ctx context.Context, token, text string) (*rss.LLMRuleSuggestion, error) {
	r, err := a.Client.FromNL(ctx, token, text)
	if err != nil {
		return nil, err
	}
	return &rss.LLMRuleSuggestion{
		Name:        r.Name,
		MatchAny:    r.MatchAny,
		MatchAll:    r.MatchAll,
		Exclude:     r.Exclude,
		OnHitBadge:  r.OnHitBadge,
		CooldownSec: r.CooldownSec,
	}, nil
}

func ruleToSDK(r *Rule) *rss.RuleSummary {
	return &rss.RuleSummary{
		ID:                r.ID,
		Scope:             r.Scope,
		ScopeID:           r.ScopeID,
		Name:              r.Name,
		MatchAny:          r.MatchAny,
		MatchAll:          r.MatchAll,
		Exclude:           r.Exclude,
		Sources:           r.Sources,
		OnHitBadge:        r.OnHitBadge,
		OnHitNotify:       r.OnHitNotify,
		CooldownSec:       r.CooldownSec,
		Enabled:           r.Enabled,
		CreatedAt:         r.CreatedAt,
		UpdatedAt:         r.UpdatedAt,
		SemanticQuery:     r.SemanticQuery,
		SemanticThreshold: r.SemanticThreshold,
		Actions:           r.Actions,
	}
}
