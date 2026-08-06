-- ════════════════════════════════════════════════════════════════════
-- P4.S3.3 — 一次性全量 backfill: aigc.{providers,models} → model_relay.*
--
-- 段 2.3 mirror 只在 aigc admin 写时触发, 旧数据 (mirror 上线前的存量
-- 行) 未同步. 本脚本把全量 aigc.* 推到 model_relay.*, 字段映射与
-- adminapi/mirror.go 一致.
--
-- 幂等: ON CONFLICT WHERE manual_override = false — 已 mirror 的行
-- 会被刷新; admin 手动锁 manual_override=true 的行不被覆盖.
--
-- 用法:
--   docker exec -i biu-postgres psql -U biumind -d biu_core \
--     < services/model-relay/scripts/migrate_aigc_dictionary.sql
--
-- 段 3.4 之前必跑一次. 跑完后下方 SELECT 应返 0 differences.
-- ════════════════════════════════════════════════════════════════════

BEGIN;

-- ─── providers ─────────────────────────────────────────
-- aigc.providers (code, name, base_url, credentials_ref, priority,
--                  enabled, config) → model_relay.providers
-- credentials_ref 不直接映射 (model_relay.providers 无此字段; 凭证关系
-- 是 model_relay.credentials.provider_id FK 反向); aigc 旧 credentials_ref
-- 通常已被 admin 在 model_relay.credentials 录入时建立 provider FK.

INSERT INTO model_relay.providers
    (code, name, protocol, icon, description, status)
SELECT
    p.code,
    p.name,
    'openai_compat'::text  AS protocol,
    ''                      AS icon,
    ''                      AS description,
    CASE WHEN p.enabled THEN 'active' ELSE 'disabled' END AS status
FROM aigc.providers p
ON CONFLICT (code) DO UPDATE SET
    name       = EXCLUDED.name,
    status     = EXCLUDED.status,
    updated_at = now();

-- ─── models ────────────────────────────────────────────
-- type → mode 翻译, pricing_strategy 按是否有 pricing_rule 推断
-- (与 mirror.go 同款逻辑).

INSERT INTO model_relay.models
    (code, display_name, family, mode, pricing_strategy, dispatch_mode,
     context_window, max_output, capabilities, status,
     upstream_ref, manual_override)
SELECT
    m.code,
    m.display_name,
    m.provider_code AS family,
    CASE m.type
        WHEN 'image'         THEN 'image_generation'
        WHEN 'video'         THEN 'video_generation'
        WHEN 'digital_human' THEN 'digital_human'
        WHEN 'hotparse'      THEN 'hotparse'
        ELSE 'image_generation'  -- defensive, schema 限制 type 必在 4 选 1
    END AS mode,
    CASE
        WHEN m.pricing_rule IS NOT NULL AND m.pricing_rule::text <> 'null'
            THEN 'parameter'
        ELSE 'fixed'
    END AS pricing_strategy,
    'async' AS dispatch_mode,
    0       AS context_window,
    0       AS max_output,
    COALESCE(m.config, '{}'::jsonb) AS capabilities,
    CASE WHEN m.enabled THEN 'active' ELSE 'disabled' END AS status,
    jsonb_build_object(
        'vendor',    m.provider_code,
        'source',    'aigc-mirror',
        'aigc_code', m.code
    ) AS upstream_ref,
    false AS manual_override
FROM aigc.models m
ON CONFLICT (code) DO UPDATE SET
    display_name     = EXCLUDED.display_name,
    family           = EXCLUDED.family,
    mode             = EXCLUDED.mode,
    pricing_strategy = EXCLUDED.pricing_strategy,
    dispatch_mode    = EXCLUDED.dispatch_mode,
    capabilities     = EXCLUDED.capabilities,
    status           = EXCLUDED.status,
    upstream_ref     = EXCLUDED.upstream_ref,
    updated_at       = now()
WHERE model_relay.models.manual_override = false;

-- ─── pricing_rules ─────────────────────────────────────
-- 给 aigc.models.pricing_rule 非空的, append 一条新规则到
-- model_relay.pricing_rules (effective_at = now()). 旧规则保留作版本史.

INSERT INTO model_relay.pricing_rules (model_id, rule_jsonb)
SELECT mr.id, m.pricing_rule
FROM aigc.models m
JOIN model_relay.models mr ON mr.code = m.code
WHERE m.pricing_rule IS NOT NULL
  AND m.pricing_rule::text <> 'null'
  AND mr.manual_override = false
  -- 防 1 分钟内重复 backfill 加多余历史
  AND NOT EXISTS (
      SELECT 1 FROM model_relay.pricing_rules pr
      WHERE pr.model_id = mr.id
        AND pr.effective_at >= now() - interval '1 minute'
  );

COMMIT;

-- ════════════════════════════════════════════════════════════════════
-- 验证: 跑完应该全 0 差异 (除 manual_override=true 锁定行)
-- ════════════════════════════════════════════════════════════════════

\echo '=== providers diff (aigc not in model_relay; should be 0) ==='
SELECT a.code FROM aigc.providers a
LEFT JOIN model_relay.providers m ON m.code = a.code
WHERE m.code IS NULL;

\echo '=== models diff (aigc not in model_relay; should be 0 except manual-locked) ==='
SELECT a.code FROM aigc.models a
LEFT JOIN model_relay.models m ON m.code = a.code
WHERE m.code IS NULL;

\echo '=== pricing_rules backfilled ==='
SELECT mr.code, count(pr.*) AS rule_versions
FROM aigc.models am
JOIN model_relay.models mr ON mr.code = am.code
LEFT JOIN model_relay.pricing_rules pr ON pr.model_id = mr.id
WHERE am.pricing_rule IS NOT NULL
GROUP BY mr.code
ORDER BY mr.code;

\echo '=== summary ==='
SELECT
    (SELECT count(*) FROM aigc.providers) AS aigc_p,
    (SELECT count(*) FROM model_relay.providers WHERE code IN (SELECT code FROM aigc.providers)) AS mr_p,
    (SELECT count(*) FROM aigc.models) AS aigc_m,
    (SELECT count(*) FROM model_relay.models WHERE upstream_ref->>'source' = 'aigc-mirror') AS mr_m;
