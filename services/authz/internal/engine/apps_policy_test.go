package engine

import (
	"os"
	"path/filepath"
	"testing"
)

// appsPolicyPath resolves deploy/.../policies/20-apps.cedar so test
// drift between filesystem and string-literal copies is impossible.
func appsPolicyPath(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(wd, "..", "..", "..", "..")
	p := filepath.Join(root, "deploy", "docker-compose", "authz", "policies", "20-apps.cedar")
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("policy file %s not found: %v", p, err)
	}
	return p
}

func appsEngine(t *testing.T) *Engine {
	t.Helper()
	raw, err := os.ReadFile(appsPolicyPath(t))
	if err != nil {
		t.Fatal(err)
	}
	e := New()
	if err := e.LoadPolicies(raw); err != nil {
		t.Fatalf("load 20-apps.cedar: %v", err)
	}
	return e
}

// ─── Builders ─────────────────────────────────────────────

func userPrincipalForApps(uid, orgID string, roles ...string) Entity {
	attrs := map[string]any{
		"id":     uid,
		"org_id": orgID,
	}
	if len(roles) > 0 {
		attrs["roles"] = stringSet(roles)
	}
	return Entity{Type: "User", ID: uid, Attributes: attrs}
}

func agentPrincipal(agentID, ownerUID string, grants []string) Entity {
	return Entity{
		Type: "AgentSession", ID: agentID,
		Attributes: map[string]any{
			"id":             agentID,
			"agent_id":       agentID,
			"owner_user_id":  ownerUID,
			"install_grants": stringSet(grants),
		},
	}
}

func appResource(appID, status, source string) Entity {
	return Entity{
		Type: "App", ID: appID,
		Attributes: map[string]any{
			"id":         appID,
			"identifier": appID,
			"status":     status,
			"source":     source,
		},
	}
}

func installResource(id, scope, scopeID string, enabled, forced bool, perms []string) Entity {
	return Entity{
		Type: "Installation", ID: id,
		Attributes: map[string]any{
			"id":               id,
			"identifier":       "rss",
			"app_id":           "app_x",
			"scope":            scope,
			"scope_id":         scopeID,
			"enabled":          enabled,
			"forced":           forced,
			"source":           "marketplace",
			"version":          "0.2.0",
			"permissions":      stringSet(perms),
			"net_outbound":     stringSet(nil),
			"oauth_providers":  stringSet(nil),
			"secret_providers": stringSet(nil),
			"data_scopes":      stringSet(nil),
		},
	}
}

// ─── app:install ──────────────────────────────────────────

func TestAppInstall_AllowsForActive(t *testing.T) {
	e := appsEngine(t)
	res, err := e.Check(Input{
		Principal: userPrincipalForApps("u-1", "org-A"),
		Action:    "app:install",
		Resource:  appResource("app_x", "active", "marketplace"),
	})
	assertDecision(t, res, err, DecisionAllow, "install active marketplace app")
}

func TestAppInstall_DeniesSuspended(t *testing.T) {
	e := appsEngine(t)
	res, err := e.Check(Input{
		Principal: userPrincipalForApps("u-1", "org-A"),
		Action:    "app:install",
		Resource:  appResource("app_x", "suspended", "marketplace"),
	})
	assertDecision(t, res, err, DecisionDeny, "install suspended app")
}

// ─── app:uninstall ────────────────────────────────────────

func TestAppUninstall_OwnerCanUninstallNonForced(t *testing.T) {
	e := appsEngine(t)
	res, err := e.Check(Input{
		Principal: userPrincipalForApps("u-1", "org-A"),
		Action:    "app:uninstall",
		Resource:  installResource("i-1", "user", "u-1", true, false, nil),
	})
	assertDecision(t, res, err, DecisionAllow, "owner uninstall non-forced")
}

func TestAppUninstall_NonOwnerDenied(t *testing.T) {
	e := appsEngine(t)
	res, err := e.Check(Input{
		Principal: userPrincipalForApps("u-2", "org-A"),
		Action:    "app:uninstall",
		Resource:  installResource("i-1", "user", "u-1", true, false, nil),
	})
	assertDecision(t, res, err, DecisionDeny, "non-owner uninstall")
}

func TestAppUninstall_ForcedDeniedForUser(t *testing.T) {
	e := appsEngine(t)
	res, err := e.Check(Input{
		Principal: userPrincipalForApps("u-1", "org-A"),
		Action:    "app:uninstall",
		Resource:  installResource("i-1", "user", "u-1", true, true /* forced */, nil),
	})
	assertDecision(t, res, err, DecisionDeny, "forced install user-uninstall")
}

func TestAppUninstall_ForcedAllowedForAdmin(t *testing.T) {
	e := appsEngine(t)
	res, err := e.Check(Input{
		Principal: userPrincipalForApps("u-1", "org-A", "admin"),
		Action:    "app:uninstall",
		Resource:  installResource("i-1", "org", "org-A", true, true, nil),
	})
	assertDecision(t, res, err, DecisionAllow, "admin uninstall forced")
}

// ─── app:invoke ────────────────────────────────────────────

func TestAppInvoke_UserCanCallOwn(t *testing.T) {
	e := appsEngine(t)
	res, err := e.Check(Input{
		Principal: userPrincipalForApps("u-1", "org-A"),
		Action:    "app:invoke",
		Resource:  installResource("i-1", "user", "u-1", true, false, []string{"hub.invoke"}),
	})
	assertDecision(t, res, err, DecisionAllow, "user invoke own install")
}

func TestAppInvoke_DisabledDenied(t *testing.T) {
	e := appsEngine(t)
	res, err := e.Check(Input{
		Principal: userPrincipalForApps("u-1", "org-A"),
		Action:    "app:invoke",
		Resource:  installResource("i-1", "user", "u-1", false /* disabled */, false, nil),
	})
	assertDecision(t, res, err, DecisionDeny, "disabled install invoke")
}

func TestAppInvoke_AgentNeedsGrant(t *testing.T) {
	e := appsEngine(t)
	res, err := e.Check(Input{
		Principal: agentPrincipal("a-1", "u-1", []string{"i-1"}),
		Action:    "app:invoke",
		Resource:  installResource("i-1", "user", "u-1", true, false, nil),
	})
	assertDecision(t, res, err, DecisionAllow, "agent invoke with grant")
}

func TestAppInvoke_AgentWithoutGrantDenied(t *testing.T) {
	e := appsEngine(t)
	res, err := e.Check(Input{
		Principal: agentPrincipal("a-1", "u-1", []string{"i-other"}),
		Action:    "app:invoke",
		Resource:  installResource("i-1", "user", "u-1", true, false, nil),
	})
	assertDecision(t, res, err, DecisionDeny, "agent invoke missing grant")
}

// ─── Data scope reads ─────────────────────────────────────

func TestAppReadData_RequiresPermission(t *testing.T) {
	e := appsEngine(t)
	res, err := e.Check(Input{
		Principal: userPrincipalForApps("u-1", "org-A"),
		Action:    "app:read_data",
		Resource:  installResource("i-1", "user", "u-1", true, false, []string{"wiki.read"}),
	})
	assertDecision(t, res, err, DecisionAllow, "read_data with wiki.read")
}

func TestAppReadData_DeniedWithoutPermission(t *testing.T) {
	e := appsEngine(t)
	res, err := e.Check(Input{
		Principal: userPrincipalForApps("u-1", "org-A"),
		Action:    "app:read_data",
		Resource:  installResource("i-1", "user", "u-1", true, false, nil),
	})
	assertDecision(t, res, err, DecisionDeny, "read_data without permission")
}

func TestAppWriteData_RequiresWritePermission(t *testing.T) {
	e := appsEngine(t)
	res, err := e.Check(Input{
		Principal: userPrincipalForApps("u-1", "org-A"),
		Action:    "app:write_data",
		Resource:  installResource("i-1", "user", "u-1", true, false, []string{"wiki.read"}),
	})
	// Has wiki.read but not write.
	assertDecision(t, res, err, DecisionDeny, "write_data with only read permission")
}

// ─── sidebar:read / sidebar:write ─────────────────────────

func TestSidebar_OwnerCanReadWrite(t *testing.T) {
	e := appsEngine(t)
	user := userPrincipalForApps("u-1", "org-A")
	for _, action := range []string{"sidebar:read", "sidebar:write"} {
		res, err := e.Check(Input{
			Principal: user,
			Action:    action,
			Resource:  Entity{Type: "User", ID: "u-1", Attributes: map[string]any{"id": "u-1"}},
		})
		assertDecision(t, res, err, DecisionAllow, action+" own")
	}
}

func TestSidebar_OtherUserDenied(t *testing.T) {
	e := appsEngine(t)
	res, err := e.Check(Input{
		Principal: userPrincipalForApps("u-1", "org-A"),
		Action:    "sidebar:read",
		Resource:  Entity{Type: "User", ID: "u-2", Attributes: map[string]any{"id": "u-2"}},
	})
	assertDecision(t, res, err, DecisionDeny, "sidebar:read other user")
}
