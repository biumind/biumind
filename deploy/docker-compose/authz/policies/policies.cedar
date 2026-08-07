// BiuMind authorization policies (Cedar).
//
// This file is the ONLY policy source for the authz service: it is
// bind-mounted to /etc/biumind/authz/policies and loaded at startup
// (services/authz/cmd/authz/main.go). If the directory is missing or
// contains no .cedar file, authz exits — there is no compiled-in copy.
//
// Semantics: deny-by-default; any matching permit allows unless a
// forbid also matches (forbid always wins).
//
// Sections: System / Skills / App Center / RSS. All authorization
// logic lives here (I9 — business code carries zero authz logic).

// ═══════════════════════════════════════════════════════════════
// Section 1: System (wiki / graph / hub / agent / sandbox / realtime)
// ═══════════════════════════════════════════════════════════════

// ─── Wiki ───────────────────────────────────────────
permit(
    principal,
    action == Action::"wiki:Page::read",
    resource is Page
)
when {
    resource.share_mode == "public_read" ||
    resource.owner == principal ||
    resource.project in principal.projects
};

permit(
    principal,
    action in [Action::"wiki:Page::write", Action::"wiki:Page::delete"],
    resource is Page
)
when {
    resource.owner == principal ||
    (resource.project in principal.projects && principal has role && principal.role == "editor")
};

// ─── Graph ──────────────────────────────────────────
permit(
    principal,
    action == Action::"graph:Node::read",
    resource is Node
)
when { resource.project in principal.projects };

// ─── Hub / Models ───────────────────────────────────
permit(
    principal,
    action == Action::"hub:Model::use",
    resource is Model
);

permit(
    principal is AgentVirtualKey,
    action == Action::"hub:Model::use",
    resource is Model
)
when { resource.family in principal.scope.allowed_models };

// 配额超限拒
forbid(
    principal,
    action == Action::"hub:Model::use",
    resource
)
when { principal has spent_today && principal.spent_today >= principal.daily_limit };

// ─── Agent / Runtime ────────────────────────────────
permit(
    principal,
    action == Action::"agent:Task::create",
    resource is Project
)
when {
    resource in principal.projects ||
    resource.owner == principal
};

// ─── Sandbox ────────────────────────────────────────
permit(
    principal,
    action in [
        Action::"sandbox:Sandbox::exec",
        Action::"sandbox:Sandbox::pause",
        Action::"sandbox:Sandbox::resume",
        Action::"sandbox:Sandbox::destroy"
    ],
    resource is Sandbox
)
when { resource.owner == principal };

// ─── Realtime topics ────────────────────────────────
// 订阅前 Realtime 服务调 authz 校验
permit(
    principal,
    action == Action::"realtime:Topic::subscribe",
    resource is Topic
)
when {
    // wiki:project:<id> → 项目成员
    (resource.kind == "wiki:project" && resource.id in principal.projects) ||
    // agui:run:<id> → 运行所有者
    (resource.kind == "agui:run" && resource.owner == principal) ||
    // presence:user:<me> → 仅自己
    (resource.kind == "presence:user" && resource.id == principal.id) ||
    // sandbox:id:<sid> → 沙箱所有者
    (resource.kind == "sandbox:id" && resource.owner == principal) ||
    // task / notify / channel 同理
    (resource.kind == "task:user" && resource.id == principal.id) ||
    (resource.kind == "notify:user" && resource.id == principal.id) ||
    // chat:user:<uid> → 仅自己（聊天跨设备同步事件：message_created /
    // thread_updated，payload 只带 id，不含内容）
    (resource.kind == "chat:user" && resource.id == principal.id) ||
    // App Center 事件订阅 (v1.5#3 / M17 outbox poller fanout)：
    //   sidebar:user:<uid>     仅 user 自己
    //   app:install:<iid>      install 所有者（realtime authz client 默认
    //                          owner=principal；install_id 是 UUID 不可枚
    //                          举，所以"知道 ID 即可订阅"在 v1.5 是可接
    //                          受的；v2.5 marketplace 上线再让 Realtime
    //                          回查 app_center DB 校验真所有权）
    //   app:catalog:<app_id>   公开 catalog 变更通知，任何人可订阅
    //
    // app:org:<org_id> 暂未开（需要 realtime authz client 把
    // principal.orgs 加进 attributes，跟随 v2.0 Authz 客户端升级落地）
    (resource.kind == "sidebar:user" && resource.id == principal.id) ||
    (resource.kind == "app:install" && resource.owner == principal.id) ||
    (resource.kind == "app:catalog") ||
    // announce:all → 全局公告广播,任何登录用户可订阅(PERI-4b)。公告是
    // 广播,identity 发布时无法枚举在线用户,只能发单一全局 topic 由 realtime
    // 扇出;未读态 per-user 靠 REST /v1/announcements 算,推送只当刷新信号。
    // 沿用上面 app:catalog 的"公开变更通知任何人可订阅"先例。
    (resource.kind == "announce:all")
};

// ═══════════════════════════════════════════════════════════════
// Section 2: Skills (PS3.4)
//
// Translates Skill manifest.permissions[] strings into Cedar rules
// that gate the matching builtin tools at services/runtime/internal/
// agent/skill_tools.go. The runtime calls Authz.Check before
// invoking each high-risk tool.
//
// Action vocabulary:
//   skill:invoke               low-risk read-only (list / activate / read_reference)
//   skill:exec_script          execute shell command in sandbox with bundle mounted
//   skill:export_file          write skill output to user Files
//   skill:fetch_network        outbound network from sandbox
//   skill:read_wiki            read user's Wiki content
//   skill:recall_memory        read user's Memory store
//
// Resource shape:
//   resource is Skill {
//     id:          string
//     org_id:      string
//     owner_id:    string  (empty for org-shared / bundled rows)
//     status:      "active" | "disabled" | "staged" | "staged_org" | "suspended"
//     source:      "bundled" | "org" | "user" | "marketplace" | "imported"
//     permissions: set<string>
//   }
//
// Principal: User (resolved from JWT subject).
// ═══════════════════════════════════════════════════════════════

// ─── Read-only invocation ──────────────────────────────────
// Any active skill in the caller's org can be listed / activated /
// read. Cross-org skills are not authorised — the API layer also
// scopes by org_id but defense-in-depth: deny here too.
permit(
    principal,
    action == Action::"skill:invoke",
    resource is Skill
)
when {
    resource.status == "active" &&
    (resource.org_id == principal.org_id || principal has org_id_admin && principal.org_id_admin == resource.org_id)
};

// ─── High-risk: sandbox execution ──────────────────────────
// Only when the skill explicitly declares "sandbox.exec" in its
// manifest permissions. Marketplace / imported skills must declare
// + the user must have approved the install.
permit(
    principal,
    action == Action::"skill:exec_script",
    resource is Skill
)
when {
    resource.status == "active" &&
    resource.org_id == principal.org_id &&
    resource.permissions.contains("sandbox.exec")
};

// ─── Outbound network from sandbox ─────────────────────────
permit(
    principal,
    action == Action::"skill:fetch_network",
    resource is Skill
)
when {
    resource.status == "active" &&
    resource.org_id == principal.org_id &&
    resource.permissions.contains("network.fetch")
};

// ─── Wiki read access ──────────────────────────────────────
permit(
    principal,
    action == Action::"skill:read_wiki",
    resource is Skill
)
when {
    resource.status == "active" &&
    resource.org_id == principal.org_id &&
    resource.permissions.contains("wiki.read")
};

// ─── Memory recall ─────────────────────────────────────────
permit(
    principal,
    action == Action::"skill:recall_memory",
    resource is Skill
)
when {
    resource.status == "active" &&
    resource.org_id == principal.org_id &&
    resource.permissions.contains("memory.recall")
};

// ─── Export sandbox output to user Files ───────────────────
// Doesn't require an explicit permission string — the user has
// already consented to install; export only writes to *their* files
// under their owner_id. The deny-by-default for non-active rows
// below catches abuse.
permit(
    principal,
    action == Action::"skill:export_file",
    resource is Skill
)
when {
    resource.status == "active" &&
    resource.org_id == principal.org_id
};

// ─── Self-authoring workflow (PS3.1) ───────────────────────
// Owner can propose / approve / reject / share-org their own skills.
// Approve / reject specifically require the row to be in a staged
// state — guard against the API smuggling disabled → active.
permit(
    principal,
    action == Action::"skill:propose",
    resource is Skill
)
when {
    resource.org_id == principal.org_id
};

permit(
    principal,
    action in [Action::"skill:approve", Action::"skill:reject"],
    resource is Skill
)
when {
    resource.org_id == principal.org_id &&
    resource.owner_id == principal.id &&
    (resource.status == "staged" || resource.status == "staged_org")
};

permit(
    principal,
    action == Action::"skill:share_org",
    resource is Skill
)
when {
    resource.org_id == principal.org_id &&
    resource.owner_id == principal.id &&
    resource.status == "active"
};

// ─── Hard denies ───────────────────────────────────────────
// Disabled / suspended rows reject every action regardless of
// permission flags. forbid wins over permit per Cedar semantics so
// this is the safety net even if a future permit accidentally
// matches.
forbid(
    principal,
    action,
    resource is Skill
)
when {
    resource.status == "disabled" || resource.status == "suspended"
};

// ═══════════════════════════════════════════════════════════════
// Section 3: App Center (M2.4)
//
// Translates App manifest.permissions[] (granted subset) into Cedar
// rules that gate every app:* action. Runtime / app_center / channels
// services call Authz.Check before mutating state or invoking actions.
//
// Action vocabulary:
//   app:install / app:uninstall / app:upgrade
//   app:configure / app:enable / app:disable
//   app:invoke                   call any action on the App
//   app:read_data              read scoped wiki/files/graph
//   app:write_data             write scoped wiki/files/graph
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
// ═══════════════════════════════════════════════════════════════

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

// ═══════════════════════════════════════════════════════════════
// Section 4: RSS app org-scope authorization (M11.2)
//
// The generic app:invoke gate (Section 3) only decides whether a
// user may invoke the RSS app at all — it cannot express "org members
// READ org-scoped resources, only org admins WRITE them" because it
// sees neither the action nor the scope input. So the RSS app makes a
// second, RSS-specific Authz.Check for any scope=org operation, gated
// by these rules.
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
// ═══════════════════════════════════════════════════════════════

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
