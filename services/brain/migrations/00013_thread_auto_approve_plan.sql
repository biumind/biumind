-- ============================================================================
-- 00013_thread_auto_approve_plan.sql — chat.threads.auto_approve check 约束
-- 扩档到含 'plan'（auto 权限模式 P1 遗留 2）
--
-- 原约束（00038，已 squash 进 00001 baseline）只认 'auto' | 'whitelist' |
-- 'manual'，composer chip 选 plan 时 PATCH 层 normalizeAutoApprove 只能把
-- 'plan' 降级落库为 'manual'。runtime 端共享权限内核已能消费 "plan"
-- （ModePlan 只读，29a9fff），约束扩档后 plan 档云端 task 以只读运行。
--
-- 本表不引用 files.objects，不涉及 FGC 孤儿扫描清单（orphan.go 无需动）。
-- ============================================================================

-- +goose Up

-- +goose StatementBegin
ALTER TABLE chat.threads DROP CONSTRAINT threads_auto_approve_chk;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE chat.threads ADD CONSTRAINT threads_auto_approve_chk
    CHECK (auto_approve IN ('auto', 'whitelist', 'manual', 'plan'));
-- +goose StatementEnd

-- +goose Down

-- 有 'plan' 行存在时 Down 会先撞新约束的旧形态 —— 先降级回 'manual'
-- 再收窄约束，保证可回滚。

-- +goose StatementBegin
UPDATE chat.threads SET auto_approve = 'manual' WHERE auto_approve = 'plan';
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE chat.threads DROP CONSTRAINT threads_auto_approve_chk;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE chat.threads ADD CONSTRAINT threads_auto_approve_chk
    CHECK (auto_approve IN ('auto', 'whitelist', 'manual'));
-- +goose StatementEnd
