// context.go — request-scoped channel handle.
//
// /v1/messages handler can't easily reach into the resolver to know
// which channel was selected; the resolver picks the channel before
// the adaptor runs. We stash the resolved channel in the request ctx
// so reportUsage can later pull it out and call supervisor.RecordSuccess
// / RecordFailure + write the dual-currency usage_log row.
//
// Pattern mirrors files.WithBearerToken from internal/relay/files —
// keeps the dependency between layers one-way (handler reads ctx,
// doesn't import router).

package router

import (
	"context"
	"time"

	"github.com/biumind/biumind/services/model-relay/internal/registry"
)

type ctxKey int

const (
	ctxKeyResolveOutput ctxKey = iota
	ctxKeyUpstreamFailure
)

// UpstreamFailure carries the upstream HTTP error metadata from the handler
// (which sees the raw *http.Response) down to OnRequestComplete (R4-B), so
// failover can classify 429/401/402/5xx and honor Retry-After. StatusCode 0
// = no HTTP status (network error). RetryAfter 0 = header absent/unparseable.
type UpstreamFailure struct {
	StatusCode int
	RetryAfter time.Duration
}

// WithUpstreamFailure stamps upstream error metadata onto the request ctx at
// the >=400 branch, before fireComplete → OnRequestComplete.
func WithUpstreamFailure(ctx context.Context, f UpstreamFailure) context.Context {
	return context.WithValue(ctx, ctxKeyUpstreamFailure, f)
}

// UpstreamFailureFrom recovers the upstream error metadata. (zero, false)
// when absent (success path, or failure with no HTTP response).
func UpstreamFailureFrom(ctx context.Context) (UpstreamFailure, bool) {
	v, ok := ctx.Value(ctxKeyUpstreamFailure).(UpstreamFailure)
	return v, ok
}

// WithResolveOutput stamps the resolver's full output onto a context
// so the downstream handler can recover the channel + provider info
// after the request completes.
func WithResolveOutput(ctx context.Context, out *ResolveOutput) context.Context {
	return context.WithValue(ctx, ctxKeyResolveOutput, out)
}

// ResolveOutputFrom retrieves the resolver output stamped earlier.
// Returns (nil, false) when the legacy env-driven path is in effect or
// the request bypassed the resolver.
func ResolveOutputFrom(ctx context.Context) (*ResolveOutput, bool) {
	v, ok := ctx.Value(ctxKeyResolveOutput).(*ResolveOutput)
	if !ok || v == nil {
		return nil, false
	}
	return v, true
}

// Convenience for handler code that only cares about the channel.
func ChannelFrom(ctx context.Context) (*registry.Channel, bool) {
	out, ok := ResolveOutputFrom(ctx)
	if !ok {
		return nil, false
	}
	return out.Channel, true
}
