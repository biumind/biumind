// M11.2 — org-scope authorization boundary.
//
// The RSS data layer already takes (scope, scopeID) everywhere; M11
// turns on the "org" scope. The one rule the existing app:invoke Cedar
// gate cannot express is "org members may READ org-scoped resources,
// only org admins may WRITE them" — app:invoke only decides whether a
// user can invoke the app at all, not which action or scope.
//
// So org reads/writes consult a *separate* Cedar decision via this
// AuthzChecker. The interface lives here (the App depends on it, like
// RadarStore / WikiSink); app_center provides the HTTP-backed adapter
// that shapes principal (User{id,org_id,roles}) + resource (RssOrg
// {org_id}) + action (rss:org_read|rss:org_write) and POSTs to Authz.
// Authorization logic stays entirely in policies.cedar RSS 节 (I9), not here.

package rss

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
)

// scopeOf extracts just the optional "scope" field from an action's raw
// input ("" when absent / unparsable). Lets scope-aware handlers stay
// minimal — they don't all need a Scope field threaded into their own
// input struct, they just call resolveScope(ctx, scopeOf(raw), write).
func scopeOf(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var probe struct {
		Scope string `json:"scope"`
	}
	_ = json.Unmarshal(raw, &probe)
	return probe.Scope
}

// ErrOrgScopeUnavailable is returned when an org-scoped operation is
// requested but no AuthzChecker is wired. We fail closed rather than
// silently allow — bypassing the org boundary is exactly the kind of
// thing CLAUDE.md forbids.
var ErrOrgScopeUnavailable = errors.New("rss: org scope requires authz (not wired)")

// ErrOrgForbidden is returned when Authz denies the org operation
// (e.g. a non-admin member attempting an org write).
var ErrOrgForbidden = errors.New("rss: org operation not permitted")

// AuthzChecker decides whether the caller may read or write org-scoped
// RSS resources. Implementations call the central Authz service; the
// decision is governed by policies.cedar RSS 节.
type AuthzChecker interface {
	// AuthorizeOrg returns nil when permitted, ErrOrgForbidden on deny,
	// or a wrapped transport error (callers treat as fail-closed).
	AuthorizeOrg(ctx context.Context, claims *bauth.Claims, write bool) error
}

// WithAuthz wires the org-scope authorizer. Optional; when nil, any
// org-scoped operation fails with ErrOrgScopeUnavailable.
func (a *App) WithAuthz(c AuthzChecker) *App {
	a.authz = c
	return a
}

// WithShareBaseURL sets the public base URL used to build share links
// (e.g. https://app.biumind.io → https://app.biumind.io/share/rss/{token}).
// Wired in app_center main.go from APP_CENTER_BASE_URL.
func (a *App) WithShareBaseURL(base string) *App {
	a.shareBaseURL = base
	return a
}

// callerClaims returns the verified caller claims or ErrNoCaller.
func callerClaims(ctx context.Context) (*bauth.Claims, error) {
	claims, ok := bauth.ClaimsFrom(ctx)
	if !ok || claims == nil || claims.UserID == "" {
		return nil, ErrNoCaller
	}
	return claims, nil
}

// authorizeOrg is the single gate every org-scoped handler calls after
// deriving org scope. write=false for list/read actions, true for
// create/update/delete/force.
func (a *App) authorizeOrg(ctx context.Context, write bool) error {
	if a.authz == nil {
		return ErrOrgScopeUnavailable
	}
	claims, ok := bauth.ClaimsFrom(ctx)
	if !ok || claims == nil {
		return ErrNoCaller
	}
	return a.authz.AuthorizeOrg(ctx, claims, write)
}

// resolveScope derives the (scope, scopeID) tuple for a handler that
// accepts an optional input.scope. This is the M11 replacement for the
// hard-coded callerScope in scope-aware handlers:
//
//   - "" / "user" → the caller's own user scope. No authz (a user always
//     owns their own data).
//   - "org"       → derive scope_id from claims.OrgID and consult Authz
//     (write=false for reads, true for mutations). org members read;
//     only org admins write — enforced entirely in policies.cedar RSS 节.
//
// Fails closed: org requested without an org claim, or without a wired
// authz checker, returns an error rather than silently downgrading to
// user scope.
func (a *App) resolveScope(ctx context.Context, requestedScope string, write bool) (string, string, error) {
	claims, ok := bauth.ClaimsFrom(ctx)
	if !ok || claims == nil || claims.UserID == "" {
		return "", "", ErrNoCaller
	}
	switch requestedScope {
	case "", "user":
		return "user", claims.UserID, nil
	case "org":
		if claims.OrgID == "" {
			return "", "", fmt.Errorf("rss: org scope requested but caller has no org")
		}
		if err := a.authorizeOrg(ctx, write); err != nil {
			return "", "", err
		}
		return "org", claims.OrgID, nil
	default:
		return "", "", fmt.Errorf("rss: unknown scope %q", requestedScope)
	}
}
