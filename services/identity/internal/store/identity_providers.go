package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// IdentityProvider 第三方登录身份映射. 一个 BiuMind user 可挂 N 个 provider.
//
// 关键约束: (Provider, ProviderUserID) 唯一 — 一个外部账号只能绑到一个
// BiuMind user. 已被绑定的外部账号要换绑必须先 Delete.
type IdentityProvider struct {
	ID             uuid.UUID
	UserID         uuid.UUID
	Provider       string // 'wechat_mp' / 'alipay_mp' / 'toutiao_mp' / ...
	ProviderUserID string // 各家 openid
	UnionID        *string
	RawProfileJSON []byte // jsonb raw, 调用方按需 unmarshal
	BoundAt        time.Time
	LastLoginAt    *time.Time
}

// Provider 字符串枚举集中管理 — 跨文件改名只改这一处.
const (
	ProviderWechatMP   = "wechat_mp"
	ProviderWechatOA   = "wechat_oa"
	ProviderWechatOpen = "wechat_open"
	ProviderAlipayMP   = "alipay_mp"
	ProviderToutiaoMP  = "toutiao_mp"
	ProviderBaiduMP    = "baidu_mp"
	ProviderQQMP       = "qq_mp"
	ProviderKuaishouMP = "kuaishou_mp"
	ProviderJDMP       = "jd_mp"
	ProviderLarkMP     = "lark_mp"
)

// IsWechatEcosystem — 微信开放平台体系内, unionid 跨这几家可合并.
// 跨厂商 (wechat ↔ alipay) 不在此列, 必须用户手动绑定.
func IsWechatEcosystem(provider string) bool {
	switch provider {
	case ProviderWechatMP, ProviderWechatOA, ProviderWechatOpen:
		return true
	}
	return false
}

const identityProviderColumns = `id, user_id, provider, provider_user_id,
	unionid, raw_profile_json, bound_at, last_login_at`

func scanIdentityProvider(row interface {
	Scan(...any) error
}) (*IdentityProvider, error) {
	p := &IdentityProvider{}
	err := row.Scan(
		&p.ID, &p.UserID, &p.Provider, &p.ProviderUserID,
		&p.UnionID, &p.RawProfileJSON, &p.BoundAt, &p.LastLoginAt,
	)
	return p, err
}

// FindIdentityProvider 按 (provider, provider_user_id) 精确查. 登录路径主入口.
func (s *Store) FindIdentityProvider(ctx context.Context, provider, providerUserID string) (*IdentityProvider, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT `+identityProviderColumns+`
		FROM identity.identity_providers
		WHERE provider = $1 AND provider_user_id = $2
	`, provider, providerUserID)
	p, err := scanIdentityProvider(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return p, err
}

// FindIdentityProviderByUnionID 微信生态合并入口 — 给定 unionid, 找已存在的
// 任意一条微信生态记录 (mp / oa / open). 命中即返回 user_id 用于合并.
// 不保证返回稳定的 provider — 调用方只关心 user_id.
func (s *Store) FindIdentityProviderByUnionID(ctx context.Context, unionID string) (*IdentityProvider, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT `+identityProviderColumns+`
		FROM identity.identity_providers
		WHERE unionid = $1
		  AND provider IN ('wechat_mp', 'wechat_oa', 'wechat_open')
		ORDER BY bound_at ASC
		LIMIT 1
	`, unionID)
	p, err := scanIdentityProvider(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return p, err
}

// ListIdentityProvidersByUser me 页面用 — 列出该用户绑过的所有第三方登录.
// 按 bound_at 升序 (先绑的在前) — 主登录方式优先展示.
func (s *Store) ListIdentityProvidersByUser(ctx context.Context, userID uuid.UUID) ([]*IdentityProvider, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+identityProviderColumns+`
		FROM identity.identity_providers
		WHERE user_id = $1
		ORDER BY bound_at ASC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*IdentityProvider
	for rows.Next() {
		p, err := scanIdentityProvider(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// CreateIdentityProvider 新建一条第三方身份映射. (provider, provider_user_id)
// 唯一冲突时返 ErrDuplicate. unionid 可空; rawProfileJSON 传 nil 时用 '{}'.
func (s *Store) CreateIdentityProvider(
	ctx context.Context,
	userID uuid.UUID, provider, providerUserID string,
	unionID *string, rawProfileJSON []byte,
) (*IdentityProvider, error) {
	if rawProfileJSON == nil {
		rawProfileJSON = []byte("{}")
	}
	row := s.pool.QueryRow(ctx, `
		INSERT INTO identity.identity_providers
		    (user_id, provider, provider_user_id, unionid, raw_profile_json, last_login_at)
		VALUES ($1, $2, $3, $4, $5, now())
		RETURNING `+identityProviderColumns+`
	`, userID, provider, providerUserID, unionID, rawProfileJSON)
	p, err := scanIdentityProvider(row)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrDuplicate
		}
		return nil, err
	}
	return p, nil
}

// TouchIdentityProviderLogin 标记最近一次登录时间 — 用于 me 页面"最近活跃".
// 失败不阻塞登录主流程, 调用方 fire-and-forget.
func (s *Store) TouchIdentityProviderLogin(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE identity.identity_providers
		SET last_login_at = now()
		WHERE id = $1
	`, id)
	return err
}

// DeleteIdentityProvider 解绑. 双 WHERE (id, user_id) 防越权.
// 调用方负责保证 "至少保留一种登录方式" 业务规则.
func (s *Store) DeleteIdentityProvider(ctx context.Context, id, userID uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM identity.identity_providers
		WHERE id = $1 AND user_id = $2
	`, id, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// CountIdentityProvidersByUser "至少保留一种登录方式" 校验用. 包含密码登录
// 时调用方还要查 users.password_hash 是否非空 — 这里只算第三方.
func (s *Store) CountIdentityProvidersByUser(ctx context.Context, userID uuid.UUID) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM identity.identity_providers WHERE user_id = $1
	`, userID).Scan(&n)
	return n, err
}
