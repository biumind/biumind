-- +goose Up
-- +goose StatementBegin

-- ─── Chat: thread workdir + auto_approve ─────────────────────
--
-- 设计文档: docs/BiuMind-Chat-UI-Benchmark-Optimization.md (业内第一系列)
--
-- 两个新字段都是为 Agent 模式的"行内可切换"UX 服务:
--
-- workdir         — agent / task 模式时,daemon spawn 的 working directory。
--                   也是 PermissionUpdate.AddDirectories 协议的初始值。
--                   chat 模式 NULL。daemon 端 worker.go 收到 work payload 后
--                   chdir + 注入到 biumindkit Options.Cwd。
--                   安全门: daemon 启动时 --allowed-roots flag 限制可暴露的
--                   根路径; brain 不能让 daemon 跑 ~/.ssh 等敏感目录。
--                   (allowed-roots 协商在后续 migration 加 capabilities 字段)
--
-- auto_approve    — Agent 工具调用的自治程度,三档:
--                     'auto'      — 全自动 allow (相当于上游 bypassPermissions)
--                     'whitelist' — 命中规则即 allow,否则弹 client 询问
--                     'manual'    — 每次都问 (默认; 安全优先)
--                   client 端在 BiuSessionConnection 拦截 SDKControlRequest
--                   {can_use_tool},按这个字段决定立即应答 or 弹 ApprovalCard。
--                   规则白名单复用上游 settings.PersistPermissionUpdate
--                   (PermissionUpdate.AddRules + destination='session') 已有
--                   能力,本表不用 thread_skill_approvals 子表。
--
-- 都默认 NULL / 'manual' —— 老 thread 升级后不破坏行为。

ALTER TABLE chat.threads
    ADD COLUMN IF NOT EXISTS workdir TEXT;

ALTER TABLE chat.threads
    ADD COLUMN IF NOT EXISTS auto_approve TEXT NOT NULL DEFAULT 'manual';

-- CHECK 约束守住三态。Postgres 16 没有 ADD CONSTRAINT IF NOT EXISTS,用
-- DO block 让本 migration 在 rerun 时不炸。
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'threads_auto_approve_chk'
    ) THEN
        ALTER TABLE chat.threads
            ADD CONSTRAINT threads_auto_approve_chk
                CHECK (auto_approve IN ('auto', 'whitelist', 'manual'));
    END IF;
END $$;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE chat.threads
    DROP CONSTRAINT IF EXISTS threads_auto_approve_chk;

ALTER TABLE chat.threads
    DROP COLUMN IF EXISTS auto_approve;

ALTER TABLE chat.threads
    DROP COLUMN IF EXISTS workdir;

-- +goose StatementEnd
