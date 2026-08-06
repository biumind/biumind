package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// MPSubscription — 小程序订阅消息授权一行.
//
// 同 (user, platform, template_id) 多次授权时为多行 (user 可多次点); 也可
// 实现成 INSERT ... ON CONFLICT 累加 times_remaining — 二选一. 当前选多行
// 路径, 便于审计 "什么时候授权的" 与按时间消费.
type MPSubscription struct {
	ID             uuid.UUID
	UserID         uuid.UUID
	Platform       string
	OpenID         string
	TemplateID     string
	TimesRemaining int
	GrantedAt      time.Time
	LastSentAt     *time.Time
}

const mpSubColumns = `id, user_id, platform, openid, template_id,
	times_remaining, granted_at, last_sent_at`

func scanMPSubscription(row interface {
	Scan(...any) error
}) (*MPSubscription, error) {
	s := &MPSubscription{}
	err := row.Scan(
		&s.ID, &s.UserID, &s.Platform, &s.OpenID, &s.TemplateID,
		&s.TimesRemaining, &s.GrantedAt, &s.LastSentAt,
	)
	return s, err
}

// CreateMPSubscription 用户客户端 requestSubscribeMessage 拿到授权后上报.
// times 是这次拿到几次 (微信一次性订阅每点一次得 1, 长期订阅 = 永久 = 9999).
func (s *Store) CreateMPSubscription(
	ctx context.Context,
	userID uuid.UUID, platform, openID, templateID string, times int,
) (*MPSubscription, error) {
	if times <= 0 {
		times = 1
	}
	row := s.pool.QueryRow(ctx, `
		INSERT INTO identity.mp_subscriptions
		    (user_id, platform, openid, template_id, times_remaining)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING `+mpSubColumns+`
	`, userID, platform, openID, templateID, times)
	return scanMPSubscription(row)
}

// ListMPSubscriptionsByUser me 页面/调试用 — 该用户当前所有授权.
func (s *Store) ListMPSubscriptionsByUser(ctx context.Context, userID uuid.UUID) ([]*MPSubscription, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+mpSubColumns+`
		FROM identity.mp_subscriptions
		WHERE user_id = $1
		ORDER BY granted_at DESC
		LIMIT 200
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*MPSubscription
	for rows.Next() {
		s, err := scanMPSubscription(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ConsumeMPSubscription notify worker 发送一条订阅消息成功后扣 1.
// times_remaining 已 0 时不会被选出 — 这里只兜底防并发减到负数.
// 返 ErrNotFound 表示该 row 已经被另一个 worker 消费了, 调用方跳过即可.
func (s *Store) ConsumeMPSubscription(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE identity.mp_subscriptions
		SET times_remaining = times_remaining - 1,
		    last_sent_at    = now()
		WHERE id = $1 AND times_remaining > 0
	`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// PickMPSubscriptionsForDispatch worker 调度入口 — 拿一批可用的授权按平台分组.
// 当前最简实现 (按 template_id + platform 拉一批), 真上线时可加 SKIP LOCKED
// 让多 worker 并发. 单测足够.
func (s *Store) PickMPSubscriptionsForDispatch(
	ctx context.Context, platform, templateID string, limit int,
) ([]*MPSubscription, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+mpSubColumns+`
		FROM identity.mp_subscriptions
		WHERE platform = $1 AND template_id = $2 AND times_remaining > 0
		ORDER BY granted_at ASC
		LIMIT $3
	`, platform, templateID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*MPSubscription
	for rows.Next() {
		s, err := scanMPSubscription(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

