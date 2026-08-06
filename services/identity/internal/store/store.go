// Package store is the data access layer for Identity.
package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound  = errors.New("not found")
	ErrDuplicate = errors.New("duplicate")
)

type User struct {
	ID                 uuid.UUID
	Email              string
	PasswordHash       *string
	DisplayName        string
	DefaultOrgID       *uuid.UUID
	Role               string // 'user' | 'support' | 'finance' | 'ops' | 'admin' | 'superadmin' | 'viewer'
	Plan               string // 'free' | 'pro' | 'team'
	RoleAssignedAt     *time.Time
	RoleAssignedBy     *uuid.UUID
	RoleAssignedReason *string
	// EmailVerifiedAt — nil 表示账号尚未通过邮件验证, 不允许 login.
	// 既存账号 (migration 00005 之前注册) 由 SQL 回填为 created_at, 视为已验证.
	EmailVerifiedAt *time.Time
	CreatedAt       time.Time
}

// EmailVerification — 单条邮箱验证 code 记录. code 本身不入库, 只存 sha256.
type EmailVerification struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	CodeHash   []byte
	ExpiresAt  time.Time
	ConsumedAt *time.Time
	Attempts   int
	CreatedAt  time.Time
}

// PasswordReset — 密码重置 code 记录. 字段跟 EmailVerification 同形,
// 单独建表语义清晰 (一个 code 只能用于一种用途).
type PasswordReset struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	CodeHash   []byte
	ExpiresAt  time.Time
	ConsumedAt *time.Time
	Attempts   int
	CreatedAt  time.Time
}

type RefreshToken struct {
	ID             uuid.UUID
	UserID         uuid.UUID
	TokenHash      []byte
	DeviceName     string
	InstallationID string
	// 可观察性字段, 老 row NULL. UI 渲染时兜底 "未知".
	LastUsedAt *time.Time
	LastIP     *string
	LastUA     *string
	// ExpiresAt — sliding window. 每次 RotateRefreshToken 续 RefreshTTL, 让活跃
	// 用户永不掉线. (BiuMind-Identity-Session-Design §3.1)
	ExpiresAt time.Time
	// AbsoluteExpiresAt — 首次签发时定死 = created_at + RefreshAbsoluteTTL,
	// rotation 不重置. 防止永久泄漏的 token 被无限续期。
	AbsoluteExpiresAt time.Time
	RevokedAt         *time.Time
	CreatedAt         time.Time
}

type VirtualKey struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	Prefix     string
	SecretHash []byte
	Name       string
	Scope      []byte // jsonb raw
	ExpiresAt  *time.Time
	RevokedAt  *time.Time
	LastUsedAt *time.Time
	CreatedAt  time.Time
}

type Store struct {
	pool *pgxpool.Pool
}

func New(p *pgxpool.Pool) *Store { return &Store{pool: p} }

// ─── Users ───────────────────────────────────────────────

// userColumns 是所有 SELECT 的列清单, 跟 User 字段顺序一对一.
// 改了 User struct 来这里加; 改了顺序记得改下面所有 Scan.
const userColumns = `id, email, password_hash, display_name, default_org_id,
	role, plan, role_assigned_at, role_assigned_by, role_assigned_reason,
	email_verified_at, created_at`

func scanUser(row interface {
	Scan(...any) error
}) (*User, error) {
	u := &User{}
	err := row.Scan(
		&u.ID, &u.Email, &u.PasswordHash, &u.DisplayName, &u.DefaultOrgID,
		&u.Role, &u.Plan, &u.RoleAssignedAt, &u.RoleAssignedBy, &u.RoleAssignedReason,
		&u.EmailVerifiedAt, &u.CreatedAt,
	)
	return u, err
}

func (s *Store) CreateUser(ctx context.Context, email, hash, displayName string) (*User, error) {
	row := s.pool.QueryRow(ctx, `
		INSERT INTO identity.users (email, password_hash, display_name)
		VALUES ($1, $2, $3)
		RETURNING `+userColumns+`
	`, email, hash, displayName)
	u, err := scanUser(row)
	if err != nil {
		// 23505 = unique violation
		if isUniqueViolation(err) {
			return nil, ErrDuplicate
		}
		return nil, err
	}
	return u, nil
}

func (s *Store) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT `+userColumns+`
		FROM identity.users WHERE email = $1
	`, email)
	u, err := scanUser(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return u, err
}

func (s *Store) GetUserByID(ctx context.Context, id uuid.UUID) (*User, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT `+userColumns+`
		FROM identity.users WHERE id = $1
	`, id)
	u, err := scanUser(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return u, err
}

// ─── Admin ───────────────────────────────────────────────
//
// 后台用接口. 跟普通查询分开是因为:
//   - 普通查询走 user.id 主键, 后台查询要按 email 模糊 + role 过滤
//   - 后台需要 total count (分页 UI), 普通查询不需要
//   - 后台 set plan / set role 是两个独立的 update, 都要落 audit.

// ListUsersForAdmin 后台用户列表. query 模糊匹配 email 子串 (ILIKE).
// 返回当前页 + 总数, 用于分页 UI.
func (s *Store) ListUsersForAdmin(ctx context.Context, query string, limit, offset int) ([]*User, int, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	// 用 q 占位 — 空字符串退化成 '%' (匹配所有, ILIKE '%' 走索引扫描)
	q := "%" + query + "%"

	var total int
	if err := s.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM identity.users WHERE email ILIKE $1
	`, q).Scan(&total); err != nil {
		return nil, 0, err
	}

	rows, err := s.pool.Query(ctx, `
		SELECT `+userColumns+`
		FROM identity.users
		WHERE email ILIKE $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3
	`, q, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []*User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, u)
	}
	return out, total, rows.Err()
}

// SetUserPlan 改用户 plan. 不改 role. plan 经 admin.handleSetPlan 校验过.
func (s *Store) SetUserPlan(ctx context.Context, id uuid.UUID, plan string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE identity.users SET plan = $2, updated_at = now()
		WHERE id = $1
	`, id, plan)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetUserRole 改用户 role. 顺带记录 actor + reason. 调用方负责事务边界
// (撤 session / 写 audit / bump role version 应跟这步在一起).
//
// 注意: 调用方必须先保证业务规则:
//   - 不能改自己 (caller_id != id)
//   - 不能删最后一个 superadmin
//   - 角色字符串在已知集合里
func (s *Store) SetUserRole(ctx context.Context, id uuid.UUID, role string, actorID uuid.UUID, reason string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE identity.users
		SET role = $2,
		    role_assigned_at = now(),
		    role_assigned_by = $3,
		    role_assigned_reason = $4,
		    updated_at = now()
		WHERE id = $1
	`, id, role, actorID, reason)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// CountUsersByRole 用于"不能删最后一个 superadmin"校验.
func (s *Store) CountUsersByRole(ctx context.Context, role string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM identity.users WHERE role = $1
	`, role).Scan(&n)
	return n, err
}

// PromoteByEmail 启动时给 BIUMIND_BOOTSTRAP_SUPERADMINS 列表提升用. 邮箱
// 不存在直接跳过 (用户尚未注册), 等用户注册后下次重启再提升.
// 已是该 role 直接 noop. 写 system actor (00000000-...) 标记为 bootstrap.
func (s *Store) PromoteByEmail(ctx context.Context, email, role string) (promoted bool, err error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE identity.users
		SET role = $2,
		    role_assigned_at = now(),
		    role_assigned_by = '00000000-0000-0000-0000-000000000000'::uuid,
		    role_assigned_reason = 'bootstrap from BIUMIND_BOOTSTRAP_SUPERADMINS env',
		    updated_at = now()
		WHERE email = $1 AND role <> $2
	`, email, role)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// ─── Refresh tokens ──────────────────────────────────────

// CreateOrRotateRefreshToken 落地点是同 (user, installation_id) 上的"设备授权".
// 已有 active 行 → 原地 rotate token_hash + 推后 expires_at + 清空 last_ip/ua
// (refresh 时再回填). 没有 → 新建. installation_id 空 → 永远新建 (老 client
// 兼容路径, partial unique index 不约束空值).
//
// 三层过期 (BiuMind-Identity-Session-Design §3.1):
//   - 新行: expires_at = now + slidingTTL, absolute_expires_at = now + absoluteTTL
//   - 已有 active 行: 续 expires_at, **绝不**动 absolute_expires_at (防永久泄漏)
//
// 返回的 id 是 row id, 写进 JWT claims.DeviceID — 同设备复用时这个 id 稳定.
func (s *Store) CreateOrRotateRefreshToken(
	ctx context.Context,
	userID uuid.UUID, installationID string,
	hash []byte, deviceName string,
	slidingTTL, absoluteTTL time.Duration,
) (uuid.UUID, error) {
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `
		INSERT INTO identity.refresh_tokens
		    (user_id, installation_id, token_hash, device_name,
		     expires_at, absolute_expires_at)
		VALUES ($1, $2, $3, $4,
		        now() + $5::interval,
		        now() + $6::interval)
		ON CONFLICT (user_id, installation_id)
		    WHERE revoked_at IS NULL AND installation_id <> ''
		DO UPDATE SET
		    token_hash   = EXCLUDED.token_hash,
		    device_name  = EXCLUDED.device_name,
		    expires_at   = EXCLUDED.expires_at,
		    last_used_at = now(),
		    last_ip      = NULL,
		    last_ua      = NULL
		    -- absolute_expires_at: 不动. 用户改密 + 旧设备撤销 后才重置.
		RETURNING id
	`, userID, installationID, hash, deviceName,
		slidingTTL.String(), absoluteTTL.String()).Scan(&id)
	return id, err
}

// RotateRefreshToken 是 /v1/auth/refresh 的核心 — 一次事务里 revoke 老行 +
// insert 新行. 跟 CreateOrRotateRefreshToken 的"login 路径"区别:
//
//   - login 是用户输密码, 同设备直接 in-place rotate (老 row id 保留)
//   - refresh 是 token 换 token, 必须留 revoked 老行用于 reuse detection (A3)
//
// 新行继承老行的 user_id, installation_id, device_name, absolute_expires_at;
// 新行 expires_at = now + slidingTTL.
//
// ip / ua 随新行**同事务**落库 (last_ip / last_ua, last_used_at = now):
// 这是迟恢复 (late grace recovery) 判定"重放者是否为轮换发起端"的依据,
// 不能异步 touch — 恢复请求可能先于异步 touch 到达, 产生竞态。
//
// 返回新行 id (= 新 access token 的 session_id) + 新行的 expires_at +
// absolute_expires_at (给客户端响应用).
//
// oldID 必须是 active (revoked_at IS NULL). 如果发现已 revoked, 返回 ErrNotFound,
// caller (handleRefresh) 据此走 grace replay / reuse detection 路径。
//
// successorEnc — grace window 用: 新 refresh_token 明文的 AES-256-GCM 密文
// (encryptGrace 输出), 跟新行 id 一起回写到被 revoke 的老行
// (rotated_to / rotated_token_enc), 形成 rotation 链供 GraceReplayHead 走。
// nil 时老行 rotated_to 仍写 (链不断), 但 replay 因缺密文不命中。
//
// 顺序说明: 必须先 revoke 老行再 insert 新行 — partial unique index
// refresh_tokens_active_device_idx 约束 (user, installation) 同时只有一条
// active, 反过来插会撞 23505。链指针在老行 revoke 后同事务补写。
func (s *Store) RotateRefreshToken(
	ctx context.Context,
	oldID uuid.UUID,
	newHash []byte,
	successorEnc []byte,
	slidingTTL time.Duration,
	ip, ua string,
) (newID uuid.UUID, expiresAt, absoluteExpiresAt time.Time, err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, time.Time{}, time.Time{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// 1) revoke 老行, 拿 installation_id / user_id / device_name / absolute_expires_at.
	// UPDATE ... WHERE revoked_at IS NULL 同时是并发 arbiter: 并发 rotate 在
	// 行锁上串行, 后到者命中 0 行 → ErrNotFound。
	var (
		userID            uuid.UUID
		installationID    string
		deviceName        string
		absoluteExpiresIn time.Time
	)
	err = tx.QueryRow(ctx, `
		UPDATE identity.refresh_tokens
		   SET revoked_at = now()
		 WHERE id = $1 AND revoked_at IS NULL
		RETURNING user_id, installation_id, device_name, absolute_expires_at
	`, oldID).Scan(&userID, &installationID, &deviceName, &absoluteExpiresIn)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, time.Time{}, time.Time{}, ErrNotFound
		}
		return uuid.Nil, time.Time{}, time.Time{}, err
	}

	// 2) insert 新行, 继承 (user_id, installation_id, device_name, absolute_expires_at)
	//    last_ip / last_ua / last_used_at 同事务落库 (迟恢复判定依据, 见上注释)。
	var (
		newRowID  uuid.UUID
		expiresIn time.Time
		ipArg     any
	)
	if ip != "" {
		ipArg = ip
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO identity.refresh_tokens
		    (user_id, installation_id, token_hash, device_name,
		     expires_at, absolute_expires_at,
		     last_used_at, last_ip, last_ua)
		VALUES ($1, $2, $3, $4,
		        now() + $5::interval,
		        $6,
		        now(), $7::inet, NULLIF($8, ''))
		RETURNING id, expires_at
	`, userID, installationID, newHash, deviceName,
		slidingTTL.String(), absoluteExpiresIn, ipArg, ua).Scan(&newRowID, &expiresIn)
	if err != nil {
		return uuid.Nil, time.Time{}, time.Time{}, err
	}

	// 3) 回写 rotation 链指针 (rotated_to) 和新 token 密文到已 revoke 的老行.
	// 行锁仍由本事务持有 (step 1 的 UPDATE), 无并发风险。
	if _, err := tx.Exec(ctx, `
		UPDATE identity.refresh_tokens
		   SET rotated_to        = $2,
		       rotated_token_enc = $3
		 WHERE id = $1
	`, oldID, newRowID, successorEnc); err != nil {
		return uuid.Nil, time.Time{}, time.Time{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, time.Time{}, time.Time{}, err
	}
	return newRowID, expiresIn, absoluteExpiresIn, nil
}

// GraceHead — GraceReplayHead 找到的链 head 信息。
type GraceHead struct {
	ID        uuid.UUID
	ExpiresAt time.Time
	TokenEnc  []byte  // head refresh_token 明文的 AES-256-GCM 密文 (caller 用 RefreshGraceKey 解密)
	Hops      int     // oldID → head 经过的 rotate 跳数 (1 = head 是 oldID 的直接后继)
	IP        *string // head 行的 last_ip (轮换请求来源), 未记录 → nil
	UA        *string // head 行的 last_ua, 未记录 → nil
}

// GraceReplayHead — grace window 内重放老 token 时, 从 oldID 行出发沿
// rotated_to 链走到 revoked_at IS NULL 的 head 行 (上限 5 跳防环)。
//
// 返回的 TokenEnc 是链上最后一枚密文 — A→B→C 链中 B.rotated_token_enc
// 存的是 C 的 token 密文, 即 head 的 refresh_token 密文。
// Hops / IP / UA 给迟恢复 (late grace recovery) 做 "1 跳 + 同端" 判定。
//
// 任一跳行不存在 / rotated_to NULL (logout 等非 rotate 撤销) / 密文 NULL /
// 超 5 跳 → ok=false。查库实时状态, 不依赖 caller 手里的快照。
func (s *Store) GraceReplayHead(
	ctx context.Context,
	oldID uuid.UUID,
) (*GraceHead, bool, error) {
	const maxHops = 5
	cur := oldID
	hops := 0
	var headTokenEnc []byte
	for range maxHops {
		var (
			revokedAt *time.Time
			rotatedTo *uuid.UUID
			enc       []byte
			expiresAt time.Time
			ip, ua    *string
		)
		err := s.pool.QueryRow(ctx, `
			SELECT revoked_at, rotated_to, rotated_token_enc, expires_at,
			       host(last_ip), last_ua
			  FROM identity.refresh_tokens
			 WHERE id = $1
		`, cur).Scan(&revokedAt, &rotatedTo, &enc, &expiresAt, &ip, &ua)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, false, nil
			}
			return nil, false, err
		}
		if revokedAt == nil {
			// 到达 head。cur == oldID 说明老行根本没被 rotate (防御:
			// caller 只在 revoked / ErrNotFound 路径调, 不该走到这)。
			if cur == oldID {
				return nil, false, nil
			}
			return &GraceHead{
				ID: cur, ExpiresAt: expiresAt, TokenEnc: headTokenEnc,
				Hops: hops, IP: ip, UA: ua,
			}, true, nil
		}
		if rotatedTo == nil || len(enc) == 0 {
			return nil, false, nil // 断链
		}
		headTokenEnc = enc
		cur = *rotatedTo
		hops++
	}
	return nil, false, nil // 超 maxHops
}

// refreshTokenColumns + scanRefreshToken: 给 5 个 SELECT 共用 (Find / List / etc),
// 改了字段顺序记得改 scanRefreshToken.
const refreshTokenColumns = `id, user_id, token_hash, device_name, installation_id,
	last_used_at, host(last_ip), last_ua,
	expires_at, absolute_expires_at, revoked_at, created_at`

func scanRefreshToken(row interface {
	Scan(...any) error
}) (*RefreshToken, error) {
	t := &RefreshToken{}
	err := row.Scan(
		&t.ID, &t.UserID, &t.TokenHash, &t.DeviceName, &t.InstallationID,
		&t.LastUsedAt, &t.LastIP, &t.LastUA,
		&t.ExpiresAt, &t.AbsoluteExpiresAt, &t.RevokedAt, &t.CreatedAt,
	)
	return t, err
}

func (s *Store) FindRefreshToken(ctx context.Context, hash []byte) (*RefreshToken, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT `+refreshTokenColumns+`
		FROM identity.refresh_tokens
		WHERE token_hash = $1
	`, hash)
	t, err := scanRefreshToken(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return t, err
}

// ListActiveRefreshTokens 列出该用户当前未撤销 + 未过期的所有 refresh_token.
// "已登录设备"页用. 按最近活跃排前面 (created_at fallback NULL last_used_at).
func (s *Store) ListActiveRefreshTokens(ctx context.Context, userID uuid.UUID) ([]*RefreshToken, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+refreshTokenColumns+`
		FROM identity.refresh_tokens
		WHERE user_id = $1
		  AND revoked_at IS NULL
		  AND expires_at > now()
		ORDER BY COALESCE(last_used_at, created_at) DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*RefreshToken
	for rows.Next() {
		t, err := scanRefreshToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// RevokeRefreshTokenByID 按 (id, user_id) 双 WHERE 撤单条. user_id 校验防止
// A 用户撤 B 用户的 session (即便拿到 B 的 session id 也不能越权).
// 返 ErrNotFound 而非 ErrForbidden — 不暴露 session id 是否存在.
func (s *Store) RevokeRefreshTokenByID(ctx context.Context, sessionID, userID uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE identity.refresh_tokens SET revoked_at = now()
		WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL
	`, sessionID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// RevokeOtherRefreshTokens "踢出其他设备" — 撤该用户除 exceptID 外所有 active.
// 返撤的数量, exceptID 留作"当前 session"保留. exceptID 为 uuid.Nil 时全撤
// (跟 RevokeAllRefreshTokens 等价, 留独立方法表达"用户主动 self-serve").
func (s *Store) RevokeOtherRefreshTokens(ctx context.Context, userID, exceptID uuid.UUID) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE identity.refresh_tokens SET revoked_at = now()
		WHERE user_id = $1 AND id <> $2
		  AND revoked_at IS NULL
		  AND expires_at > now()
	`, userID, exceptID)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func (s *Store) RevokeRefreshToken(ctx context.Context, hash []byte) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE identity.refresh_tokens SET revoked_at = now()
		WHERE token_hash = $1 AND revoked_at IS NULL
	`, hash)
	return err
}

// RevokeAllRefreshTokens 撤销某用户所有未失效的 refresh_token. 用于:
//   - 改 role 后强制重登 (旧 access token 还能用 ≤15min, 新 role 立即生效)
//   - 用户密码泄露后强制下线全平台
//   - admin 主动踢人
//
// 返回受影响行数 (=该用户活跃 session 数). 不存在该用户也返回 0 不报错.
func (s *Store) RevokeAllRefreshTokens(ctx context.Context, userID uuid.UUID) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE identity.refresh_tokens SET revoked_at = now()
		WHERE user_id = $1 AND revoked_at IS NULL
	`, userID)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// RevokeFamilyByInstallation 撤销 (user, installation) 下所有 active
// refresh_token — reuse detection 触发时 (handleRefresh 看到 revoked_at
// != nil 的老 token 被再次提交) 调,把整个"家族"清空。
//
// 攻击场景:
//
//	攻击者偷了 refresh_tokenA → A 用一次 → server rotate 给攻击者 B
//	    用户合法客户端下次 rotate 拿来 A → server 看到 A.revoked_at != nil
//	→ 触发本方法 → B (和后续衍生的所有 token) 全部 revoke
//	→ 用户和攻击者都被踢回登录
//	→ 用户重新输密码新建 family;攻击者无法。
//
// installation_id 空 → no-op 返 0 (老客户端兼容路径不参与家族识别)。
//
// 返回撤销行数, caller 写进 security_events.detail。
func (s *Store) RevokeFamilyByInstallation(
	ctx context.Context,
	userID uuid.UUID, installationID string,
) (int64, error) {
	if installationID == "" {
		return 0, nil
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE identity.refresh_tokens SET revoked_at = now()
		 WHERE user_id = $1
		   AND installation_id = $2
		   AND revoked_at IS NULL
	`, userID, installationID)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// SecurityEvent — identity.security_events 行。reuse detection 等安全
// 事件审计用 (BiuMind-Identity-Session-Design §4.1)。
type SecurityEvent struct {
	UserID uuid.UUID
	Kind   string // 'refresh_token_reuse' | future kinds
	Detail []byte // jsonb raw, caller json.Marshal 一次
	IP     string // "" → NULL
	UA     string // "" → NULL
}

// SecurityEventOut — ListSecurityEvents 返的行,带 created_at 用于
// "最近 24h 内有 reuse?" 的客户端 banner 决策。
type SecurityEventOut struct {
	ID        uuid.UUID
	Kind      string
	Detail    []byte // jsonb raw
	IP        *string
	UA        *string
	CreatedAt time.Time
}

// ListSecurityEvents 返该用户最近 limit 条事件,新的在前。limit ≤ 0 → 50。
//
// "我的安全活动"页 + reuse banner 都用它。banner 只看最近 24h 内的
// refresh_token_reuse;activity 页可能展示更多 kind。
func (s *Store) ListSecurityEvents(
	ctx context.Context, userID uuid.UUID, limit int,
) ([]*SecurityEventOut, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, kind, detail::text::bytea, host(ip), ua, created_at
		  FROM identity.security_events
		 WHERE user_id = $1
		 ORDER BY created_at DESC
		 LIMIT $2
	`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*SecurityEventOut
	for rows.Next() {
		ev := &SecurityEventOut{}
		if err := rows.Scan(&ev.ID, &ev.Kind, &ev.Detail, &ev.IP, &ev.UA, &ev.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

// InsertSecurityEvent 写一行 security_events。失败不阻塞业务 —
// reuse detection 已经把 family revoke 了, 审计行掉了下次再排查也无大碍,
// 但绝不能让审计写失败导致 401 信号丢失。
func (s *Store) InsertSecurityEvent(ctx context.Context, e SecurityEvent) error {
	var ipArg any
	if e.IP != "" {
		ipArg = e.IP
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO identity.security_events (user_id, kind, detail, ip, ua)
		VALUES ($1, $2, $3::jsonb, $4::inet, NULLIF($5, ''))
	`, e.UserID, e.Kind, string(e.Detail), ipArg, e.UA)
	return err
}

// ReapRefreshTokens 物理删除过期数据 — 给后台 reaper 调。
//
// 删除条件:
//   - revoked_at < now() - revokedRetention   已 revoked 且过了 reuse detection
//     保留窗口 (默认 30d)
//   - absolute_expires_at < now() - absExpiredRetention
//     absolute cap 已经过了, 留 7 天
//     给审计/排查后再删
//
// 用 OR 一条 DELETE,batch 受 LIMIT 限制避免大事务卡 vacuum。
//
// 返回删除行数。失败不中断 — caller (reaper goroutine) 会下次重试。
func (s *Store) ReapRefreshTokens(
	ctx context.Context,
	revokedRetention, absExpiredRetention time.Duration,
	batchLimit int,
) (int64, error) {
	if batchLimit <= 0 {
		batchLimit = 500
	}
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM identity.refresh_tokens
		WHERE id IN (
		    SELECT id FROM identity.refresh_tokens
		    WHERE (revoked_at IS NOT NULL AND revoked_at < now() - $1::interval)
		       OR (absolute_expires_at < now() - $2::interval)
		    LIMIT $3
		)
	`, revokedRetention.String(), absExpiredRetention.String(), batchLimit)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// ─── Email verification ──────────────────────────────────
//
// 注册时建用户 + 写一条 verifications. 用户输入 code 后:
//   - GetLatestEmailVerification 拿该用户最新一条 (按 created_at desc)
//   - 比对 code_hash, 失败 → IncEmailVerificationAttempts
//   - 成功 → ConsumeEmailVerification (consumed_at=now()) +
//             MarkUserEmailVerified (users.email_verified_at=now())
// resend 走 InvalidateActiveEmailVerifications + 新建一条.

func (s *Store) CreateEmailVerification(ctx context.Context, userID uuid.UUID, codeHash []byte, ttl time.Duration) (*EmailVerification, error) {
	v := &EmailVerification{}
	err := s.pool.QueryRow(ctx, `
		INSERT INTO identity.email_verifications (user_id, code_hash, expires_at)
		VALUES ($1, $2, now() + $3::interval)
		RETURNING id, user_id, code_hash, expires_at, consumed_at, attempts, created_at
	`, userID, codeHash, ttl.String()).Scan(
		&v.ID, &v.UserID, &v.CodeHash, &v.ExpiresAt, &v.ConsumedAt, &v.Attempts, &v.CreatedAt,
	)
	return v, err
}

// GetLatestEmailVerification 取该用户最新的一条 (无论是否消费/过期).
// 业务层判断 consumed_at / expires_at, 给前端不同错误码.
func (s *Store) GetLatestEmailVerification(ctx context.Context, userID uuid.UUID) (*EmailVerification, error) {
	v := &EmailVerification{}
	err := s.pool.QueryRow(ctx, `
		SELECT id, user_id, code_hash, expires_at, consumed_at, attempts, created_at
		FROM identity.email_verifications
		WHERE user_id = $1
		ORDER BY created_at DESC
		LIMIT 1
	`, userID).Scan(
		&v.ID, &v.UserID, &v.CodeHash, &v.ExpiresAt, &v.ConsumedAt, &v.Attempts, &v.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return v, err
}

// IncEmailVerificationAttempts +1, 返回更新后的 attempts. 用于 5 次错码作废.
func (s *Store) IncEmailVerificationAttempts(ctx context.Context, id uuid.UUID) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `
		UPDATE identity.email_verifications
		SET attempts = attempts + 1
		WHERE id = $1
		RETURNING attempts
	`, id).Scan(&n)
	return n, err
}

// ConsumeEmailVerification 标记 code 已用. 在事务中跟 MarkUserEmailVerified 一起.
func (s *Store) ConsumeEmailVerification(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE identity.email_verifications
		SET consumed_at = now()
		WHERE id = $1 AND consumed_at IS NULL
	`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// InvalidateActiveEmailVerifications 把该用户当前未消费/未过期的 code 全部失效.
// resend 时调用 — 防止旧 code 还能用造成歧义.
func (s *Store) InvalidateActiveEmailVerifications(ctx context.Context, userID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE identity.email_verifications
		SET consumed_at = now()
		WHERE user_id = $1 AND consumed_at IS NULL AND expires_at > now()
	`, userID)
	return err
}

// MarkUserEmailVerified 设置 users.email_verified_at = now(). 调用方在 verify
// 成功后立刻调; 已验证的用户重复调是幂等的 (只更新时间戳).
func (s *Store) MarkUserEmailVerified(ctx context.Context, userID uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE identity.users
		SET email_verified_at = now(), updated_at = now()
		WHERE id = $1
	`, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ─── Password reset ──────────────────────────────────────
//
// 跟 email_verification CRUD 同形 — 业务流程不同 (改密码 vs 验邮箱) 但
// 数据访问几乎一致. 重复几行 SQL 比把两表合一 + 加 'purpose' 字段简单.

func (s *Store) CreatePasswordReset(ctx context.Context, userID uuid.UUID, codeHash []byte, ttl time.Duration) (*PasswordReset, error) {
	v := &PasswordReset{}
	err := s.pool.QueryRow(ctx, `
		INSERT INTO identity.password_resets (user_id, code_hash, expires_at)
		VALUES ($1, $2, now() + $3::interval)
		RETURNING id, user_id, code_hash, expires_at, consumed_at, attempts, created_at
	`, userID, codeHash, ttl.String()).Scan(
		&v.ID, &v.UserID, &v.CodeHash, &v.ExpiresAt, &v.ConsumedAt, &v.Attempts, &v.CreatedAt,
	)
	return v, err
}

func (s *Store) GetLatestPasswordReset(ctx context.Context, userID uuid.UUID) (*PasswordReset, error) {
	v := &PasswordReset{}
	err := s.pool.QueryRow(ctx, `
		SELECT id, user_id, code_hash, expires_at, consumed_at, attempts, created_at
		FROM identity.password_resets
		WHERE user_id = $1
		ORDER BY created_at DESC
		LIMIT 1
	`, userID).Scan(
		&v.ID, &v.UserID, &v.CodeHash, &v.ExpiresAt, &v.ConsumedAt, &v.Attempts, &v.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return v, err
}

func (s *Store) IncPasswordResetAttempts(ctx context.Context, id uuid.UUID) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `
		UPDATE identity.password_resets
		SET attempts = attempts + 1
		WHERE id = $1
		RETURNING attempts
	`, id).Scan(&n)
	return n, err
}

func (s *Store) ConsumePasswordReset(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE identity.password_resets
		SET consumed_at = now()
		WHERE id = $1 AND consumed_at IS NULL
	`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) InvalidateActivePasswordResets(ctx context.Context, userID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE identity.password_resets
		SET consumed_at = now()
		WHERE user_id = $1 AND consumed_at IS NULL AND expires_at > now()
	`, userID)
	return err
}

// UpdateUserDisplayName 改 display_name. 给 me 页面"完善资料" / 微信
// chooseAvatar+nickname 流用. 不改其他字段, 也不动 role/plan.
func (s *Store) UpdateUserDisplayName(ctx context.Context, userID uuid.UUID, displayName string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE identity.users
		SET display_name = $2, updated_at = now()
		WHERE id = $1
	`, userID, displayName)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateUserPassword 改 password_hash. 调用方先 hash, 已校验过 code.
// 失败返 ErrNotFound (id 不存在). 改完调用方应 RevokeAllRefreshTokens
// 强制其它设备重登.
func (s *Store) UpdateUserPassword(ctx context.Context, userID uuid.UUID, hash string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE identity.users
		SET password_hash = $2, updated_at = now()
		WHERE id = $1
	`, userID, hash)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ─── Virtual keys ────────────────────────────────────────

func (s *Store) CreateVirtualKey(ctx context.Context, userID uuid.UUID, prefix string, secretHash []byte, name string, scopeJSON []byte, expiresAt *time.Time) (*VirtualKey, error) {
	v := &VirtualKey{}
	err := s.pool.QueryRow(ctx, `
		INSERT INTO identity.virtual_keys (user_id, prefix, secret_hash, name, scope, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, user_id, prefix, secret_hash, name, scope, expires_at, revoked_at, last_used_at, created_at
	`, userID, prefix, secretHash, name, scopeJSON, expiresAt).Scan(
		&v.ID, &v.UserID, &v.Prefix, &v.SecretHash, &v.Name, &v.Scope,
		&v.ExpiresAt, &v.RevokedAt, &v.LastUsedAt, &v.CreatedAt,
	)
	return v, err
}

func (s *Store) ListVirtualKeys(ctx context.Context, userID uuid.UUID, limit int) ([]*VirtualKey, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, user_id, prefix, secret_hash, name, scope, expires_at, revoked_at, last_used_at, created_at
		FROM identity.virtual_keys
		WHERE user_id = $1 AND revoked_at IS NULL
		ORDER BY created_at DESC LIMIT $2
	`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*VirtualKey
	for rows.Next() {
		v := &VirtualKey{}
		if err := rows.Scan(&v.ID, &v.UserID, &v.Prefix, &v.SecretHash, &v.Name, &v.Scope,
			&v.ExpiresAt, &v.RevokedAt, &v.LastUsedAt, &v.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) RevokeVirtualKey(ctx context.Context, id uuid.UUID, userID uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE identity.virtual_keys SET revoked_at = now()
		WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL
	`, id, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ─── helpers ─────────────────────────────────────────────

func isUniqueViolation(err error) bool {
	// Avoid importing pgconn in interface; use string match (acceptable for narrow check)
	return err != nil && (containsAny(err.Error(),
		"duplicate key value violates unique constraint",
		"23505"))
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
	}
	return false
}
