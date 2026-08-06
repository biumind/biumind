package rss

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
)

// withOrgCaller injects a user claim that belongs to an org (and may be
// an admin), mirroring requireAuth's decoded claims.
func withOrgCaller(ctx context.Context, userID, orgID string, roles ...string) context.Context {
	return bauth.WithClaims(ctx, &bauth.Claims{
		UserID: userID, OrgID: orgID, Roles: roles,
	})
}

func TestOrgForcedFeeds_FlowsToMembers(t *testing.T) {
	a := newPGApp(t)
	// Allow org writes in the test (Cedar decision is unit-tested
	// separately in services/authz).
	a.WithAuthz(&stubAuthz{allow: true})

	orgID := "org-" + t.Name()
	admin := withOrgCaller(context.Background(), "admin-1", orgID, "admin")
	member := withOrgCaller(context.Background(), "member-1", orgID)

	// admin force-adds an org feed (no upstream → title falls back to URL)
	add, err := a.Invoke(admin, "org_feeds_force_add",
		json.RawMessage(`{"feed_url":"https://nonexistent.example/org.xml","title":"Org Feed"}`))
	if err != nil {
		t.Fatal(err)
	}
	feedID := add.(map[string]any)["id"].(string)
	if add.(map[string]any)["forced"] != true {
		t.Fatal("force-added feed not marked forced")
	}

	// a member's USER-scope feeds_list must include the forced org feed
	out, err := a.Invoke(member, "feeds_list", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	items := out.(map[string]any)["items"].([]map[string]any)
	var found map[string]any
	for _, it := range items {
		if it["id"] == feedID {
			found = it
		}
	}
	if found == nil {
		t.Fatal("forced org feed not visible to member")
	}
	if found["forced"] != true {
		t.Error("forced flag not surfaced to member")
	}

	// member cannot remove the forced feed via the ordinary path
	if _, err := a.Invoke(member, "feeds_remove",
		json.RawMessage(`{"id":"`+feedID+`"}`)); !errors.Is(err, ErrForcedFeed) {
		// member's user-scope delete won't match the org row, so this is
		// ErrNotFound OR ErrForcedFeed depending on scope — either way
		// the feed must survive. Accept both, but it must not succeed.
		if err == nil {
			t.Fatal("member removed a forced feed")
		}
	}

	// admin can remove it via the forced path
	if _, err := a.Invoke(admin, "org_feeds_force_remove",
		json.RawMessage(`{"id":"`+feedID+`"}`)); err != nil {
		t.Fatalf("admin force-remove failed: %v", err)
	}
}

func TestOrgForcedFeeds_NoAuthzFailsClosed(t *testing.T) {
	a := newPGApp(t) // no WithAuthz
	admin := withOrgCaller(context.Background(), "admin-1", "org-x", "admin")
	if _, err := a.Invoke(admin, "org_feeds_force_add",
		json.RawMessage(`{"feed_url":"https://x.example/o.xml"}`)); !errors.Is(err, ErrOrgScopeUnavailable) {
		t.Fatalf("want ErrOrgScopeUnavailable, got %v", err)
	}
}
