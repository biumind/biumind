// RSS app org-scope authorization (M11.2).
//
// The generic app:invoke gate (20-apps.cedar) only decides whether a
// user may invoke the RSS app at all — it cannot express "org members
// READ org-scoped resources, only org admins WRITE them" because it
// sees neither the action nor the scope input. So the RSS app makes a
// second, RSS-specific Authz.Check for any scope=org operation, gated
// by these rules. Authorization logic for the org boundary lives ONLY
// here (I9 — business code carries zero authz logic).
//
// Action vocabulary:
//   rss:org_read    list / read org-scoped feeds, rules, hits
//   rss:org_write   create / update / delete / force org-scoped rsrcs
//
// Principal:
//   User { id, org_id: string, roles: set<string> }
//
// Resource:
//   RssOrg { org_id: string }   — the org tenant being acted on
//
// The app derives org_id from the caller's verified JWT org_id claim
// and sets resource.org_id to the same value; the policy still checks
// principal.org_id == resource.org_id defensively so a future caller
// that targets a different org is denied.

// ─── Read: any member of the org ───────────────────────────
permit(
    principal is User,
    action == Action::"rss:org_read",
    resource is RssOrg
)
when {
    resource.org_id != "" &&
    principal has org_id &&
    principal.org_id == resource.org_id
};

// ─── Write: org admins only ────────────────────────────────
permit(
    principal is User,
    action == Action::"rss:org_write",
    resource is RssOrg
)
when {
    resource.org_id != "" &&
    principal has org_id &&
    principal.org_id == resource.org_id &&
    principal has roles &&
    principal.roles.contains("admin")
};
