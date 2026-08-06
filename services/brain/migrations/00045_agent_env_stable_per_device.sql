-- +goose Up
-- +goose StatementBegin

-- Runtime v3 R9：env_id 按 device 稳定化。
--
-- 此前每次 daemon 重启 register 都 uuid.New() 造新 env_id（env_id churn），派生
-- 三个问题：① 旧 env_id 下的在飞 work 成孤儿——work 路由绑 biu.work.<envID>、
-- worker 用 durable worker-<envID> 拉取，JetStream redeliver 只投同名 durable，
-- 重启后的新 env_id 拉不到旧 subject 的消息（janitor.go 开头声称的「in-flight
-- 靠 JetStream redeliver」前提被 churn 证伪）；② worker-<envID> durable consumer
-- + agent_environments 行随每次重启泄漏；③ re-register 风暴曾 1h 堆 396 万行。
--
-- 修复：register 对有 device_id 的注册按 device_id UPSERT 复用既有 env_id（见
-- store.RegisterEnvironment）。重启回到同 env_id → 同 durable → 旧在飞 work 被
-- AckWait 自动 redeliver 给重连 worker。runtime 池 / PAT / JWT（device_id IS NULL）
-- 不受约束，保持每行独立。

-- Step 1：折叠历史重复 —— 每个 device_id 只保留 last_seen_at 最新的一行。
-- agent_sessions.environment_id FK 是 ON DELETE SET NULL（00033），被删的都是
-- 老/死 env，关联 session 已终态，置空无害。
DELETE FROM agent_environments e
 USING (
     SELECT device_id, max(last_seen_at) AS keep_seen
       FROM agent_environments
      WHERE device_id IS NOT NULL
      GROUP BY device_id
 ) latest
 WHERE e.device_id = latest.device_id
   AND e.last_seen_at < latest.keep_seen;

-- 极端并列（同 device_id 多行同 last_seen_at）：按 environment_id 兜底再去重，
-- 保证 Step 2 的 UNIQUE 一定能建上。
DELETE FROM agent_environments e
 USING agent_environments k
 WHERE e.device_id IS NOT NULL
   AND e.device_id = k.device_id
   AND e.last_seen_at = k.last_seen_at
   AND e.environment_id < k.environment_id;

-- Step 2：partial unique —— 一台 device 至多一行 environment。
CREATE UNIQUE INDEX IF NOT EXISTS agent_environments_device_uniq
    ON agent_environments (device_id) WHERE device_id IS NOT NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS agent_environments_device_uniq;
-- 历史重复行不恢复（已物理删，无回溯价值）。
-- +goose StatementEnd
