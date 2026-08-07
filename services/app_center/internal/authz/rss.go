package authz

import (
	"context"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/biumind/biumind/packages/go-sdk/biu/biuapp/rss"
)

// RSSOrgChecker adapts a Decider into the rss.AuthzChecker interface the
// RSS BiuApp depends on. It shapes the Cedar request — principal
// User{id,org_id,roles}, resource RssOrg{org_id}, action
// rss:org_read|rss:org_write — and maps the decision back to nil /
// rss.ErrOrgForbidden. Authorization logic itself lives in policies.cedar RSS 节
// (I9); this is pure plumbing.
//
// Fail-closed: any transport error or non-ALLOW decision denies.
type RSSOrgChecker struct {
	D Decider
}

// AuthorizeOrg implements rss.AuthzChecker.
func (c RSSOrgChecker) AuthorizeOrg(ctx context.Context, claims *bauth.Claims, write bool) error {
	if claims == nil || claims.OrgID == "" {
		return rss.ErrOrgForbidden
	}
	action := "rss:org_read"
	if write {
		action = "rss:org_write"
	}
	roles := claims.Roles
	if roles == nil {
		roles = []string{}
	}
	res, err := c.D.Check(ctx, Request{
		Principal: Entity{
			Type: "User",
			ID:   claims.UserID,
			Attributes: map[string]any{
				"org_id": claims.OrgID,
				"roles":  roles,
			},
		},
		Action: action,
		Resource: Entity{
			Type:       "RssOrg",
			ID:         claims.OrgID,
			Attributes: map[string]any{"org_id": claims.OrgID},
		},
	})
	if err != nil {
		// Treat transport failure as deny — a transient Authz outage must
		// not silently permit an org write.
		return rss.ErrOrgForbidden
	}
	if res.Decision != Allow {
		return rss.ErrOrgForbidden
	}
	return nil
}

var _ rss.AuthzChecker = RSSOrgChecker{}
