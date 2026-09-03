// Package settings — identity.user_settings 通用 KV 仓储（migration 00020）.
//
// 表是 per-user 任意设置的承载：key = 设置名，value = jsonb 负载。
// B2 只用 key='ingest_model'（用户 ingest 模型偏好，形如 {"model":"..."}），
// 但 store 层保持通用，新增设置项不必再建表 / 改 schema。
//
// 两端消费：
//   - 用户侧 api.MountSettings（requireAuth，读写自己的）
//   - worker 侧 internalapi.MountSettings（共享 bearer，按 owner 拉，404 = 未设置）
package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound — (user_id, key) 无行。消费方（如 ingest worker）应回落默认行为。
var ErrNotFound = errors.New("settings: key not found for user")

// KeyIngestModel — B2 ingest 模型偏好的设置 key。value 形如 {"model":"..."}.
const KeyIngestModel = "ingest_model"

// Store 包装 identity.user_settings 的读写。
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Get 取 (user_id, key) 的 jsonb 原文；无行返 ErrNotFound。
func (s *Store) Get(ctx context.Context, userID uuid.UUID, key string) (json.RawMessage, error) {
	var raw []byte
	err := s.pool.QueryRow(ctx,
		`SELECT value FROM identity.user_settings WHERE user_id = $1 AND key = $2`,
		userID, key).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("settings get: %w", err)
	}
	return json.RawMessage(raw), nil
}

// Set upsert (user_id, key)。value 必须是合法 jsonb。
func (s *Store) Set(ctx context.Context, userID uuid.UUID, key string, value json.RawMessage) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO identity.user_settings (user_id, key, value)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (user_id, key)
		 DO UPDATE SET value = EXCLUDED.value, updated_at = now()`,
		userID, key, []byte(value))
	if err != nil {
		return fmt.Errorf("settings set: %w", err)
	}
	return nil
}

// Delete 删 (user_id, key)；无行也视为成功（清除语义幂等）。
func (s *Store) Delete(ctx context.Context, userID uuid.UUID, key string) error {
	_, err := s.pool.Exec(ctx,
		`DELETE FROM identity.user_settings WHERE user_id = $1 AND key = $2`,
		userID, key)
	if err != nil {
		return fmt.Errorf("settings delete: %w", err)
	}
	return nil
}

// ─── ingest_model 类型化封装 ──────────────────────────
//
// 通用 KV 之上给 B2 一个类型化入口，调用方不用关心 jsonb 包装形态。
// ingestModelValue 的字段未来可加（如温度等 ingest 调参），保持 jsonb 演进空间。

type ingestModelValue struct {
	Model string `json:"model"`
}

// GetIngestModel 取用户 ingest 模型偏好；未设置返 ErrNotFound。
// value 缺 model 字段（脏数据）也按未设置处理，消费方回落。
func (s *Store) GetIngestModel(ctx context.Context, userID uuid.UUID) (string, error) {
	raw, err := s.Get(ctx, userID, KeyIngestModel)
	if err != nil {
		return "", err
	}
	var v ingestModelValue
	if err := json.Unmarshal(raw, &v); err != nil || v.Model == "" {
		return "", ErrNotFound
	}
	return v.Model, nil
}

// SetIngestModel 写入用户 ingest 模型偏好。
func (s *Store) SetIngestModel(ctx context.Context, userID uuid.UUID, model string) error {
	raw, err := json.Marshal(ingestModelValue{Model: model})
	if err != nil {
		return fmt.Errorf("settings marshal ingest_model: %w", err)
	}
	return s.Set(ctx, userID, KeyIngestModel, raw)
}

// DeleteIngestModel 清除用户 ingest 模型偏好（幂等）。
func (s *Store) DeleteIngestModel(ctx context.Context, userID uuid.UUID) error {
	return s.Delete(ctx, userID, KeyIngestModel)
}
