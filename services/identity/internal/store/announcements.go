// 公告 / 通知 inbox 持久层(PERI-4)。admin 发布、客户端拉取 + per-user 读态。
package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Announcement 是 identity.announcements 一行。
type Announcement struct {
	ID            uuid.UUID
	Level         string // info | warning | error
	Title         string
	Body          string
	BodyZh        string
	URL           string
	MinAppVersion string
	MaxAppVersion string
	Published     bool
	CreatedAt     time.Time
	ExpiresAt     *time.Time
}

// AnnouncementForUser 是面向客户端的投影,带 per-user 读态。
type AnnouncementForUser struct {
	Announcement
	IsRead bool
}

// CreateAnnouncementInput 是 admin 发布公告的入参。
type CreateAnnouncementInput struct {
	Level         string
	Title         string
	Body          string
	BodyZh        string
	URL           string
	MinAppVersion string
	MaxAppVersion string
	Published     bool
	ExpiresAt     *time.Time
}

// CreateAnnouncement 新建公告,返回完整行。
func (s *Store) CreateAnnouncement(ctx context.Context, in CreateAnnouncementInput) (*Announcement, error) {
	level := in.Level
	if level == "" {
		level = "info"
	}
	var a Announcement
	err := s.pool.QueryRow(ctx, `
		INSERT INTO identity.announcements
			(level, title, body, body_zh, url, min_app_version, max_app_version, published, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id, level, title, body, body_zh, url, min_app_version, max_app_version, published, created_at, expires_at
	`, level, in.Title, in.Body, in.BodyZh, in.URL, in.MinAppVersion, in.MaxAppVersion, in.Published, in.ExpiresAt,
	).Scan(&a.ID, &a.Level, &a.Title, &a.Body, &a.BodyZh, &a.URL,
		&a.MinAppVersion, &a.MaxAppVersion, &a.Published, &a.CreatedAt, &a.ExpiresAt)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// UpdateAnnouncement 全量更新某公告(admin 编辑)。
func (s *Store) UpdateAnnouncement(ctx context.Context, id uuid.UUID, in CreateAnnouncementInput) error {
	level := in.Level
	if level == "" {
		level = "info"
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE identity.announcements
		SET level=$2, title=$3, body=$4, body_zh=$5, url=$6,
		    min_app_version=$7, max_app_version=$8, published=$9, expires_at=$10
		WHERE id=$1
	`, id, level, in.Title, in.Body, in.BodyZh, in.URL,
		in.MinAppVersion, in.MaxAppVersion, in.Published, in.ExpiresAt)
	return err
}

// DeleteAnnouncement 删公告(级联删读态)。
func (s *Store) DeleteAnnouncement(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM identity.announcements WHERE id=$1`, id)
	return err
}

// ListAnnouncements 列全部公告(admin,含草稿),按创建倒序。
func (s *Store) ListAnnouncements(ctx context.Context) ([]*Announcement, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, level, title, body, body_zh, url, min_app_version, max_app_version, published, created_at, expires_at
		FROM identity.announcements
		ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Announcement
	for rows.Next() {
		var a Announcement
		if err := rows.Scan(&a.ID, &a.Level, &a.Title, &a.Body, &a.BodyZh, &a.URL,
			&a.MinAppVersion, &a.MaxAppVersion, &a.Published, &a.CreatedAt, &a.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, &a)
	}
	return out, rows.Err()
}

// ListActiveAnnouncementsForUser 返回对该用户可见的已发布、未过期公告,带读态。
// 版本门槛(min/max_app_version)在 Go 侧过滤(见 handler),此处只管发布态/过期/读态。
func (s *Store) ListActiveAnnouncementsForUser(ctx context.Context, userID uuid.UUID) ([]*AnnouncementForUser, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT a.id, a.level, a.title, a.body, a.body_zh, a.url,
		       a.min_app_version, a.max_app_version, a.published, a.created_at, a.expires_at,
		       (r.user_id IS NOT NULL) AS is_read
		FROM identity.announcements a
		LEFT JOIN identity.announcement_reads r
		       ON r.announcement_id = a.id AND r.user_id = $1
		WHERE a.published = true
		  AND (a.expires_at IS NULL OR a.expires_at > now())
		ORDER BY a.created_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*AnnouncementForUser
	for rows.Next() {
		var a AnnouncementForUser
		if err := rows.Scan(&a.ID, &a.Level, &a.Title, &a.Body, &a.BodyZh, &a.URL,
			&a.MinAppVersion, &a.MaxAppVersion, &a.Published, &a.CreatedAt, &a.ExpiresAt,
			&a.IsRead); err != nil {
			return nil, err
		}
		out = append(out, &a)
	}
	return out, rows.Err()
}

// MarkAnnouncementRead 标记某公告对该用户已读(幂等 upsert)。
func (s *Store) MarkAnnouncementRead(ctx context.Context, userID, announcementID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO identity.announcement_reads (user_id, announcement_id)
		VALUES ($1, $2)
		ON CONFLICT (user_id, announcement_id) DO NOTHING
	`, userID, announcementID)
	return err
}

// MarkAllAnnouncementsRead 标记当前所有已发布公告对该用户已读。
func (s *Store) MarkAllAnnouncementsRead(ctx context.Context, userID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO identity.announcement_reads (user_id, announcement_id)
		SELECT $1, a.id FROM identity.announcements a
		WHERE a.published = true AND (a.expires_at IS NULL OR a.expires_at > now())
		ON CONFLICT (user_id, announcement_id) DO NOTHING
	`, userID)
	return err
}
