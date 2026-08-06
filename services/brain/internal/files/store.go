// Package files — 文件元数据 + blob 存储 (MinIO/S3 兼容).
//
// 职责拆分:
//   * Store (本文件) — Postgres 元数据 CRUD + sha256 dedup
//   * Blob (blob.go)  — MinIO 对象 put/get/remove
//   * Server (api.go) — REST handlers, 串起两者
//
// 设计文档 docs/BiuMind-Code-Artifacts-Sync-Design.md §3.3。
package files

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound = errors.New("files: not found")
	ErrInvalid  = errors.New("files: invalid input")
)

// Status 取值, 区分两段式上传 (presign-upload + finalize) 的中间态。
const (
	StatusPending = "pending" // presigned PUT 已发, 字节可能还没上 / 未 finalize
	StatusReady   = "ready"   // 业务可引用
	StatusOrphan  = "orphan"  // GC 标记的待删
)

// Object — 一行 files.objects, 表示用户拥有的一个 blob。
type Object struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	Sha256     string
	SizeBytes  int64
	MimeType   *string
	Bucket     string
	ObjectKey  string
	Source     string
	Status     string
	Metadata   json.RawMessage
	CreatedAt  time.Time
	DeletedAt  *time.Time
}

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// LookupBySha256 — 用户已上传过同 sha256 的 ready 对象时返回它, 否则 nil。
// 用于 upload 路径的 dedup: 命中则跳过 PutObject, 直接复用 object_key。
// 排除 pending — pending 还没确认字节有没有真上来, 不能用作 dedup 目标。
func (s *Store) LookupBySha256(ctx context.Context, userID uuid.UUID, sha256 string) (*Object, error) {
	const q = `
SELECT id, user_id, sha256, size_bytes, mime_type, bucket, object_key,
       source, status, metadata, created_at, deleted_at
  FROM files.objects
 WHERE user_id = $1 AND sha256 = $2 AND status = 'ready' AND deleted_at IS NULL
 LIMIT 1
`
	row := s.pool.QueryRow(ctx, q, userID, sha256)
	o, err := scanObject(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return o, err
}

// Insert 写入新对象元数据. ID 必须由 caller 提供 (caller 通常已经把它编进
// MinIO object_key 里, 保持一致性)。Status 留空时默认 'ready' — 老的
// multipart upload 路径就是这么用的。Presign 流程的 caller 显式传
// 'pending'。
func (s *Store) Insert(ctx context.Context, o Object) error {
	if o.ID == uuid.Nil || o.UserID == uuid.Nil || o.Bucket == "" || o.ObjectKey == "" {
		return ErrInvalid
	}
	// pending 阶段允许空 sha256 (字节还没传完, 没法算)。ready 阶段必须有。
	if o.Status == "" {
		o.Status = StatusReady
	}
	if o.Status == StatusReady && o.Sha256 == "" {
		return ErrInvalid
	}
	if len(o.Metadata) == 0 {
		o.Metadata = json.RawMessage(`{}`)
	}
	const q = `
INSERT INTO files.objects (
  id, user_id, sha256, size_bytes, mime_type,
  bucket, object_key, source, status, metadata, created_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, now())
`
	_, err := s.pool.Exec(ctx, q,
		o.ID, o.UserID, o.Sha256, o.SizeBytes, o.MimeType,
		o.Bucket, o.ObjectKey, o.Source, o.Status, o.Metadata,
	)
	return err
}

// MarkReady — finalize 阶段把 pending 行升为 ready, 写入真实 sha256/size。
// 仅当 status='pending' 且归属当前 user 时成功; 其他情况 ErrNotFound。
func (s *Store) MarkReady(ctx context.Context, userID, id uuid.UUID, sha256 string, size int64) error {
	if sha256 == "" || size <= 0 {
		return ErrInvalid
	}
	const q = `
UPDATE files.objects
   SET status = 'ready', sha256 = $3, size_bytes = $4
 WHERE user_id = $1 AND id = $2 AND status = 'pending' AND deleted_at IS NULL
`
	tag, err := s.pool.Exec(ctx, q, userID, id, sha256, size)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// HardDelete — 真删一行 (用于 finalize dedup 命中后撤掉自己刚预占位的
// pending 行, 以及 GC 流程)。仅当归属当前 user 才删。
func (s *Store) HardDelete(ctx context.Context, userID, id uuid.UUID) error {
	const q = `DELETE FROM files.objects WHERE user_id = $1 AND id = $2`
	tag, err := s.pool.Exec(ctx, q, userID, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// GetPending — 给 finalize 拿 pending 行, 校验归属与状态一并完成。
// status='ready' 的对象不通过这个接口拿 (那是 Get 的活)。
func (s *Store) GetPending(ctx context.Context, userID, id uuid.UUID) (*Object, error) {
	const q = `
SELECT id, user_id, sha256, size_bytes, mime_type, bucket, object_key,
       source, status, metadata, created_at, deleted_at
  FROM files.objects
 WHERE user_id = $1 AND id = $2 AND status = 'pending' AND deleted_at IS NULL
`
	row := s.pool.QueryRow(ctx, q, userID, id)
	o, err := scanObject(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return o, err
}

// Get — 按 id + user_id 严格匹配, 仅返回 ready 对象。其他用户的 /
// pending 中的 / 已 deleted 的都返回 ErrNotFound (不是 403, 防
// enumerate)。pending 行用 GetPending 单独取。
func (s *Store) Get(ctx context.Context, userID, id uuid.UUID) (*Object, error) {
	const q = `
SELECT id, user_id, sha256, size_bytes, mime_type, bucket, object_key,
       source, status, metadata, created_at, deleted_at
  FROM files.objects
 WHERE user_id = $1 AND id = $2 AND status = 'ready' AND deleted_at IS NULL
`
	row := s.pool.QueryRow(ctx, q, userID, id)
	o, err := scanObject(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return o, err
}

// SoftDelete — 标记 deleted_at, MinIO 对象由清理 job 异步真删。
// 重复 / 跨租户删都返回 ErrNotFound。
func (s *Store) SoftDelete(ctx context.Context, userID, id uuid.UUID) error {
	const q = `
UPDATE files.objects
   SET deleted_at = now()
 WHERE user_id = $1 AND id = $2 AND deleted_at IS NULL
`
	tag, err := s.pool.Exec(ctx, q, userID, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ─── 内部 scan helper ─────────────────────────────────

type rowScanner interface {
	Scan(dest ...any) error
}

func scanObject(row rowScanner) (*Object, error) {
	var o Object
	if err := row.Scan(
		&o.ID, &o.UserID, &o.Sha256, &o.SizeBytes, &o.MimeType,
		&o.Bucket, &o.ObjectKey, &o.Source, &o.Status, &o.Metadata,
		&o.CreatedAt, &o.DeletedAt,
	); err != nil {
		return nil, err
	}
	return &o, nil
}
