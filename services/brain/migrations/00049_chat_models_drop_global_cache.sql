-- +goose Up
-- +goose StatementBegin

-- P6 catalog 收窄: brain 不再缓存 global 模型清单 (改 client 直读
-- model-relay /v1/me/models)。清 chat.models 里的两类 global 行:
--   1. source='builtin'   — brain 静态 catalog.go 的 lazy-seed (已删文件)
--   2. source='remote' AND provider_id='biumind-official'
--                          — brain 拉 model-relay 批量同步的 official 行
-- 保留:
--   - source='custom'                       — 用户自填模型 (per-user)
--   - source='remote' AND provider_id!='biumind-official'
--                                            — BYOK 上游 /models 拉的 (per-user)

DELETE FROM chat.models
WHERE source = 'builtin'
   OR (source = 'remote' AND provider_id = 'biumind-official');

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- noop: global 行已由 model-relay 接管 (client 直读 /v1/me/models),
-- per-user custom / BYOK remote 行未动。回滚不重建缓存 (brain 已无 seed 逻辑)。

-- +goose StatementEnd
