// Skills subsystem authorization (PS3.4).
//
// Translates Skill manifest.permissions[] strings into Cedar rules
// that gate the matching builtin tools at services/runtime/internal/
// agent/skill_tools.go. The runtime calls Authz.Check before
// invoking each high-risk tool; the rules below are the *only* place
// where skill permission semantics live (I9 — business code carries
// zero authorization logic).
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
