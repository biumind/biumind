-- +goose Up
-- +goose StatementBegin

-- ─── model_relay LISTEN/NOTIFY fan-out (MC-M1.2) ────────────────────
--
-- Why this exists: model-relay caches model / channel / credential
-- configuration in-process (TTL 60s); on every mutation we fire a
-- NOTIFY so multi-replica deployments invalidate caches in <1s instead
-- of waiting for TTL. Same pattern as app_center 00001's events_notify
-- but simpler — there is no events ledger here, the trigger reads
-- straight off the mutated row.
--
-- Channel name 'model_relay_config_changed' is fixed; subscribers in
-- internal/registry/cache.go must match exactly.
--
-- Payload shape: {"table": "...", "id": "...", "op": "INSERT|UPDATE|DELETE"}
-- - For composite-PK tables (fx_rates / model_group_bindings /
--   user_group_memberships) we use a synthesised id like "USD/CNY"
--   that the application can split if it needs to invalidate the exact
--   row. Most consumers will just blow away the whole sub-cache.
--
-- Tables NOT triggered: route_rules (MVP doesn't read it; adding noise
-- when the table is empty has no value). When P3 lifts the editor we
-- add the trigger then.

CREATE OR REPLACE FUNCTION model_relay.notify_config_changed()
RETURNS trigger AS $$
DECLARE
    row_id     text;
    op         text := TG_OP;
    payload    jsonb;
    target     record;
BEGIN
    -- DELETE 走 OLD，其它走 NEW
    IF op = 'DELETE' THEN
        target := OLD;
    ELSE
        target := NEW;
    END IF;

    -- 按表名分支取主键串
    -- 复合 PK 的表合成 "a/b" 形式；单 uuid PK 直接用 id::text
    CASE TG_TABLE_NAME
        WHEN 'fx_rates' THEN
            row_id := target.from_currency || '/' || target.to_currency;
        WHEN 'model_group_bindings' THEN
            row_id := target.group_id::text || '/' || target.model_id::text;
        WHEN 'user_group_memberships' THEN
            row_id := target.user_id::text || '/' || target.group_id::text;
        ELSE
            row_id := target.id::text;
    END CASE;

    payload := jsonb_build_object(
        'table', TG_TABLE_NAME,
        'id',    row_id,
        'op',    op
    );

    PERFORM pg_notify('model_relay_config_changed', payload::text);

    IF op = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- 给 8 张主表挂触发器（route_rules 暂不挂，见上文注释）
DROP TRIGGER IF EXISTS notify_providers ON model_relay.providers;
CREATE TRIGGER notify_providers
    AFTER INSERT OR UPDATE OR DELETE ON model_relay.providers
    FOR EACH ROW EXECUTE FUNCTION model_relay.notify_config_changed();

DROP TRIGGER IF EXISTS notify_credentials ON model_relay.credentials;
CREATE TRIGGER notify_credentials
    AFTER INSERT OR UPDATE OR DELETE ON model_relay.credentials
    FOR EACH ROW EXECUTE FUNCTION model_relay.notify_config_changed();

DROP TRIGGER IF EXISTS notify_models ON model_relay.models;
CREATE TRIGGER notify_models
    AFTER INSERT OR UPDATE OR DELETE ON model_relay.models
    FOR EACH ROW EXECUTE FUNCTION model_relay.notify_config_changed();

DROP TRIGGER IF EXISTS notify_channels ON model_relay.channels;
CREATE TRIGGER notify_channels
    AFTER INSERT OR UPDATE OR DELETE ON model_relay.channels
    FOR EACH ROW EXECUTE FUNCTION model_relay.notify_config_changed();

DROP TRIGGER IF EXISTS notify_pricing ON model_relay.pricing;
CREATE TRIGGER notify_pricing
    AFTER INSERT OR UPDATE OR DELETE ON model_relay.pricing
    FOR EACH ROW EXECUTE FUNCTION model_relay.notify_config_changed();

DROP TRIGGER IF EXISTS notify_fx_rates ON model_relay.fx_rates;
CREATE TRIGGER notify_fx_rates
    AFTER INSERT OR UPDATE OR DELETE ON model_relay.fx_rates
    FOR EACH ROW EXECUTE FUNCTION model_relay.notify_config_changed();

DROP TRIGGER IF EXISTS notify_model_groups ON model_relay.model_groups;
CREATE TRIGGER notify_model_groups
    AFTER INSERT OR UPDATE OR DELETE ON model_relay.model_groups
    FOR EACH ROW EXECUTE FUNCTION model_relay.notify_config_changed();

DROP TRIGGER IF EXISTS notify_model_group_bindings ON model_relay.model_group_bindings;
CREATE TRIGGER notify_model_group_bindings
    AFTER INSERT OR UPDATE OR DELETE ON model_relay.model_group_bindings
    FOR EACH ROW EXECUTE FUNCTION model_relay.notify_config_changed();

DROP TRIGGER IF EXISTS notify_user_group_memberships ON model_relay.user_group_memberships;
CREATE TRIGGER notify_user_group_memberships
    AFTER INSERT OR UPDATE OR DELETE ON model_relay.user_group_memberships
    FOR EACH ROW EXECUTE FUNCTION model_relay.notify_config_changed();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TRIGGER IF EXISTS notify_user_group_memberships ON model_relay.user_group_memberships;
DROP TRIGGER IF EXISTS notify_model_group_bindings ON model_relay.model_group_bindings;
DROP TRIGGER IF EXISTS notify_model_groups ON model_relay.model_groups;
DROP TRIGGER IF EXISTS notify_fx_rates ON model_relay.fx_rates;
DROP TRIGGER IF EXISTS notify_pricing ON model_relay.pricing;
DROP TRIGGER IF EXISTS notify_channels ON model_relay.channels;
DROP TRIGGER IF EXISTS notify_models ON model_relay.models;
DROP TRIGGER IF EXISTS notify_credentials ON model_relay.credentials;
DROP TRIGGER IF EXISTS notify_providers ON model_relay.providers;

DROP FUNCTION IF EXISTS model_relay.notify_config_changed();

-- +goose StatementEnd
