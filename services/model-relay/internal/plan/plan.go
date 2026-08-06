// Package plan resolves a request's user → billing plan and exposes
// helpers to size per-plan quota buckets.
//
// model-relay uses one quota.Limiter for everything; we partition by plan via
// distinct bucket names ("hub.rpm.free" vs "hub.rpm.pro"). The Limiter
// is constructed at startup with one Spec per (metric, plan) pair, so
// no per-request allocation happens here.
//
// Plan / Limits values mirror services/identity/internal/billing
// deliberately — model-relay does not import identity to keep the service
// dependency graph one-way (identity emits, model-relay consumes via JWT/HTTP).
//
// A nil Resolver is harmless: PlanFromRequest falls back to the free
// tier. This keeps model-relay usable in self-hosted / single-tenant deployments
// where there is no Identity service to query.

package plan

import (
	"context"
	"net/http"
	"time"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/biumind/biumind/packages/go-sdk/biu/quota"
)

// Plan is the canonical pricing tier name. Values must match the
// strings persisted by services/identity/internal/billing so that JWT
// claims / Identity responses round-trip cleanly.
type Plan string

const (
	PlanFree Plan = "free"
	PlanPro  Plan = "pro"
	PlanTeam Plan = "team"
)

// Limits is the subset of identity/billing.PlanLimits that model-relay cares
// about: just the relay rate ceilings.
type Limits struct {
	HubRPM int64
	HubTPM int64
}

// DefaultLimits mirrors billing.DefaultLimits[*].HubRPM / HubTPM. Keep
// these in sync; the integration test in services/model-relay/internal/plan
// asserts every plan in identity/billing has a model-relay entry too.
var DefaultLimits = map[Plan]Limits{
	PlanFree: {HubRPM: 60, HubTPM: 50_000},
	PlanPro:  {HubRPM: 600, HubTPM: 500_000},
	PlanTeam: {HubRPM: 6_000, HubTPM: 5_000_000},
}

// Resolver maps a user id to their current plan. Implementations may
// query Identity over HTTP, read a shared DB, or pull from an in-memory
// cache. Errors fall back to the free tier so a transient outage does
// not lock paying users out — log and move on.
type Resolver interface {
	Resolve(ctx context.Context, userID string) (Plan, error)
}

// ResolverFunc adapts a plain function to Resolver.
type ResolverFunc func(ctx context.Context, userID string) (Plan, error)

func (f ResolverFunc) Resolve(ctx context.Context, userID string) (Plan, error) {
	return f(ctx, userID)
}

// StaticResolver always returns the same plan. Useful for self-hosted
// deployments where everyone is on the same tier.
func StaticResolver(p Plan) Resolver {
	return ResolverFunc(func(context.Context, string) (Plan, error) { return p, nil })
}

// PlanFromRequest resolves the plan for the JWT subject of r. If r has
// no claims, or resolver is nil, or resolution fails, free is returned
// — the limiter will still gate at the lowest ceiling.
func PlanFromRequest(r *http.Request, resolver Resolver) Plan {
	if resolver == nil {
		return PlanFree
	}
	c, ok := bauth.ClaimsFrom(r.Context())
	if !ok || c.UserID == "" {
		return PlanFree
	}
	p, err := resolver.Resolve(r.Context(), c.UserID)
	if err != nil || p == "" {
		return PlanFree
	}
	if _, known := DefaultLimits[p]; !known {
		return PlanFree
	}
	return p
}

// SpecsFor builds the (bucketName → Spec) map every model-relay limiter needs:
// one entry per (metric, plan) pair. Pass to quota.NewInMemoryLimiter
// or quota.NewPGLimiter at startup.
//
//	specs := plan.SpecsFor(plan.DefaultLimits)
//	limiter := quota.NewInMemoryLimiter(specs)
//	bucket := plan.BucketFor("hub.rpm", plan.PlanPro)  // "hub.rpm.pro"
//	limiter.CheckAndReserve(bucket, userID, 1)
func SpecsFor(limits map[Plan]Limits) map[string]quota.Spec {
	out := map[string]quota.Spec{}
	for p, lim := range limits {
		if lim.HubRPM > 0 {
			out[BucketFor("hub.rpm", p)] = quota.Spec{
				Window: time.Minute, Limit: lim.HubRPM, Unit: "requests",
			}
		}
		if lim.HubTPM > 0 {
			out[BucketFor("hub.tpm", p)] = quota.Spec{
				Window: time.Minute, Limit: lim.HubTPM, Unit: "tokens",
			}
		}
	}
	return out
}

// BucketFor returns the canonical bucket name for a (metric, plan). We
// suffix rather than prefix so Prometheus rules grouping by metric can
// strip the trailing component cleanly.
func BucketFor(metric string, p Plan) string {
	if p == "" {
		p = PlanFree
	}
	return metric + "." + string(p)
}
