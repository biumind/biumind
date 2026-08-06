-- 移除编码任务的云端持久化(D4 / Code-I4 / Code-I6)。
--
-- 编码任务 100% 本地(客户端 Drift 即唯一真相源)。旧 codeSync 设计(00010 建的
-- code schema + tasks/task_events/task_artifacts/task_commands 四表 + /v1/code/tasks*
-- endpoints)已废弃并从客户端 + 服务器移除。远控将走 Runtime v3 agent-plane(M6),
-- 不复用此路径。
--
-- DROP SCHEMA ... CASCADE 一并清掉 schema 下全部表 / 索引 / 约束。

-- +goose Up
-- +goose StatementBegin
DROP SCHEMA IF EXISTS code CASCADE;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- 不可逆:编码任务已确立为纯本地,不恢复云端表。需要时回滚到 00010 重建。
SELECT 1;
-- +goose StatementEnd
