package engine

import (
	"os"
	"path/filepath"
	"testing"
)

// rssEngine loads deploy/.../policies/policies.cedar so the test always
// runs against the shipped policy, not a string copy.
func rssEngine(t *testing.T) *Engine {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(wd, "..", "..", "..", "..")
	p := filepath.Join(root, "deploy", "docker-compose", "authz", "policies", "policies.cedar")
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read policies.cedar: %v", err)
	}
	e := New()
	if err := e.LoadPolicies(raw); err != nil {
		t.Fatalf("load policies.cedar: %v", err)
	}
	return e
}

func rssOrgResource(orgID string) Entity {
	return Entity{
		Type: "RssOrg", ID: orgID,
		Attributes: map[string]any{"org_id": orgID},
	}
}

// ─── rss:org_read — any member ─────────────────────────────

func TestRSSOrgRead_MemberAllowed(t *testing.T) {
	e := rssEngine(t)
	res, err := e.Check(Input{
		Principal: userPrincipalForApps("u-1", "org-A"), // no admin role
		Action:    "rss:org_read",
		Resource:  rssOrgResource("org-A"),
	})
	assertDecision(t, res, err, DecisionAllow, "member reads own org")
}

func TestRSSOrgRead_OtherOrgDenied(t *testing.T) {
	e := rssEngine(t)
	res, err := e.Check(Input{
		Principal: userPrincipalForApps("u-1", "org-A"),
		Action:    "rss:org_read",
		Resource:  rssOrgResource("org-B"), // different org
	})
	assertDecision(t, res, err, DecisionDeny, "member reads other org")
}

// ─── rss:org_write — admins only ───────────────────────────

func TestRSSOrgWrite_AdminAllowed(t *testing.T) {
	e := rssEngine(t)
	res, err := e.Check(Input{
		Principal: userPrincipalForApps("u-1", "org-A", "admin"),
		Action:    "rss:org_write",
		Resource:  rssOrgResource("org-A"),
	})
	assertDecision(t, res, err, DecisionAllow, "admin writes own org")
}

func TestRSSOrgWrite_MemberDenied(t *testing.T) {
	e := rssEngine(t)
	res, err := e.Check(Input{
		Principal: userPrincipalForApps("u-1", "org-A"), // member, no admin role
		Action:    "rss:org_write",
		Resource:  rssOrgResource("org-A"),
	})
	assertDecision(t, res, err, DecisionDeny, "member writes own org")
}

func TestRSSOrgWrite_AdminOtherOrgDenied(t *testing.T) {
	e := rssEngine(t)
	res, err := e.Check(Input{
		Principal: userPrincipalForApps("u-1", "org-A", "admin"),
		Action:    "rss:org_write",
		Resource:  rssOrgResource("org-B"),
	})
	assertDecision(t, res, err, DecisionDeny, "admin writes other org")
}
