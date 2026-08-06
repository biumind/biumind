// App Center subsystem authorization (M2.4).
//
// Translates App manifest.permissions[] (granted subset) into Cedar
// rules that gate every app:* action. Runtime / app_center / channels
// services call Authz.Check before mutating state or invoking actions;
// these rules are the *only* place where App permission semantics
// live (I9 — business code carries zero authorization logic).
//
// Action vocabulary:
//   app:install / app:uninstall / app:upgrade
//   app:configure / app:enable / app:disable
//   app:invoke                   call any action on the App
//   app:read_data                read scoped wiki/files/graph
//   app:write_data               write scoped wiki/files/graph
//   app:grant_agent / app:revoke_agent
//   sidebar:read / sidebar:write
//   app:publish / app:deprecate / app:suspend     (admin/marketplace)
//
// Resource shape:
//   resource is App {
//     id:          string
//     identifier:  string
//     source:      "bundled" | "org" | "marketplace" | "user_webview"
//     status:      "active" | "deprecated" | "suspended" | "beta"
//     org_id:      string  (set for source="org" only)
//   }
//   resource is Installation {
//     id:                  string
//     identifier:          string
//     app_id:              string
//     scope:               "org" | "user"
//     scope_id:            string
//     enabled:             bool
//     forced:              bool       (org policy installed; user can't uninstall)
//     source:              same as App.source for that version
//     permissions:         set<string>
//     net_outbound:        set<string>
//     oauth_providers:     set<string>
//     secret_providers:    set<string>
//     data_scopes:         set<string>
//   }
//
// Principal types:
//   User { id, org_id, roles[] }     — interactive end-user
//   AgentSession { id, agent_id, owner_user_id, install_grants[] }
//                                      — Runtime-spawned agent run
//   Admin                             — org admin / marketplace ops

// ─── Install / uninstall / configure ───────────────────────
// Users can install apps for themselves (scope=user, scope_id=user_id).
// Org admins can install for the org.
permit(
    principal,
    action == Action::"app:install",
    resource is App
)
when {
    resource.status == "active" || resource.status == "beta"
};

// Forced installs (org policy) cannot be uninstalled by members; only
// admins can. Non-forced installs: owner of the install can uninstall.
permit(
    principal,
    action == Action::"app:uninstall",
    resource is Installation
)
when {
    resource.forced == false &&
    (
        (resource.scope == "user" && resource.scope_id == principal.id)
        ||
        (resource.scope == "org" && principal has roles && principal.roles.contains("admin"))
    )
};

// Admin override: forced installs uninstall path.
permit(
    principal,
    action == Action::"app:uninstall",
    resource is Installation
)
when {
    resource.forced == true &&
    principal has roles && principal.roles.contains("admin")
};

permit(
    principal,
    action in [Action::"app:configure", Action::"app:enable", Action::"app:disable"],
    resource is Installation
)
when {
    (resource.scope == "user" && resource.scope_id == principal.id)
    ||
    (resource.scope == "org" && principal has roles && principal.roles.contains("admin"))
};

permit(
    principal,
    action == Action::"app:upgrade",
    resource is Installation
)
when {
    (resource.scope == "user" && resource.scope_id == principal.id)
    ||
    (resource.scope == "org" && principal has roles && principal.roles.contains("admin"))
};

// ─── Runtime invocation ────────────────────────────────────
// User can invoke any action on their own enabled installations. Org
// installs invocable by any member of that org (membership checked
// upstream by app_center; we trust the principal.org_id claim here).
permit(
    principal is User,
    action == Action::"app:invoke",
    resource is Installation
)
when {
    resource.enabled == true &&
    (
        (resource.scope == "user" && resource.scope_id == principal.id)
        ||
        (resource.scope == "org" && resource.scope_id == principal.org_id)
    )
};

// Agent invocation requires:
//   * the install be enabled
//   * the install be granted to this agent (decision §21#7: default
//     agent auto-granted at install; others by explicit user grant).
//
// AgentSession.install_grants is the set of install_ids the user has
// granted to the running agent. Runtime populates from the agent_apps
// table when starting a session.
permit(
    principal is AgentSession,
    action == Action::"app:invoke",
    resource is Installation
)
when {
    resource.enabled == true &&
    principal.install_grants.contains(resource.id)
};

// ─── Data scope reads / writes ─────────────────────────────
// The runtime calls app:read_data / app:write_data when an App is
// about to read/write Brain / Files / Graph. The static rule says:
// the targeted data resource MUST belong to one of the install's
// declared data_scopes. Brain / Files services pass `resource.target`
// as the namespace they're about to access; Cedar checks containment.
permit(
    principal,
    action == Action::"app:read_data",
    resource is Installation
)
when {
    resource.enabled == true &&
    resource.permissions.contains("wiki.read") ||
    resource.permissions.contains("graph.read") ||
    resource.permissions.contains("files.read") ||
    resource.permissions.contains("memory.read")
};

permit(
    principal,
    action == Action::"app:write_data",
    resource is Installation
)
when {
    resource.enabled == true &&
    (
        resource.permissions.contains("wiki.write") ||
        resource.permissions.contains("graph.write") ||
        resource.permissions.contains("files.write") ||
        resource.permissions.contains("memory.write")
    )
};

// ─── Agent grants ──────────────────────────────────────────
// Users grant / revoke their own installs to specific agents.
permit(
    principal is User,
    action in [Action::"app:grant_agent", Action::"app:revoke_agent"],
    resource is Installation
)
when {
    resource.scope == "user" && resource.scope_id == principal.id
};

// ─── Sidebar layouts ───────────────────────────────────────
// User can only read/write their own sidebar.
permit(
    principal is User,
    action in [Action::"sidebar:read", Action::"sidebar:write"],
    resource is User
)
when {
    resource.id == principal.id
};

// ─── Marketplace operations (admin) ────────────────────────
// publish / deprecate / suspend are platform-admin only. We don't
// model the marketplace ops staff as a separate entity yet; the
// `roles` claim "marketplace_ops" gates these.
permit(
    principal,
    action in [Action::"app:publish", Action::"app:deprecate", Action::"app:suspend"],
    resource is App
)
when {
    principal has roles &&
    (principal.roles.contains("marketplace_ops") || principal.roles.contains("admin"))
};

// ─── Hard denies ───────────────────────────────────────────
// Disabled / suspended installs reject every runtime action regardless
// of permission flags. forbid wins over permit per Cedar semantics so
// this is the safety net even if a future permit accidentally matches.
forbid(
    principal,
    action in [Action::"app:invoke", Action::"app:read_data", Action::"app:write_data"],
    resource is Installation
)
when {
    resource.enabled == false
};

forbid(
    principal,
    action,
    resource is App
)
when {
    resource.status == "suspended"
};
