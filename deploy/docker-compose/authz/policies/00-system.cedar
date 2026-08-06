// 系统级策略（最高优先级；编译进 binary）
// 测试环境用文件加载；生产从 Postgres + LISTEN/NOTIFY 热加载

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
