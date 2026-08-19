-- ============================================================================
-- 00004_events_notify_lightweight.sql — pg_notify 不再携带整行事件 payload
--
-- 故障：00001 的 brain.notify_event() 把整行事件 JSON（含笔记正文快照
-- content_md）塞进 pg_notify。PG 对 notify payload 有 8000 字节硬上限
-- （SQLSTATE 22023 payload string too long），正文较长的笔记（≈8KB+，
-- 中文笔记 3KB 字符即超）INSERT brain.events 直接报错 → 整个写事务回滚
-- → 客户端 500 无限重试，长笔记的所有更新永远落不了库、跨端不同步。
--
-- 修复：notify 只当「叫醒铃」，发 {scope, id, type} 三元组（定长几十字节，
-- 永不超限）；consumer（internal/events/listener.go）按 id 回查
-- brain.events 取完整 payload 再发布。poller 的 listenNudge 本就不读
-- payload，outbox 兜底轮询（processBatch）也直接读表，均不受影响。
--
-- 兼容性：滚动发布期间旧 listener 收到新格式 notify 会拿到空 payload
-- （data=null）——brain 单二进制同时升级 trigger 与 listener，窗口极短；
-- 反向（新 listener 收旧格式）按 id 回查依旧正确。events 表无 DELETE，
-- 回查必然命中。
-- ============================================================================

-- +goose Up

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION brain.notify_event() RETURNS trigger AS $$
BEGIN
    -- 只发叫醒三元组；payload 由 consumer 按 id 回查（pg_notify 8000 字节
    -- 上限，整行事件 JSON 会超）。
    PERFORM pg_notify('brain_events', json_build_object(
        'scope', NEW.scope,
        'id',    NEW.id,
        'type',  NEW.event_type
    )::text);
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose Down

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION brain.notify_event() RETURNS trigger AS $$
BEGIN
    PERFORM pg_notify('brain_events', json_build_object(
        'scope', NEW.scope,
        'id',    NEW.id,
        'type',  NEW.event_type,
        'payload', NEW.payload
    )::text);
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
