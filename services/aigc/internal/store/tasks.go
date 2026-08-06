package store

// tasks.go — aigc.tasks + aigc.task_outputs 仓储.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ─── Domain types ────────────────────────────────────

// Task 对应 aigc.tasks 一行. 字段顺序与 taskColumns / scanTask 一对一.
type Task struct {
	ID              uuid.UUID
	UserID          uuid.UUID
	OrgID           *uuid.UUID
	Type            string // image | video | digital_human | hotparse
	ModelCode       string
	ProviderCode    string
	Prompt          string
	NegativePrompt  string
	Params          []byte // raw jsonb; 调用方按需 unmarshal
	Status          string
	Progress        int16
	ErrorCode       string
	ErrorMessage    string
	CostCredits     int64
	RefundedCredits int64
	IsPublic        bool
	CacheKey        string
	CacheHit        bool
	ExternalTaskID  string
	ParentSHA       string
	LineageOp       string
	DeletedAt       *time.Time
	CreatedAt       time.Time
	QueuedAt        *time.Time
	StartedAt       *time.Time
	CompletedAt     *time.Time
}

// TaskOutput 对应 aigc.task_outputs 一行.
type TaskOutput struct {
	ID               uuid.UUID
	TaskID           uuid.UUID
	Idx              int16
	Kind             string // image | video | audio | cover
	SHA256           string
	StorageURL       string
	StorageKey       string
	Blurhash         string
	CoverSHA         string
	MimeType         string
	FileSize         int64
	Width            int
	Height           int
	DurationMs       int
	ModerationStatus string
	Metadata         []byte // raw jsonb
	CreatedAt        time.Time
}

// IsActive 判断 task 是否还在追踪中（未 terminal）.
func (t *Task) IsActive() bool {
	switch t.Status {
	case "pending", "queued", "running":
		return true
	}
	return false
}

const taskColumns = `id, user_id, org_id, type, model_code, provider_code,
	prompt, negative_prompt, params, status, progress,
	error_code, error_message, cost_credits, refunded_credits, is_public,
	cache_key, cache_hit, external_task_id, parent_sha, lineage_op,
	deleted_at, created_at, queued_at, started_at, completed_at`

func scanTask(r scanner) (*Task, error) {
	t := &Task{}
	var (
		negativePrompt, errorCode, errorMessage, cacheKey, externalTaskID, parentSHA, lineageOp *string
	)
	err := r.Scan(
		&t.ID, &t.UserID, &t.OrgID, &t.Type, &t.ModelCode, &t.ProviderCode,
		&t.Prompt, &negativePrompt, &t.Params, &t.Status, &t.Progress,
		&errorCode, &errorMessage, &t.CostCredits, &t.RefundedCredits, &t.IsPublic,
		&cacheKey, &t.CacheHit, &externalTaskID, &parentSHA, &lineageOp,
		&t.DeletedAt, &t.CreatedAt, &t.QueuedAt, &t.StartedAt, &t.CompletedAt,
	)
	if err != nil {
		return nil, err
	}
	if negativePrompt != nil {
		t.NegativePrompt = *negativePrompt
	}
	if errorCode != nil {
		t.ErrorCode = *errorCode
	}
	if errorMessage != nil {
		t.ErrorMessage = *errorMessage
	}
	if cacheKey != nil {
		t.CacheKey = *cacheKey
	}
	if externalTaskID != nil {
		t.ExternalTaskID = *externalTaskID
	}
	if parentSHA != nil {
		t.ParentSHA = *parentSHA
	}
	if lineageOp != nil {
		t.LineageOp = *lineageOp
	}
	return t, nil
}

const taskOutputColumns = `id, task_id, idx, kind, sha256, storage_url, storage_key,
	blurhash, cover_sha, mime_type, file_size, width, height, duration_ms,
	moderation_status, metadata, created_at`

func scanTaskOutput(r scanner) (*TaskOutput, error) {
	o := &TaskOutput{}
	var blurhash, coverSHA, mimeType *string
	var fileSize *int64
	var width, height, durationMs *int
	err := r.Scan(
		&o.ID, &o.TaskID, &o.Idx, &o.Kind, &o.SHA256, &o.StorageURL, &o.StorageKey,
		&blurhash, &coverSHA, &mimeType, &fileSize, &width, &height, &durationMs,
		&o.ModerationStatus, &o.Metadata, &o.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	if blurhash != nil {
		o.Blurhash = *blurhash
	}
	if coverSHA != nil {
		o.CoverSHA = *coverSHA
	}
	if mimeType != nil {
		o.MimeType = *mimeType
	}
	if fileSize != nil {
		o.FileSize = *fileSize
	}
	if width != nil {
		o.Width = *width
	}
	if height != nil {
		o.Height = *height
	}
	if durationMs != nil {
		o.DurationMs = *durationMs
	}
	return o, nil
}

// ─── Tasks ───────────────────────────────────────────

// CreateTaskArgs 提交时的入参 (orchestrator 在事务内同时:
// 扣积分 → CreateTask → publish NATS).
type CreateTaskArgs struct {
	UserID         uuid.UUID
	OrgID          *uuid.UUID
	Type           string
	ModelCode      string
	ProviderCode   string
	Prompt         string
	NegativePrompt string
	Params         any // 任意结构, 序列化为 jsonb
	IsPublic       bool
	CostCredits    int64
	CacheKey       string
	ParentSHA      string
	LineageOp      string
}

// CreateTask 插入新任务 (status=pending). 返回填充了 id / created_at 的 Task.
func (s *Store) CreateTask(ctx context.Context, a CreateTaskArgs) (*Task, error) {
	paramsJSON, err := json.Marshal(a.Params)
	if err != nil {
		return nil, fmt.Errorf("marshal params: %w", err)
	}
	row := s.pool.QueryRow(ctx, `
		INSERT INTO aigc.tasks
			(user_id, org_id, type, model_code, provider_code,
			 prompt, negative_prompt, params, cost_credits, is_public,
			 cache_key, parent_sha, lineage_op, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, 'pending')
		RETURNING `+taskColumns,
		a.UserID, a.OrgID, a.Type, a.ModelCode, a.ProviderCode,
		a.Prompt, nullableStr(a.NegativePrompt), paramsJSON, a.CostCredits, a.IsPublic,
		nullableStr(a.CacheKey), nullableStr(a.ParentSHA), nullableStr(a.LineageOp),
	)
	return rowOrErr(scanTask(row))
}

// GetTask 按 id 取单任务. 不过滤 deleted_at (调用方按需用 ApplyVisibility 过滤).
func (s *Store) GetTask(ctx context.Context, id uuid.UUID) (*Task, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT `+taskColumns+`
		FROM aigc.tasks
		WHERE id = $1
	`, id)
	return rowOrErr(scanTask(row))
}

// ListMyTasksArgs — ListMyTasks 入参. statuses/types 空切片 = 不过滤.
type ListMyTasksArgs struct {
	UserID         uuid.UUID
	Statuses       []string
	Types          []string
	IncludeDeleted bool
	Limit          int
	Offset         int
}

// ListMyTasks 按 user 拉任务, 默认按 created_at desc.
func (s *Store) ListMyTasks(ctx context.Context, a ListMyTasksArgs) ([]*Task, error) {
	args := []any{a.UserID}
	q := strings.Builder{}
	q.WriteString(`SELECT ` + taskColumns + ` FROM aigc.tasks WHERE user_id = $1`)
	if !a.IncludeDeleted {
		q.WriteString(` AND deleted_at IS NULL`)
	}
	if len(a.Statuses) > 0 {
		q.WriteString(fmt.Sprintf(` AND status = ANY($%d)`, len(args)+1))
		args = append(args, a.Statuses)
	}
	if len(a.Types) > 0 {
		q.WriteString(fmt.Sprintf(` AND type = ANY($%d)`, len(args)+1))
		args = append(args, a.Types)
	}
	limit := a.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	q.WriteString(fmt.Sprintf(` ORDER BY created_at DESC LIMIT $%d OFFSET $%d`,
		len(args)+1, len(args)+2))
	args = append(args, limit, max0(a.Offset))

	rows, err := s.pool.Query(ctx, q.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// UpdateTaskStatus 设置 status / progress / error / 时间戳.
// 不存在的 task 返回 ErrNotFound. 由 orchestrator (worker 收到 NATS update 事件后) 调用.
type UpdateTaskStatusArgs struct {
	ID              uuid.UUID
	Status          string
	Progress        *int16
	ErrorCode       string
	ErrorMessage    string
	ExternalTaskID  string
	RefundedCredits *int64
	CacheHit        *bool
	QueuedAt        *time.Time
	StartedAt       *time.Time
	CompletedAt     *time.Time
}

func (s *Store) UpdateTaskStatus(ctx context.Context, a UpdateTaskStatusArgs) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE aigc.tasks SET
			status            = $2,
			progress          = COALESCE($3, progress),
			error_code        = COALESCE($4, error_code),
			error_message     = COALESCE($5, error_message),
			external_task_id  = COALESCE($6, external_task_id),
			refunded_credits  = COALESCE($7, refunded_credits),
			cache_hit         = COALESCE($8, cache_hit),
			queued_at         = COALESCE($9, queued_at),
			started_at        = COALESCE($10, started_at),
			completed_at      = COALESCE($11, completed_at)
		WHERE id = $1
	`,
		a.ID, a.Status, a.Progress, nullableStr(a.ErrorCode), nullableStr(a.ErrorMessage),
		nullableStr(a.ExternalTaskID), a.RefundedCredits, a.CacheHit,
		a.QueuedAt, a.StartedAt, a.CompletedAt,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetTaskVisibility 批量改 is_public. 仅作用于自己的任务 (UserID 过滤).
// 返回真正更新的行数.
func (s *Store) SetTaskVisibility(ctx context.Context, userID uuid.UUID, taskIDs []uuid.UUID, isPublic bool) (int64, error) {
	if len(taskIDs) == 0 {
		return 0, nil
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE aigc.tasks SET is_public = $1
		WHERE user_id = $2 AND id = ANY($3) AND deleted_at IS NULL
	`, isPublic, userID, taskIDs)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// SoftDeleteTasks 批量软删. 30d 后由 GC job 物理删除 (含 outputs / derivatives /
// public mirror 镜像). 仅作用于自己的任务.
func (s *Store) SoftDeleteTasks(ctx context.Context, userID uuid.UUID, taskIDs []uuid.UUID) (int64, error) {
	if len(taskIDs) == 0 {
		return 0, nil
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE aigc.tasks SET deleted_at = now()
		WHERE user_id = $1 AND id = ANY($2) AND deleted_at IS NULL
	`, userID, taskIDs)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// ─── TaskOutputs ─────────────────────────────────────

// CreateTaskOutputArgs — worker 转存完上游产物后写入.
// CAS sha256 是核心字段, 同 task 内 (idx, sha256) 唯一 (由调用方保证).
type CreateTaskOutputArgs struct {
	TaskID           uuid.UUID
	Idx              int16
	Kind             string
	SHA256           string
	StorageURL       string
	StorageKey       string
	Blurhash         string
	CoverSHA         string
	MimeType         string
	FileSize         int64
	Width            int
	Height           int
	DurationMs       int
	Metadata         any
}

func (s *Store) CreateTaskOutput(ctx context.Context, a CreateTaskOutputArgs) (*TaskOutput, error) {
	var metaJSON []byte
	if a.Metadata != nil {
		var err error
		metaJSON, err = json.Marshal(a.Metadata)
		if err != nil {
			return nil, fmt.Errorf("marshal metadata: %w", err)
		}
	}
	row := s.pool.QueryRow(ctx, `
		INSERT INTO aigc.task_outputs
			(task_id, idx, kind, sha256, storage_url, storage_key,
			 blurhash, cover_sha, mime_type, file_size,
			 width, height, duration_ms, metadata)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		RETURNING `+taskOutputColumns,
		a.TaskID, a.Idx, a.Kind, a.SHA256, a.StorageURL, a.StorageKey,
		nullableStr(a.Blurhash), nullableStr(a.CoverSHA), nullableStr(a.MimeType), nullableInt64(a.FileSize),
		nullableInt(a.Width), nullableInt(a.Height), nullableInt(a.DurationMs),
		nullableJSON(metaJSON),
	)
	return rowOrErr(scanTaskOutput(row))
}

// ListTaskOutputs 拉一个任务的所有输出, 按 idx 升序.
func (s *Store) ListTaskOutputs(ctx context.Context, taskID uuid.UUID) ([]*TaskOutput, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+taskOutputColumns+`
		FROM aigc.task_outputs
		WHERE task_id = $1
		ORDER BY idx
	`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*TaskOutput
	for rows.Next() {
		o, err := scanTaskOutput(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// OutputLocation — by-sha 下载时定位一个 CAS 对象的物理位置 + 归属信息.
// Bucket 取 "outputs" / "derivatives" 二选一 (逻辑名, 由 caller 映射到真桶).
type OutputLocation struct {
	StorageKey  string
	Bucket      string // "outputs" | "derivatives"
	MimeType    string
	OwnerUserID uuid.UUID
	IsPublic    bool
}

// LookupOutputBySha 按 sha 定位一个可下载对象 + 归属. 命中顺序:
//  1. task_outputs.sha256 (产物本体, 在 outputs 桶) — 用其 storage_key。
//  2. task_outputs.cover_sha (视频封面, 在 derivatives 桶) — key 按 CAS 规则
//     推导 (worker persist 用 derivatives/<sha[:2]>/<sha[2:4]>/<sha>.jpg)。
//  3. metadata->'derivatives' 里的多分辨率派生 (v2 派生落地后) — 同 derivatives 桶。
//
// 任一命中都 JOIN tasks 拿 owner_user_id + is_public 供 caller 鉴权.
// 未命中返回 ErrNotFound. 软删 (deleted_at) 的 task 视为不存在.
func (s *Store) LookupOutputBySha(ctx context.Context, sha string) (*OutputLocation, error) {
	// 1) 产物本体
	var loc OutputLocation
	err := s.pool.QueryRow(ctx, `
		SELECT o.storage_key, COALESCE(o.mime_type, ''), t.user_id, COALESCE(t.is_public, false)
		FROM aigc.task_outputs o
		JOIN aigc.tasks t ON t.id = o.task_id
		WHERE o.sha256 = $1 AND t.deleted_at IS NULL
		LIMIT 1
	`, sha).Scan(&loc.StorageKey, &loc.MimeType, &loc.OwnerUserID, &loc.IsPublic)
	if err == nil {
		loc.Bucket = "outputs"
		return &loc, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}

	// 2) 视频封面 (derivatives 桶, key 按 CAS 推导)
	err = s.pool.QueryRow(ctx, `
		SELECT t.user_id, COALESCE(t.is_public, false)
		FROM aigc.task_outputs o
		JOIN aigc.tasks t ON t.id = o.task_id
		WHERE o.cover_sha = $1 AND t.deleted_at IS NULL
		LIMIT 1
	`, sha).Scan(&loc.OwnerUserID, &loc.IsPublic)
	if err == nil {
		loc.Bucket = "derivatives"
		loc.StorageKey = CASKey("derivatives", sha, "jpg")
		loc.MimeType = "image/jpeg"
		return &loc, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}

	// 3) 多分辨率派生 (metadata.derivatives[].sha). v2 派生落地后命中;
	// 现阶段无派生时此查询恒空, 不影响.
	err = s.pool.QueryRow(ctx, `
		SELECT t.user_id, COALESCE(t.is_public, false),
		       COALESCE(d.value->>'mime', 'image/webp')
		FROM aigc.task_outputs o
		JOIN aigc.tasks t ON t.id = o.task_id
		CROSS JOIN LATERAL jsonb_array_elements(
		    COALESCE(o.metadata->'derivatives', '[]'::jsonb)) AS d(value)
		WHERE d.value->>'sha' = $1 AND t.deleted_at IS NULL
		LIMIT 1
	`, sha).Scan(&loc.OwnerUserID, &loc.IsPublic, &loc.MimeType)
	if err == nil {
		loc.Bucket = "derivatives"
		ext := "webp"
		if loc.MimeType == "image/jpeg" {
			ext = "jpg"
		}
		loc.StorageKey = CASKey("derivatives", sha, ext)
		return &loc, nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return nil, err
}

// CASKey 复刻 worker persist.py 的 cas_key: <prefix>/<sha[:2]>/<sha[2:4]>/<sha>.<ext>.
// derivatives / cover 没有独立 storage_key 列, 按此规则推导物理位置.
func CASKey(prefix, sha, ext string) string {
	if len(sha) < 4 {
		return fmt.Sprintf("%s/%s.%s", prefix, sha, ext)
	}
	return fmt.Sprintf("%s/%s/%s/%s.%s", prefix, sha[:2], sha[2:4], sha, ext)
}

// ListTaskOutputsBatch 批量按 task_id 拉所有 outputs (用于 ListMyTasks 一次性聚合).
// 返回 map[task_id][]output, 内部按 idx 升序.
func (s *Store) ListTaskOutputsBatch(ctx context.Context, taskIDs []uuid.UUID) (map[uuid.UUID][]*TaskOutput, error) {
	if len(taskIDs) == 0 {
		return map[uuid.UUID][]*TaskOutput{}, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+taskOutputColumns+`
		FROM aigc.task_outputs
		WHERE task_id = ANY($1)
		ORDER BY task_id, idx
	`, taskIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[uuid.UUID][]*TaskOutput{}
	for rows.Next() {
		o, err := scanTaskOutput(rows)
		if err != nil {
			return nil, err
		}
		out[o.TaskID] = append(out[o.TaskID], o)
	}
	return out, rows.Err()
}

// ─── helpers (本文件复用; 公共的放 store.go) ──────────

func nullableStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullableInt(n int) any {
	if n == 0 {
		return nil
	}
	return n
}

func nullableInt64(n int64) any {
	if n == 0 {
		return nil
	}
	return n
}

func nullableJSON(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}

func max0(n int) int {
	if n < 0 {
		return 0
	}
	return n
}
