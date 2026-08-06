package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// OAuthState — H5 OAuth 授权码流的临时 state 行.
//
// state 是 32 字节随机串 (base64url), 由 caller 生成. 这里只负责存 / 取 / 删.
// 一次性使用: callback 验证完即 DELETE — 同一个 state 被重放第二次会拿不到行.
type OAuthState struct {
	State     string
	Provider  string
	ReturnURL string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// CreateOAuthState 写一条 state. ttl 应在 5min 左右; 太长 state 表会膨胀,
// 太短用户在微信里授权慢一点就过期.
func (s *Store) CreateOAuthState(
	ctx context.Context, state, provider, returnURL string, ttl time.Duration,
) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO identity.oauth_states (state, provider, return_url, expires_at)
		VALUES ($1, $2, $3, now() + $4::interval)
	`, state, provider, returnURL, ttl.String())
	if err != nil && isUniqueViolation(err) {
		return ErrDuplicate
	}
	return err
}

// ConsumeOAuthState 校验 state 并立刻删除 (一次性).
//
// 返回:
//   - (state, nil)            正常: 已删, 可继续登录流
//   - (nil, ErrNotFound)      state 不存在 — 可能伪造或已被消费
//   - (nil, 其他 err)          DB 错误
//
// 用 RETURNING 确保 SELECT + DELETE 原子: 防并发 callback 让两个请求都拿到行.
func (s *Store) ConsumeOAuthState(ctx context.Context, state string) (*OAuthState, error) {
	row := s.pool.QueryRow(ctx, `
		DELETE FROM identity.oauth_states
		WHERE state = $1 AND expires_at > now()
		RETURNING state, provider, return_url, created_at, expires_at
	`, state)
	var st OAuthState
	if err := row.Scan(
		&st.State, &st.Provider, &st.ReturnURL, &st.CreatedAt, &st.ExpiresAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &st, nil
}

// GCExpiredOAuthStates 删过期 state. 启动时跑一次 + 每小时跑一次足够;
// 表小不需要专门 worker. 返回受影响行数, 用于 metrics.
func (s *Store) GCExpiredOAuthStates(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM identity.oauth_states WHERE expires_at <= now()
	`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
