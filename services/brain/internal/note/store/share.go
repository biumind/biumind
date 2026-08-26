// 笔记分享（公开只读链接）数据访问 —— 表结构见迁移 00005，
// 契约见 docs/BiuMind-Technical-Architecture.md §7.6。
//
// 与 note 主表同一 Store（同 pool、同事务事件模式），差异只在审计事件
// scope：分享事件打在固定 scope `note_share`（审计流，不驱动客户端
// 增量同步，所以不走 note:user:<uid>）。
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ShareEventScope —— 分享审计事件的固定 scope（创建/改密/停用/rotate/
// 密码失败，设计 §7.6）。全局审计流：payload 不放密码、IP 等敏感字段。
const ShareEventScope = "note_share"

// 分享审计事件类型。
const (
	ShareEventCreated      = "note_share.created"
	ShareEventUpdated      = "note_share.updated" // 配置变更（含改密/恢复，payload 带标志）
	ShareEventDisabled     = "note_share.disabled"
	ShareEventRotated      = "note_share.rotated"
	ShareEventUnlockFailed = "note_share.unlock_failed"
)

type Share struct {
	ID                uuid.UUID
	NoteID            uuid.UUID
	UserID            uuid.UUID
	Token             string
	PasswordHash      *string
	ExpiresAt         *time.Time
	CredentialVersion int
	ViewCount         int64
	// MaxViews —— 访问次数上限（S2，迁移 00006）；NULL = 不限。
	// view_count >= max_views 即 exhausted（校验链① 410 / 列表 status）。
	MaxViews   *int
	DisabledAt *time.Time
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// ShareWithTitle —— 「我的分享」管理列表行（join note_notes 取标题）。
type ShareWithTitle struct {
	Share
	NoteTitle string
}

// PublicNote —— 公开端读取的笔记字段（无 user 过滤；DeletedAt 交给
// 校验链判 410 note_deleted）。
type PublicNote struct {
	ID        uuid.UUID
	Title     string
	ContentMD string
	Author    *string
	SourceURL *string
	UpdatedAt time.Time
	DeletedAt *time.Time
}

const shareColumns = `id, note_id, user_id, token, password_hash, expires_at,
	credential_version, view_count, max_views, disabled_at, created_at, updated_at`

func scanShare(row pgx.Row) (*Share, error) {
	sh := &Share{}
	err := row.Scan(
		&sh.ID, &sh.NoteID, &sh.UserID, &sh.Token, &sh.PasswordHash, &sh.ExpiresAt,
		&sh.CredentialVersion, &sh.ViewCount, &sh.MaxViews, &sh.DisabledAt, &sh.CreatedAt, &sh.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return sh, nil
}

// emitShareEventTx —— 业务事务内追加分享审计事件（同 emitEvent 模式，
// scope 固定 note_share）。
func emitShareEventTx(ctx context.Context, tx pgx.Tx, actorType, actorID, eventType string, payload map[string]any) error {
	pl, _ := json.Marshal(payload)
	_, err := tx.Exec(ctx, `
		INSERT INTO brain.events (scope, actor_type, actor_id, event_type, payload)
		VALUES ($1, $2, $3, $4, $5)
	`, ShareEventScope, actorType, actorID, eventType, pl)
	return err
}

// EmitShareEvent —— 无业务事务的审计事件单写（unlock 密码失败等公开端
// 场景），独立连接直接落库。
func (s *Store) EmitShareEvent(ctx context.Context, actorType, actorID, eventType string, payload map[string]any) error {
	pl, _ := json.Marshal(payload)
	_, err := s.pool.Exec(ctx, `
		INSERT INTO brain.events (scope, actor_type, actor_id, event_type, payload)
		VALUES ($1, $2, $3, $4, $5)
	`, ShareEventScope, actorType, actorID, eventType, pl)
	return err
}

// ─── 管理端 ─────────────────────────────────────────────

// GetShareByNote —— 取该笔记最新一条分享（含已停用；部分唯一索引只约束
// 活跃行，历史上可能有多条停用行，取最新）。管理端 GET 用它展示
// active/disabled 状态。无行 → ErrNotFound。
func (s *Store) GetShareByNote(ctx context.Context, noteID, userID uuid.UUID) (*Share, error) {
	return scanShare(s.pool.QueryRow(ctx, `
		SELECT `+shareColumns+` FROM brain.note_shares
		WHERE note_id = $1 AND user_id = $2
		ORDER BY created_at DESC LIMIT 1
	`, noteID, userID))
}

// UpsertShareInput —— PUT /v1/notes/{id}/share 的落库语义：
//   - 无分享行 → 新建（Token 由调用方生成，credential_version=1）；
//   - 已有活跃行 → 更新配置（token 不变）；
//   - 已有停用行 → 以原 token 恢复（disabled_at 置 NULL）并更新配置。
type UpsertShareInput struct {
	NoteID uuid.UUID
	UserID uuid.UUID
	// Token —— 仅新建时使用（crypto/rand 24B → 32 字符 base64url）。
	Token string
	// ExpiresSet —— true 表示本次要改有效期：ExpiresAt 非 nil = 截止
	// 时间，nil = 永久（never）。false = 保持现有 expires_at 不变
	// （新建时没有"现有值"，落 NULL 即 never，与 PasswordSet 同型三态）。
	ExpiresSet bool
	ExpiresAt  *time.Time
	// PasswordSet —— true 表示本次要改密码：PasswordHash 非 nil = 重设
	// （credential_version+1），nil = 移除密码。false = 密码保持不变。
	PasswordSet  bool
	PasswordHash *string
	// MaxViewsSet —— true 表示本次要改访问上限（S2）：MaxViews 非 nil =
	// 设置/调整上限，nil = 移除上限（body max_views=0）。false = 保持
	// 不变（与 ExpiresSet / PasswordSet 同套三态）。
	MaxViewsSet bool
	MaxViews    *int
	ActorID     string
}

func (s *Store) UpsertShare(ctx context.Context, in UpsertShareInput) (*Share, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	cur, err := scanShare(tx.QueryRow(ctx, `
		SELECT `+shareColumns+` FROM brain.note_shares
		WHERE note_id = $1 AND user_id = $2
		ORDER BY created_at DESC LIMIT 1
		FOR UPDATE
	`, in.NoteID, in.UserID))
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, err
	}

	if errors.Is(err, ErrNotFound) {
		// 新建（expires_in / max_views 缺省 → 零值 nil = never / 不限）
		var hash *string
		if in.PasswordSet {
			hash = in.PasswordHash
		}
		var maxViews *int
		if in.MaxViewsSet {
			maxViews = in.MaxViews
		}
		sh, cerr := scanShare(tx.QueryRow(ctx, `
			INSERT INTO brain.note_shares (note_id, user_id, token, password_hash, expires_at, max_views)
			VALUES ($1, $2, $3, $4, $5, $6)
			RETURNING `+shareColumns,
			in.NoteID, in.UserID, in.Token, hash, in.ExpiresAt, maxViews))
		if cerr != nil {
			return nil, fmt.Errorf("insert note share: %w", cerr)
		}
		if err := emitShareEventTx(ctx, tx, "user", in.ActorID, ShareEventCreated, map[string]any{
			"share_id": sh.ID, "note_id": sh.NoteID, "user_id": sh.UserID,
			"password_set": sh.PasswordHash != nil, "expires_at": sh.ExpiresAt,
		}); err != nil {
			return nil, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return sh, nil
	}

	// 更新（含停用恢复：disabled_at 置 NULL，原 token 保留）
	passwordChanged := in.PasswordSet
	bumpCred := in.PasswordSet && in.PasswordHash != nil // 重设密码 → 已签发 JWT 全失效
	newHash := cur.PasswordHash
	newCred := cur.CredentialVersion
	if passwordChanged {
		newHash = in.PasswordHash
	}
	if bumpCred {
		newCred++
	}
	// expires_in 缺省 = 保持现有 expires_at（含停用恢复：原值已过期则
	// 恢复后仍是 expired，由客户端显式传值续期，服务端不特殊处理）。
	newExpiresAt := cur.ExpiresAt
	if in.ExpiresSet {
		newExpiresAt = in.ExpiresAt
	}
	// max_views 缺省 = 保持现有上限（同套三态）。
	newMaxViews := cur.MaxViews
	if in.MaxViewsSet {
		newMaxViews = in.MaxViews
	}
	restored := cur.DisabledAt != nil
	sh, err := scanShare(tx.QueryRow(ctx, `
		UPDATE brain.note_shares
		SET password_hash = $3, expires_at = $4, credential_version = $5,
		    max_views = $6, disabled_at = NULL, updated_at = now()
		WHERE id = $1 AND user_id = $2
		RETURNING `+shareColumns,
		cur.ID, in.UserID, newHash, newExpiresAt, newCred, newMaxViews))
	if err != nil {
		return nil, fmt.Errorf("update note share: %w", err)
	}
	if err := emitShareEventTx(ctx, tx, "user", in.ActorID, ShareEventUpdated, map[string]any{
		"share_id": sh.ID, "note_id": sh.NoteID, "user_id": sh.UserID,
		"restored": restored, "password_changed": passwordChanged,
		"password_set": sh.PasswordHash != nil, "expires_at": sh.ExpiresAt,
	}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return sh, nil
}

// DisableShare —— 软停用（链接立即 404，可经 PUT 恢复）。无活跃分享行
// → ErrNotFound（含已停用重复 DELETE 的情形）。
func (s *Store) DisableShare(ctx context.Context, noteID, userID uuid.UUID, actorID string) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	sh, err := scanShare(tx.QueryRow(ctx, `
		UPDATE brain.note_shares SET disabled_at = now(), updated_at = now()
		WHERE note_id = $1 AND user_id = $2 AND disabled_at IS NULL
		RETURNING `+shareColumns, noteID, userID))
	if errors.Is(err, ErrNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("disable note share: %w", err)
	}
	if err := emitShareEventTx(ctx, tx, "user", actorID, ShareEventDisabled, map[string]any{
		"share_id": sh.ID, "note_id": sh.NoteID, "user_id": sh.UserID,
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// RotateShare —— 重置链接：原行替换新 token + credential_version+1
// （旧链接即 404，已签发访问 JWT 全失效）。停用中的分享同样可 rotate
// （disabled_at 保持，恢复时即新 token）。无分享行 → ErrNotFound。
func (s *Store) RotateShare(ctx context.Context, noteID, userID uuid.UUID, newToken, actorID string) (*Share, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	sh, err := scanShare(tx.QueryRow(ctx, `
		UPDATE brain.note_shares
		SET token = $3, credential_version = credential_version + 1, updated_at = now()
		WHERE id = (
			SELECT id FROM brain.note_shares
			WHERE note_id = $1 AND user_id = $2
			ORDER BY created_at DESC LIMIT 1
		)
		RETURNING `+shareColumns, noteID, userID, newToken))
	if errors.Is(err, ErrNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("rotate note share: %w", err)
	}
	if err := emitShareEventTx(ctx, tx, "user", actorID, ShareEventRotated, map[string]any{
		"share_id": sh.ID, "note_id": sh.NoteID, "user_id": sh.UserID,
		"credential_version": sh.CredentialVersion,
	}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return sh, nil
}

// ListShares —— 「我的分享」管理列表：全状态（active/disabled/expired 由
// 调用方按 disabled_at/expires_at 推导），join note_notes 取标题。
// note 硬删会 ON DELETE CASCADE 连带删分享行，所以 LEFT JOIN 必中。
func (s *Store) ListShares(ctx context.Context, userID uuid.UUID) ([]*ShareWithTitle, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT s.id, s.note_id, s.user_id, s.token, s.password_hash, s.expires_at,
		       s.credential_version, s.view_count, s.max_views, s.disabled_at, s.created_at, s.updated_at,
		       n.title
		FROM brain.note_shares s
		LEFT JOIN brain.note_notes n ON n.id = s.note_id
		WHERE s.user_id = $1
		ORDER BY s.created_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*ShareWithTitle, 0, 16)
	for rows.Next() {
		sw := &ShareWithTitle{}
		if err := rows.Scan(
			&sw.ID, &sw.NoteID, &sw.UserID, &sw.Token, &sw.PasswordHash, &sw.ExpiresAt,
			&sw.CredentialVersion, &sw.ViewCount, &sw.MaxViews, &sw.DisabledAt, &sw.CreatedAt, &sw.UpdatedAt,
			&sw.NoteTitle,
		); err != nil {
			return nil, err
		}
		out = append(out, sw)
	}
	return out, rows.Err()
}

// ─── 公开端 ─────────────────────────────────────────────

// GetShareByToken —— 公开校验链第①步的原始读取；disabled/expired 判定
// 交给调用方（需要区分 404 与 410）。
func (s *Store) GetShareByToken(ctx context.Context, token string) (*Share, error) {
	return scanShare(s.pool.QueryRow(ctx, `
		SELECT `+shareColumns+` FROM brain.note_shares WHERE token = $1
	`, token))
}

// GetPublicNote —— 无 user 过滤按 id 读笔记（分享链专用；DeletedAt 非空
// 即已进回收站 → 410 note_deleted）。
func (s *Store) GetPublicNote(ctx context.Context, noteID uuid.UUID) (*PublicNote, error) {
	n := &PublicNote{}
	err := s.pool.QueryRow(ctx, `
		SELECT id, title, content_md, author, source_url, updated_at, deleted_at
		FROM brain.note_notes WHERE id = $1
	`, noteID).Scan(&n.ID, &n.Title, &n.ContentMD, &n.Author, &n.SourceURL, &n.UpdatedAt, &n.DeletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return n, nil
}

// AttachmentBelongs —— 校验链第③步：file_id 确实挂在该笔记上
// （Joplin isInTree 式，防随机 ID 盗链）。
func (s *Store) AttachmentBelongs(ctx context.Context, noteID, fileID uuid.UUID) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM brain.note_attachments WHERE note_id = $1 AND file_id = $2
		)
	`, noteID, fileID).Scan(&ok)
	return ok, err
}

// GetSharedFileObjectKey —— 取 ready 且未删的 files.objects 对象键
// （presign 用）；对象不存在/pending/已删 → ErrNotFound。
func (s *Store) GetSharedFileObjectKey(ctx context.Context, fileID uuid.UUID) (string, error) {
	var key string
	err := s.pool.QueryRow(ctx, `
		SELECT object_key FROM files.objects
		WHERE id = $1 AND status = 'ready' AND deleted_at IS NULL
	`, fileID).Scan(&key)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return key, nil
}

// RecordShareView —— 公开 GET 成功路径的访问计数（S2 会话级去重，
// §7.6 增量契约）：
//   - sessionHash 非 nil（调用方上送了 X-Share-Session 的 sha256）→
//     先 INSERT note_share_view_sessions ON CONFLICT DO NOTHING，仅当真
//     插入（RowsAffected=1）才 view_count+1，counted 返回是否真计了一次；
//     并发首次插入由主键约束兜底，至多一方计数，无需事务。
//   - sessionHash 为 nil（curl / 爬虫 / 直开 API）→ 每次照计。
func (s *Store) RecordShareView(ctx context.Context, shareID uuid.UUID, sessionHash *string) (counted bool, err error) {
	if sessionHash != nil {
		tag, err := s.pool.Exec(ctx, `
			INSERT INTO brain.note_share_view_sessions (share_id, session_hash)
			VALUES ($1, $2)
			ON CONFLICT DO NOTHING
		`, shareID, *sessionHash)
		if err != nil {
			return false, err
		}
		if tag.RowsAffected() == 0 {
			return false, nil // 同会话重复访问，不计数
		}
	}
	if _, err := s.pool.Exec(ctx, `
		UPDATE brain.note_shares SET view_count = view_count + 1 WHERE id = $1
	`, shareID); err != nil {
		return false, err
	}
	return true, nil
}

// ShareViewSessionsDefaultKeepDays —— 会话去重记录保留期（S2 契约：
// 30 天 TTL）。超期记录删掉后同会话再次访问会重新计数——可接受，
// 30 天前的会话基本已死。
const ShareViewSessionsDefaultKeepDays = 30

// PruneShareViewSessions —— 清理超过 keepDays 天的会话去重记录
// （照 PruneRevisions 的周期 job 模式：main 里 boot scan + 每日 tick）。
func (s *Store) PruneShareViewSessions(ctx context.Context, keepDays int) (int64, error) {
	if keepDays <= 0 {
		keepDays = ShareViewSessionsDefaultKeepDays
	}
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM brain.note_share_view_sessions
		WHERE created_at < now() - make_interval(days => $1)
	`, keepDays)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// RewriteShareFileURIs —— 公开内容出口改写：正文里的 `biu-file://<uuid>`
// 统一替换为 `/v1/shares/n/{token}/files/<uuid>`（类型命名空间 n=note；
// 相对 origin 的绝对路径，Astro 落地页同源访问）。正则只匹配附件 URI
// 的精确形态（biu-file:// + 规范 uuid），正文普通文本不会被误伤。
func RewriteShareFileURIs(content, token string) string {
	return biuFileURIRe.ReplaceAllString(content, "/v1/shares/n/"+token+"/files/${1}")
}
