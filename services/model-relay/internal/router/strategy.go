// Package router holds the per-request channel selection logic.
//
// Strategy is the extension point for future routing modes
// (lowest_latency, least_busy, lowest_tpm_rpm, cost_aware) listed in
// design §3.3. MVP ships only `weighted`. The interface is deliberately
// narrow — Pick takes a fully-described PickInput and returns a single
// Channel pointer or an error. Adding a new strategy is a new file
// under internal/router/, no changes here.
//
// Why PickInput is a struct, not positional args: every future
// strategy needs slightly different inputs (latency stats, current
// rpm/tpm headroom, geographic affinity, ...). A struct lets us add
// fields without touching every call site or every existing strategy
// — strategies that don't care about a field just ignore it.

package router

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/biumind/biumind/services/model-relay/internal/registry"
)

// Sentinel errors. ModelResolver translates these to HTTP responses:
//   ErrNoCandidates → 503 model_unavailable
//   ErrAllExcluded  → 503 model_exhausted (after retries)
var (
	ErrNoCandidates = errors.New("router: no active channels for model")
	ErrAllExcluded  = errors.New("router: all candidates excluded from this attempt")
)

// PickInput is the immutable per-request state every Strategy.Pick
// receives. Fields are added (never removed/renamed) when new
// strategies need new signals — old strategies ignore the new field.
type PickInput struct {
	// ModelID is the model the user requested. Strategies may use it
	// to scope per-model state (e.g. latency_p50_ms is per-channel
	// but read in the context of "for this model").
	ModelID uuid.UUID

	// Candidates is the already-filtered list of active channels for
	// ModelID, pre-sorted by (priority DESC, weight DESC, id ASC).
	// Strategies MUST NOT mutate this slice — it's a cache view from
	// registry.Cache.ChannelsForModel.
	Candidates []registry.Channel

	// Exclude maps channel IDs the resolver has already tried (and
	// failed) on the current request to the failure they hit. The
	// strategy MUST skip these. The error value is informational —
	// future strategies (e.g. cost_aware) may decide a 429 deserves
	// a brief cooldown but a 5xx warrants permanent skip; weighted
	// just treats every entry as "skip". Empty on first attempt.
	Exclude map[uuid.UUID]error

	// Attempt is 0 on the first call for a request, 1 on first retry,
	// etc. Useful for strategies that want to widen tolerances under
	// retry (e.g. cost_aware: "first attempt cheapest; on retry pick
	// any healthy channel"). Weighted ignores it — Exclude is enough.
	Attempt int

	// UserID and UserPlan let strategies key per-tenant state. Both
	// fields populated by the resolver from the request's JWT claims.
	UserID   uuid.UUID
	UserPlan registry.Plan

	// RequestID propagates from the resolver for log correlation.
	// Strategies log their decision under this key.
	RequestID string
}

// Strategy picks one channel from a candidate set per request.
// Implementations must be safe for concurrent calls (the resolver
// is called from many goroutines simultaneously).
type Strategy interface {
	// Name returns the strategy code that matches
	// model_relay.models.routing_strategy values
	// ("weighted", "lowest_latency", ...).
	Name() string

	// Pick selects one channel. Returns ErrNoCandidates when
	// in.Candidates is empty, ErrAllExcluded when every candidate
	// is in in.Exclude. Other errors propagate as-is.
	Pick(ctx context.Context, in PickInput) (*registry.Channel, error)
}

// Registry is a minimal lookup so the resolver can pick the right
// strategy by name (taken from Model.RoutingStrategy). Strategies
// register themselves at process startup; resolver hits the registry
// on every request, so a map[string]Strategy is sufficient — no need
// for sync.Map or atomic.Value because writes happen exactly once.
type Registry struct {
	strategies map[string]Strategy
}

// NewRegistry constructs an empty registry. Use Register to add
// strategies; the result is meant to be built once at startup and
// passed read-only thereafter.
func NewRegistry() *Registry {
	return &Registry{strategies: map[string]Strategy{}}
}

// Register adds a strategy under its Name(). Panics on duplicate —
// double-registration is a programmer error, not a runtime condition.
func (r *Registry) Register(s Strategy) {
	if _, dup := r.strategies[s.Name()]; dup {
		panic("router: duplicate strategy " + s.Name())
	}
	r.strategies[s.Name()] = s
}

// Get returns the registered strategy or false. Resolver calls this
// once per request; misses fall back to the default ("weighted").
func (r *Registry) Get(name string) (Strategy, bool) {
	s, ok := r.strategies[name]
	return s, ok
}
