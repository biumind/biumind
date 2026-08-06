// Agent Plane DB store —— 4 张表里 environments 的 CRUD（S3-2）。sessions /
// session_results 的 CRUD 等 S3-5 / S3-6 引入路由层时再加。
//
// 设计：见 docs/BiuMind-Agent-Plane-Design.md §5.3。
//
// 错误返回：sentinel `ErrNotFound` 给 HTTP 层判 404；其他 SQL 错原样返回供
// 上层包装（同 services/brain/internal/code/store.go 的约定）。

package agentplane

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound 表示 environment 不存在或不属于调用方 —— HTTP 层映射 404。
// 不区分 "不存在" 跟 "存在但不属于你" 是为了避免帮攻击者枚举其他用户的 env_id。
var ErrNotFound = errors.New("agentplane: environment not found")

// Store 包了 pgxpool，提供高层 CRUD。
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Environment 是 DB 行的 Go 表示。字段顺序跟 schema 一致便于 review。
type Environment struct {
	EnvironmentID uuid.UUID
	UserID        *uuid.UUID // NULL = 系统级（runtime 共享池）；biu_daemon 时必有用户
	WorkerKind    string
	MachineName   string
	OsArch        string
	GitInfo       []byte // JSONB raw bytes
	Capabilities  []string
	PublicKey     []byte // X25519 公钥；S3-4 用，S3-2 字段就位但可空
	PoolTag       string
	DeviceID      *uuid.UUID // R6.3：签发本 environment 的 device（device token 注册时填）；JWT/runtime 注册为 NULL
	State         string
	CreatedAt     time.Time
	LastSeenAt    time.Time
}

// CreateEnvironmentReq 是 RegisterEnvironment 的输入。caller 已做参数校验
// （worker_kind 在白名单内、machine_name 非空等），store 只管落库。
type CreateEnvironmentReq struct {
	UserID       *uuid.UUID
	WorkerKind   string
	MachineName  string
	OsArch       string
	GitInfo      []byte
	Capabilities []string
	PublicKey    []byte
	PoolTag      string
	DeviceID     *uuid.UUID // R6.3：device token 注册时关联的设备；其余 nil
}

// RegisterEnvironment 注册一个 environment，返回完整 Environment（含
// environment_id + state + created_at/last_seen_at）。
//
// device token 注册（DeviceID != nil）：按 device_id UPSERT —— 复用既有
// environment_id，避免每次 daemon 重启 churn 新 env_id。重启回到同 env_id →
// 同 JetStream durable worker-<envID> → 旧的在飞 work 被 AckWait 自动 redeliver
// 给重连的 worker（janitor.go 开头声称的不变量，靠这里成立）。DO UPDATE 刷新
// pubkey/capabilities/os_arch/git_info/pool_tag 并把 state 拉回 online；
// created_at 不动（保留首次注册时间）。public_key 必须刷新 —— daemon 若轮换
// X25519 keypair，否则 BYOK 会用旧 key 封装导致 daemon 解密失败。
//
// 无 device（runtime 池 / PAT / JWT，DeviceID == nil）：保持 INSERT 语义 ——
// runtime 每副本须是独立 env 供 PickRuntimeEnvironment 负载均衡，不能合并。
// 约束 agent_environments_device_uniq 是 partial（WHERE device_id IS NOT NULL），
// nil device 不触发冲突。
func (s *Store) RegisterEnvironment(ctx context.Context, req CreateEnvironmentReq) (*Environment, error) {
	envID := uuid.New()
	const cols = `environment_id, user_id, worker_kind, machine_name, os_arch,
	              git_info, capabilities, public_key, pool_tag, device_id, state,
	              created_at, last_seen_at`

	var q string
	if req.DeviceID != nil {
		// 冲突时 RETURNING 返回 DO UPDATE 后那行 —— 既有的稳定 environment_id，
		// 不是这次的 uuid.New()。
		q = `
			INSERT INTO agent_environments
				(environment_id, user_id, worker_kind, machine_name, os_arch,
				 git_info, capabilities, public_key, pool_tag, device_id)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			ON CONFLICT (device_id) WHERE device_id IS NOT NULL
			DO UPDATE SET
				worker_kind  = EXCLUDED.worker_kind,
				machine_name = EXCLUDED.machine_name,
				os_arch      = EXCLUDED.os_arch,
				git_info     = EXCLUDED.git_info,
				capabilities = EXCLUDED.capabilities,
				public_key   = EXCLUDED.public_key,
				pool_tag     = EXCLUDED.pool_tag,
				state        = 'online',
				last_seen_at = now()
			RETURNING ` + cols
	} else {
		q = `
			INSERT INTO agent_environments
				(environment_id, user_id, worker_kind, machine_name, os_arch,
				 git_info, capabilities, public_key, pool_tag, device_id)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			RETURNING ` + cols
	}

	var env Environment
	err := s.pool.QueryRow(ctx, q,
		envID, req.UserID, req.WorkerKind, req.MachineName, req.OsArch,
		req.GitInfo, req.Capabilities, req.PublicKey, req.PoolTag, req.DeviceID,
	).Scan(
		&env.EnvironmentID, &env.UserID, &env.WorkerKind, &env.MachineName,
		&env.OsArch, &env.GitInfo, &env.Capabilities, &env.PublicKey,
		&env.PoolTag, &env.DeviceID, &env.State, &env.CreatedAt, &env.LastSeenAt,
	)
	if err != nil {
		return nil, fmt.Errorf("register environment: %w", err)
	}
	return &env, nil
}

// Heartbeat 续 last_seen_at 并把 state 拉回 online（之前可能被 janitor 标
// offline）。userID 严格匹配 —— 跨用户调用会被当 not found 拒绝。
func (s *Store) Heartbeat(ctx context.Context, userID, envID uuid.UUID) error {
	const q = `
		UPDATE agent_environments
		   SET last_seen_at = now(),
		       state = 'online'
		 WHERE environment_id = $1
		   AND user_id = $2
	`
	tag, err := s.pool.Exec(ctx, q, envID, userID)
	if err != nil {
		return fmt.Errorf("heartbeat: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteEnvironment 注销。物理删而非软删 —— Agent Plane environments 没有
// 历史回看价值（用户可重新注册），无需保留死数据。
func (s *Store) DeleteEnvironment(ctx context.Context, userID, envID uuid.UUID) error {
	const q = `DELETE FROM agent_environments WHERE environment_id = $1 AND user_id = $2`
	tag, err := s.pool.Exec(ctx, q, envID, userID)
	if err != nil {
		return fmt.Errorf("delete environment: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ListUserEnvironments 列出 userID 的 environment(按 last_seen_at 倒序)。
// `state=` 过滤可空(空 = 默认过滤;非空 = 严格匹配)。
//
// 默认过滤(stateFilter==""): 只保留 online + 1 小时内的 offline。
// 历史背景: 这里曾经是"包含全部 offline 让用户看见所有曾注册过的 worker",
// 但 worker 有 re-register-on-404 路径(没退避),brain 抖一抖能在几分钟内
// 爆增几百万行;listEnvironments 全表排序炸 30s timeout, dialog 永远转
// 圈。改成默认丢掉老 offline + 硬上 LIMIT 防止单用户撑爆响应。
//
// LIMIT 100: 单个用户合理上限 —— 平时只 1-2 台 daemon, runtime 池也就
// 几十台。100 已经是"出问题了"的预警,不是常态值。
const listLimit = 100
const listOfflineMaxAge = "1 hour"

func (s *Store) ListUserEnvironments(ctx context.Context, userID uuid.UUID, stateFilter string) ([]Environment, error) {
	args := []any{userID}
	q := `
		SELECT environment_id, user_id, worker_kind, machine_name, os_arch,
		       git_info, capabilities, public_key, pool_tag, device_id, state,
		       created_at, last_seen_at
		  FROM agent_environments
		 WHERE user_id = $1
	`
	if stateFilter != "" {
		args = append(args, stateFilter)
		q += ` AND state = $2`
	} else {
		// 默认: 只 online/draining + 最近 1h 的 offline
		q += ` AND (state IN ('online', 'draining') OR last_seen_at > NOW() - INTERVAL '` + listOfflineMaxAge + `')`
	}
	q += fmt.Sprintf(` ORDER BY last_seen_at DESC LIMIT %d`, listLimit)

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list environments: %w", err)
	}
	defer rows.Close()

	var out []Environment
	for rows.Next() {
		var e Environment
		if err := rows.Scan(
			&e.EnvironmentID, &e.UserID, &e.WorkerKind, &e.MachineName,
			&e.OsArch, &e.GitInfo, &e.Capabilities, &e.PublicKey,
			&e.PoolTag, &e.DeviceID, &e.State, &e.CreatedAt, &e.LastSeenAt,
		); err != nil {
			return nil, fmt.Errorf("scan environment: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// GetEnvironment 取单条；userID 严格匹配。主要给 internal 路由 / debug 用。
func (s *Store) GetEnvironment(ctx context.Context, userID, envID uuid.UUID) (*Environment, error) {
	const q = `
		SELECT environment_id, user_id, worker_kind, machine_name, os_arch,
		       git_info, capabilities, public_key, pool_tag, device_id, state,
		       created_at, last_seen_at
		  FROM agent_environments
		 WHERE environment_id = $1 AND user_id = $2
	`
	var e Environment
	err := s.pool.QueryRow(ctx, q, envID, userID).Scan(
		&e.EnvironmentID, &e.UserID, &e.WorkerKind, &e.MachineName,
		&e.OsArch, &e.GitInfo, &e.Capabilities, &e.PublicKey,
		&e.PoolTag, &e.DeviceID, &e.State, &e.CreatedAt, &e.LastSeenAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get environment: %w", err)
	}
	return &e, nil
}

// ─── agent_sessions（S3-9 + S3-6 共用） ──────────────────────

// CreateSessionReq 是 InsertSession 的输入。Mode / EnvironmentID 由路由层
// （S3-6）决定；store 不做语义校验。
type CreateSessionReq struct {
	UserID        uuid.UUID
	EnvironmentID *uuid.UUID
	ThreadID      *uuid.UUID
	Mode          string // 'chat' | 'agent' | 'task'
	Model         string
	SystemPrompt  string
	// RuntimeEnvMode 是工具执行环境（轴 B）：'none' | 'local' | 'cloud'。
	// 空 → DB 默认 'none'。路由层按 mode 推默认后传入。
	RuntimeEnvMode string
	// Backend（R3/Q3）：'biumindkit'(默认/空) | 'claude-cli' | 'codex-cli'。
	Backend string
	// State（R7）：初始 state。空 → DB 默认 'active'。离线 agent 任务排队时
	// 传 'pending'。
	State string
}

// Session 是 agent_sessions 行的 Go 表示。state 默认 'active'。
type Session struct {
	SessionID      uuid.UUID
	UserID         uuid.UUID
	EnvironmentID  *uuid.UUID
	ThreadID       *uuid.UUID
	Mode           string
	State          string
	Model          string
	SystemPrompt   string
	RuntimeEnvMode string
	Backend        string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// InsertSession 写一行新 session。返回完整 Session（含生成的 session_id +
// 默认 state="active"）。S3-6 路由层用；S3-9 测试也用来 seed 数据。
func (s *Store) InsertSession(ctx context.Context, req CreateSessionReq) (*Session, error) {
	sessionID := uuid.New()
	renv := req.RuntimeEnvMode
	if renv == "" {
		renv = "none" // DB CHECK 不接受空串；空 → none（无外设）
	}
	backend := req.Backend
	if backend == "" {
		backend = "biumindkit" // 空 → 内建 loop
	}
	state := req.State
	if state == "" {
		state = "active" // 空 → DB 默认；显式传保证一致
	}
	const q = `
		INSERT INTO agent_sessions
			(session_id, user_id, environment_id, thread_id, mode, model, system_prompt, runtime_env_mode, backend, state)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING session_id, user_id, environment_id, thread_id, mode, state,
		          COALESCE(model, ''), COALESCE(system_prompt, ''), runtime_env_mode, backend,
		          created_at, updated_at
	`
	var sess Session
	err := s.pool.QueryRow(ctx, q,
		sessionID, req.UserID, req.EnvironmentID, req.ThreadID, req.Mode,
		nullableString(req.Model), nullableString(req.SystemPrompt), renv, backend, state,
	).Scan(
		&sess.SessionID, &sess.UserID, &sess.EnvironmentID, &sess.ThreadID,
		&sess.Mode, &sess.State, &sess.Model, &sess.SystemPrompt, &sess.RuntimeEnvMode, &sess.Backend,
		&sess.CreatedAt, &sess.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("insert session: %w", err)
	}
	return &sess, nil
}

// GetSession 取单条；userID 严格匹配避免跨用户访问。Refresh-token 等
// 端点用。
func (s *Store) GetSession(ctx context.Context, userID, sessionID uuid.UUID) (*Session, error) {
	const q = `
		SELECT session_id, user_id, environment_id, thread_id, mode, state,
		       COALESCE(model, ''), COALESCE(system_prompt, ''), runtime_env_mode, backend,
		       created_at, updated_at
		  FROM agent_sessions
		 WHERE session_id = $1 AND user_id = $2
	`
	var sess Session
	err := s.pool.QueryRow(ctx, q, sessionID, userID).Scan(
		&sess.SessionID, &sess.UserID, &sess.EnvironmentID, &sess.ThreadID,
		&sess.Mode, &sess.State, &sess.Model, &sess.SystemPrompt, &sess.RuntimeEnvMode, &sess.Backend,
		&sess.CreatedAt, &sess.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get session: %w", err)
	}
	return &sess, nil
}

// PickRuntimeEnvironment 从 runtime 池里选一个 online environment。Task 模式
// 调度用 —— 调用方可指定 poolTag 限制（"runtime-prod" / "runtime-gpu"），
// 空字符串则不限。
//
// 选择策略：last_seen_at DESC 取最新心跳的（粗略代理"最不忙"）。真正的
// 负载均衡（per-env 跑中 work 计数 / latency 直方图）等 v2，当前 v1 简单
// 优先。
//
// 返回 ErrNotFound 当池子里没合格 environment，调用方映射 503/404。
func (s *Store) PickRuntimeEnvironment(ctx context.Context, poolTag string) (*Environment, error) {
	args := []any{}
	q := `
		SELECT environment_id, user_id, worker_kind, machine_name, os_arch,
		       git_info, capabilities, public_key, pool_tag, device_id, state,
		       created_at, last_seen_at
		  FROM agent_environments
		 WHERE worker_kind = 'runtime' AND state = 'online'
	`
	if poolTag != "" {
		args = append(args, poolTag)
		q += ` AND pool_tag = $1`
	}
	q += ` ORDER BY last_seen_at DESC LIMIT 1`

	var e Environment
	err := s.pool.QueryRow(ctx, q, args...).Scan(
		&e.EnvironmentID, &e.UserID, &e.WorkerKind, &e.MachineName,
		&e.OsArch, &e.GitInfo, &e.Capabilities, &e.PublicKey,
		&e.PoolTag, &e.DeviceID, &e.State, &e.CreatedAt, &e.LastSeenAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("pick runtime: %w", err)
	}
	return &e, nil
}

// SessionResult 是 agent_session_results 行的输入。Task 模式 finalize 写。
// status: 'completed' | 'failed' | 'cancelled' —— DB 有 CHECK 约束。
type SessionResult struct {
	SessionID        uuid.UUID
	Status           string
	FinalText        string
	FinalParts       []byte // JSONB raw
	ToolCallsSummary []byte // JSONB raw
	CostUSD          float64
	PromptTokens     int
	CompletionTokens int
	DurationMs       int64
	ErrorMessage     string
}

// InsertSessionResult 写一行最终态摘要。session_id 是 PK，重复 insert 会
// 冲突 —— 调用方应该确保 finalize 只调一次（ingress 的 SDKResultMessage
// hook 天然只触发一次）。
func (s *Store) InsertSessionResult(ctx context.Context, r SessionResult) error {
	const q = `
		INSERT INTO agent_session_results
			(session_id, status, final_text, final_parts, tool_calls_summary,
			 cost_usd, prompt_tokens, completion_tokens, duration_ms, error_message)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`
	_, err := s.pool.Exec(ctx, q,
		r.SessionID, r.Status,
		nullableString(r.FinalText),
		nullableBytes(r.FinalParts),
		nullableBytes(r.ToolCallsSummary),
		r.CostUSD, r.PromptTokens, r.CompletionTokens, r.DurationMs,
		nullableString(r.ErrorMessage),
	)
	if err != nil {
		return fmt.Errorf("insert session_result: %w", err)
	}
	return nil
}

// UpdateSessionState —— 改 agent_sessions.state。finalize 时把 active →
// completed/failed/cancelled。state 已是终态时跳过 update（幂等）。
func (s *Store) UpdateSessionState(ctx context.Context, sessionID uuid.UUID, state string) error {
	const q = `
		UPDATE agent_sessions
		   SET state = $2, updated_at = now()
		 WHERE session_id = $1
		   AND state NOT IN ('completed', 'failed', 'cancelled')
	`
	_, err := s.pool.Exec(ctx, q, sessionID, state)
	if err != nil {
		return fmt.Errorf("update session state: %w", err)
	}
	return nil
}

// UpdateSessionEnvAndState（R7）—— 离线任务派发时把 pending session 关联到设备
// 重连后的新 environment 并置 active。仅当当前 state='pending' 时生效（幂等：
// 并发重派只第一个成功）。返回是否更新了行。
func (s *Store) UpdateSessionEnvAndState(ctx context.Context, sessionID, envID uuid.UUID, state string) (bool, error) {
	const q = `
		UPDATE agent_sessions
		   SET environment_id = $2, state = $3, updated_at = now()
		 WHERE session_id = $1 AND state = 'pending'
	`
	tag, err := s.pool.Exec(ctx, q, sessionID, envID, state)
	if err != nil {
		return false, fmt.Errorf("update session env+state: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// ─── agent_pending_work（R7 离线 agent 任务队列）─────────────

// PendingWork 是 agent_pending_work 行——离线设备的排队 agent work。重派所需
// 请求参数，**不含 BYOK**（派发时按 ProviderID 重解析）。
type PendingWork struct {
	PendingID      uuid.UUID
	SessionID      uuid.UUID
	UserID         uuid.UUID
	DeviceID       uuid.UUID
	Prompt         string
	Model          string
	ProviderID     string
	SystemPrompt   string
	ThreadID       string
	Workdir        string
	RuntimeEnvMode string
	Backend        string
}

// CountPendingWorkByDevice 返回某设备当前挂起 work 数（用于上限）。
func (s *Store) CountPendingWorkByDevice(ctx context.Context, deviceID uuid.UUID) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM agent_pending_work WHERE device_id = $1`, deviceID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count pending work: %w", err)
	}
	return n, nil
}

// InsertPendingWorkIfUnderLimit 原子地"检查上限 + 插入"：单条 INSERT...SELECT，
// 仅当该 device 现有挂起数 < limit 时才真正插入，返回是否插入。
//
// 比"先 CountPendingWorkByDevice 再 InsertPendingWork"两步关掉了应用层 round-trip
// 之间的 TOCTOU 窗口。注意：READ COMMITTED 下两条并发语句仍可能都看到 count<limit
// 而各插一条、轻微越限一两条——但这是**软上限**（防单设备堆积），非安全边界，
// 接受。真要硬上限需 SERIALIZABLE 或行锁，对这个场景不划算。
func (s *Store) InsertPendingWorkIfUnderLimit(ctx context.Context, pw PendingWork, expiresAt time.Time, limit int) (bool, error) {
	const q = `
		INSERT INTO agent_pending_work
			(pending_id, session_id, user_id, device_id, prompt, model, provider_id,
			 system_prompt, thread_id, workdir, runtime_env_mode, backend, expires_at)
		SELECT $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13
		 WHERE (SELECT count(*) FROM agent_pending_work WHERE device_id = $4) < $14
	`
	tag, err := s.pool.Exec(ctx, q,
		uuid.New(), pw.SessionID, pw.UserID, pw.DeviceID,
		nullableString(pw.Prompt), nullableString(pw.Model), nullableString(pw.ProviderID),
		nullableString(pw.SystemPrompt), parseOptionalUUID(pw.ThreadID), nullableString(pw.Workdir),
		nullableString(pw.RuntimeEnvMode), nullableString(pw.Backend), expiresAt, limit,
	)
	if err != nil {
		return false, fmt.Errorf("insert pending work (limited): %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// InsertPendingWork 写一条排队 work（无上限检查）。expiresAt 由调用方给（如
// now+7d）。janitor 测试 / 内部 seed 用；生产路由走 InsertPendingWorkIfUnderLimit。
func (s *Store) InsertPendingWork(ctx context.Context, pw PendingWork, expiresAt time.Time) error {
	const q = `
		INSERT INTO agent_pending_work
			(pending_id, session_id, user_id, device_id, prompt, model, provider_id,
			 system_prompt, thread_id, workdir, runtime_env_mode, backend, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
	`
	_, err := s.pool.Exec(ctx, q,
		uuid.New(), pw.SessionID, pw.UserID, pw.DeviceID,
		nullableString(pw.Prompt), nullableString(pw.Model), nullableString(pw.ProviderID),
		nullableString(pw.SystemPrompt), parseOptionalUUID(pw.ThreadID), nullableString(pw.Workdir),
		nullableString(pw.RuntimeEnvMode), nullableString(pw.Backend), expiresAt,
	)
	if err != nil {
		return fmt.Errorf("insert pending work: %w", err)
	}
	return nil
}

// ListPendingWorkByDevice 取某设备所有挂起 work（设备重连重派用）。
func (s *Store) ListPendingWorkByDevice(ctx context.Context, deviceID uuid.UUID) ([]PendingWork, error) {
	const q = `
		SELECT pending_id, session_id, user_id, device_id,
		       COALESCE(prompt,''), COALESCE(model,''), COALESCE(provider_id,''),
		       COALESCE(system_prompt,''), COALESCE(thread_id::text,''), COALESCE(workdir,''),
		       COALESCE(runtime_env_mode,''), COALESCE(backend,'')
		  FROM agent_pending_work WHERE device_id = $1 ORDER BY created_at ASC
	`
	rows, err := s.pool.Query(ctx, q, deviceID)
	if err != nil {
		return nil, fmt.Errorf("list pending work: %w", err)
	}
	defer rows.Close()
	var out []PendingWork
	for rows.Next() {
		var pw PendingWork
		if err := rows.Scan(&pw.PendingID, &pw.SessionID, &pw.UserID, &pw.DeviceID,
			&pw.Prompt, &pw.Model, &pw.ProviderID, &pw.SystemPrompt, &pw.ThreadID,
			&pw.Workdir, &pw.RuntimeEnvMode, &pw.Backend); err != nil {
			return nil, fmt.Errorf("scan pending work: %w", err)
		}
		out = append(out, pw)
	}
	return out, rows.Err()
}

// DeletePendingWork 删一条（派发成功后）。
func (s *Store) DeletePendingWork(ctx context.Context, pendingID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM agent_pending_work WHERE pending_id = $1`, pendingID)
	if err != nil {
		return fmt.Errorf("delete pending work: %w", err)
	}
	return nil
}

// nullableBytes —— 跟 nullableString 同语义但作用于 JSONB raw bytes。
func nullableBytes(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}

// nullableString —— 空字符串视作 SQL NULL。让"未提供 model"跟"提供空字符
// 串"在 DB 中是同一种状态（都是 NULL），简化查询。
func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
