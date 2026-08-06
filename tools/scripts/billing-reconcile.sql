-- W3-8 财务对账 SQL
--
-- 目的: 验证两条记账路径在 [from_ts, to_ts) 窗口内一致.
--
--   路径 A (DB 真相): identity.credit_logs (Consume / Refund 写)
--                    + identity.credit_holds (Settle 写 settle_log via Consume 路径)
--                    每条 log 对应一次扣减或退款, 是积分变动的最终账本.
--
--   路径 B (事件审计): billing.events (publisher → sink → PG, 是路径 A 的 NATS 镜像)
--                      kind='consume' / 'refund' / 'settle' 应当一一对应路径 A 的
--                      credit_logs (settle 走 Consume 内部, 写一条 log).
--
-- 期望: 路径 A 的 (delta) 总额 与 路径 B 的 (amount) 总额 偏差 < 0.01% (绝对值
--       < 1 积分时 0% 也合理). 偏差超阈值 → 排查 NATS 丢消息 / sink stop /
--       publisher 失败.
--
-- 使用:
--   docker exec -i biu-postgres psql -U biumind -d biu_core \
--     -v from_ts="'2026-06-07T00:00:00Z'" \
--     -v to_ts="'2026-06-08T00:00:00Z'" \
--     < tools/scripts/billing-reconcile.sql
--
-- 部署: 每日 00:05 cron 跑昨天的窗口, 偏差 > 0.01% 时报警 (通过 cron 退出码 1).

\set from_ts :'from_ts'
\set to_ts   :'to_ts'

\echo '=== 1. credit_logs (路径 A) 总扣减 ==='
SELECT
    count(*) AS log_count,
    COALESCE(sum(CASE WHEN delta < 0 THEN -delta ELSE 0 END), 0) AS consume_credits,
    COALESCE(sum(CASE WHEN delta > 0 THEN  delta ELSE 0 END), 0) AS refund_credits
FROM identity.credit_logs
WHERE created_at >= :from_ts AND created_at < :to_ts;

\echo '=== 2. billing.events (路径 B) 总额 ==='
SELECT
    kind,
    count(*)                          AS event_count,
    COALESCE(sum(amount), 0)::bigint  AS total_amount
FROM billing.events
WHERE occurred_at >= :from_ts AND occurred_at < :to_ts
  AND kind IN ('consume', 'refund', 'settle')
GROUP BY kind
ORDER BY kind;

\echo '=== 3. 一一对应检查: credit_logs 但未在 events ==='
-- credit_logs 行 (含 settle 通过 Consume 写的 log) 必须有对应 billing.events
-- 用 (event.log_id, occurred_at 落在同窗口) 匹配. 路径 A 多出来的行 = NATS
-- 没收到 / sink 漏掉.
WITH
log_ids AS (
    SELECT id FROM identity.credit_logs
    WHERE created_at >= :from_ts AND created_at < :to_ts
),
event_log_ids AS (
    SELECT log_id FROM billing.events
    WHERE occurred_at >= :from_ts AND occurred_at < :to_ts
      AND log_id IS NOT NULL
)
SELECT
    'logs_without_events' AS check_name,
    count(*) AS missing_count
FROM log_ids
WHERE id NOT IN (SELECT log_id FROM event_log_ids);

\echo '=== 4. 反向: events 引用了不存在的 credit_log ==='
-- 通常应该 0; 非 0 = sink 收到了 publisher 发但实际没落 DB 的事件 (理论
-- 不会发生, 因为 publisher 在 Consume 事务内调). 调试用.
WITH
log_ids AS (
    SELECT id FROM identity.credit_logs
    WHERE created_at >= :from_ts AND created_at < :to_ts
),
event_log_ids AS (
    SELECT log_id FROM billing.events
    WHERE occurred_at >= :from_ts AND occurred_at < :to_ts
      AND log_id IS NOT NULL
      AND kind IN ('consume', 'refund', 'settle')
)
SELECT
    'events_orphan_log_ref' AS check_name,
    count(*) AS missing_count
FROM event_log_ids
WHERE log_id NOT IN (SELECT id FROM log_ids);

\echo '=== 5. 总额偏差 ==='
WITH
db_consume AS (
    SELECT COALESCE(sum(-delta), 0)::bigint AS amount
    FROM identity.credit_logs
    WHERE created_at >= :from_ts AND created_at < :to_ts AND delta < 0
),
ev_consume AS (
    SELECT COALESCE(sum(amount), 0)::bigint AS amount
    FROM billing.events
    WHERE occurred_at >= :from_ts AND occurred_at < :to_ts
      AND kind IN ('consume', 'settle')
)
SELECT
    db_consume.amount  AS db_total,
    ev_consume.amount  AS event_total,
    (db_consume.amount - ev_consume.amount) AS delta,
    CASE
        WHEN db_consume.amount = 0 THEN 0
        ELSE round(
            abs(db_consume.amount - ev_consume.amount)::numeric
              / db_consume.amount::numeric * 100,
            4
        )
    END AS abs_pct_diff
FROM db_consume, ev_consume;

\echo '=== 6. Hold 残留检查: held 状态 hold expires_at 早于窗口结束 ==='
-- 应该被 ReapExpired 清掉, 走 Release 事件路径. 残留 = reaper 没跑或漏跑.
SELECT
    count(*) AS stuck_held_count
FROM identity.credit_holds
WHERE status = 'held'
  AND expires_at < :to_ts;

\echo '=== 7. 订阅事件 ==='
SELECT
    count(*) AS sub_event_count_db,
    (SELECT count(*) FROM billing.events
     WHERE occurred_at >= :from_ts AND occurred_at < :to_ts
       AND kind = 'subscription') AS sub_event_count_nats
FROM billing.subscription_events
WHERE created_at >= :from_ts AND created_at < :to_ts;
