// rules_from_nl — LLM-assisted natural language → keyword rule.
// Wired only when both radar (P2) AND LLMAdvisor are attached.

package rss

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
)

// LLMAdvisor is the SDK-side surface for LLM-backed rss features.
// The services side wires a model-relay-backed implementation.
type LLMAdvisor interface {
	// FromNL converts a natural-language description into a rule
	// suggestion (P3 / radar editor).
	FromNL(ctx context.Context, token, text string) (*LLMRuleSuggestion, error)

	// Rephrase retells an article in a target persona voice. Returns
	// the full text (no streaming yet — client polls or awaits the
	// HTTP response). M1 acceptance requires this surface but allows a
	// nil return when the advisor predates the v2 interface.
	Rephrase(ctx context.Context, token, title, personaPrompt, body string) (string, error)
}

// LLMRuleSuggestion is what the advisor returns. Field shape matches
// CreateRuleInput minus tenant/sources (those are filled in by the
// caller / form layer).
type LLMRuleSuggestion struct {
	Name        string   `json:"name"`
	MatchAny    []string `json:"match_any"`
	MatchAll    []string `json:"match_all"`
	Exclude     []string `json:"exclude"`
	OnHitBadge  string   `json:"on_hit_badge"`
	CooldownSec int      `json:"cooldown_sec"`
}

func (a *App) invokeRulesFromNL(ctx context.Context, raw json.RawMessage) (any, error) {
	if a.llm == nil {
		return nil, errors.New("rss: llm advisor not wired")
	}
	var in struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, fmt.Errorf("rss: bad input: %w", err)
	}
	in.Text = strings.TrimSpace(in.Text)
	if in.Text == "" {
		return nil, errors.New("rss: text required")
	}
	if len(in.Text) > 500 {
		// Defensive: keep prompts tight; long pastes usually mean the
		// user mis-pasted an entry body. The form's hint text caps at
		// ~120 chars anyway.
		in.Text = in.Text[:500]
	}

	claims, ok := bauth.ClaimsFrom(ctx)
	if !ok || claims == nil {
		return nil, ErrNoCaller
	}
	// Use the caller's bearer for model-relay so quotas + per-user
	// billing apply naturally. The token is captured from the same
	// context AppCenter built when verifying the inbound JWT, so we
	// don't need to round-trip through Identity again.
	token := bauth.RawTokenFrom(ctx)
	if token == "" {
		return nil, errors.New("rss: missing bearer in context")
	}

	suggestion, err := a.llm.FromNL(ctx, token, in.Text)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"name":          suggestion.Name,
		"match_any":     suggestion.MatchAny,
		"match_all":     suggestion.MatchAll,
		"exclude":       suggestion.Exclude,
		"on_hit_badge":  suggestion.OnHitBadge,
		"cooldown_sec":  suggestion.CooldownSec,
	}, nil
}
