// Package store is the data access layer for Brain.Notes.
//
// 与 wiki 同一形态：每次变更
//
//  1. 写业务表（更新接口带 version 乐观锁）
//  2. 同事务追加一行 brain.events（触发 pg_notify）
//
// 事件 scope 固定为 `note:user:<uid>`（个人空间，见设计文档 §4 D4），
// 删除也是一条事件（持久 tombstone），客户端靠
// GET /v1/notes/changes?since=N 增量追赶。
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// isUniqueViolation —— SQLSTATE 23505（唯一索引冲突）。note_notebooks 的
// 唯一索引只有「同父同名」一条，命中即 ErrDuplicateName。
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("version conflict")
	// ErrInvalidParent —— 指定父本不存在 / 跨用户 / 已软删。
	ErrInvalidParent = errors.New("invalid parent notebook")
	// ErrNotebookCycle —— reparent 目标是本笔记本自身或其后代。
	ErrNotebookCycle = errors.New("notebook parent would create a cycle")
	// ErrNotebookDepth —— 创建/移动后层级超过 maxNotebookDepth。
	ErrNotebookDepth = errors.New("notebook hierarchy too deep")
	// ErrDuplicateName —— 同一父目录下已存在同名（不区分大小写）的活本。
	ErrDuplicateName = errors.New("notebook name already exists in parent")
)

// maxNotebookDepth —— 笔记本目录树最大层数（根=1）。DB 不加约束，
// 与事件/软删一致，规则集中在写路径校验（迁移 00003 头注释 §3）。
const maxNotebookDepth = 5

type Notebook struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	Name      string
	ParentID  *uuid.UUID
	Position  float64
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Note struct {
	ID              uuid.UUID
	UserID          uuid.UUID
	NotebookID      *uuid.UUID
	Title           string
	ContentMD       string
	IsTodo          bool
	TodoCompletedAt *time.Time
	Position        float64
	Version         int
	DeletedAt       *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
	// SourceURL / Author —— webclip 剪藏来源（迁移 00060），均可空。
	SourceURL *string
	Author    *string
	// ArchivedAt —— 归档时间戳；区别于 DeletedAt（回收站）。归档笔记
	// 默认从列表/搜索隐藏，archived=only 只看归档。
	ArchivedAt *time.Time
	// PromotedPageID —— 「转入知识库」生成的 wiki page id；非空即已
	// promote，重复 promote 幂等回既有 page，不再新建。
	PromotedPageID *uuid.UUID
}

type Tag struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	ScopeKey  string
	Name      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Revision —— 笔记版本历史快照（迁移 00059）。edit = 保存前旧状态快照
// （有 5 分钟窗口合并与定期清理）；restore = 恢复前自动备份（永久保留）。
type Revision struct {
	ID            uuid.UUID
	NoteID        uuid.UUID
	UserID        uuid.UUID
	Title         string
	ContentMD     string
	ChangeType    string
	ChangeSummary *string
	CreatedAt     time.Time
}

type Event struct {
	ID        int64
	Scope     string
	ActorType string
	ActorID   string
	EventType string
	Payload   map[string]any
	SchemaVer int
	CreatedAt time.Time
}

type Store struct {
	pool *pgxpool.Pool
}

func New(p *pgxpool.Pool) *Store { return &Store{pool: p} }

// scopeFor —— 笔记域全部事件都打在用户个人 scope 上（设计 §4 D4）。
func scopeFor(userID uuid.UUID) string {
	return fmt.Sprintf("note:user:%s", userID)
}

func tagScopeKey(userID uuid.UUID) string {
	return fmt.Sprintf("personal:%s", userID)
}

// notePayload —— created/updated/restored 事件带上整行快照，
// 客户端拿到事件即可落本地缓存，不必回源再拉一次。
func notePayload(n *Note) map[string]any {
	pl := map[string]any{
		"note_id":    n.ID,
		"title":      n.Title,
		"content_md": n.ContentMD,
		"is_todo":    n.IsTodo,
		"position":   n.Position,
		"version":    n.Version,
		"updated_at": n.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if n.NotebookID != nil {
		pl["notebook_id"] = *n.NotebookID
	} else {
		pl["notebook_id"] = nil
	}
	if n.TodoCompletedAt != nil {
		pl["todo_completed_at"] = n.TodoCompletedAt.UTC().Format(time.RFC3339)
	}
	if n.SourceURL != nil {
		pl["source_url"] = *n.SourceURL
	}
	if n.Author != nil {
		pl["author"] = *n.Author
	}
	if n.ArchivedAt != nil {
		pl["archived_at"] = n.ArchivedAt.UTC().Format(time.RFC3339)
	}
	if n.PromotedPageID != nil {
		pl["promoted_page_id"] = *n.PromotedPageID
	}
	return pl
}

// ─── Notebooks ──────────────────────────────────────────

// CreateNotebook —— parentID 为 nil 时创建根级笔记本；非 nil 时校验父本
// 存在/同用户/未软删，且父本已在第 maxNotebookDepth 层时拒绝（ErrNotebookDepth）。
func (s *Store) CreateNotebook(ctx context.Context, userID uuid.UUID, name string, position float64, parentID *uuid.UUID, actorID string) (*Notebook, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	if parentID != nil {
		depth, derr := notebookDepthTx(ctx, tx, userID, *parentID)
		if derr != nil {
			return nil, derr
		}
		if depth == 0 {
			return nil, ErrInvalidParent
		}
		if depth+1 > maxNotebookDepth {
			return nil, ErrNotebookDepth
		}
	}

	nb := &Notebook{}
	err = tx.QueryRow(ctx, `
		INSERT INTO brain.note_notebooks (user_id, name, parent_id, position)
		VALUES ($1, $2, $3, $4)
		RETURNING id, user_id, name, parent_id, position, created_at, updated_at
	`, userID, name, parentID, position).Scan(
		&nb.ID, &nb.UserID, &nb.Name, &nb.ParentID, &nb.Position, &nb.CreatedAt, &nb.UpdatedAt,
	)
	if isUniqueViolation(err) {
		return nil, ErrDuplicateName
	}
	if err != nil {
		return nil, fmt.Errorf("insert notebook: %w", err)
	}
	if err := emitEvent(ctx, tx, userID, "user", actorID, "notebook.created", map[string]any{
		"notebook_id": nb.ID, "name": nb.Name, "parent_id": nb.ParentID,
	}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return nb, nil
}

// notebookDepthTx —— 沿 parent 链向上数该活本的层数（根=1）；本不存在 /
// 跨用户 / 已软删返回 0。软删路径会把子本上移（SoftDeleteNotebook），
// 所以活本的祖先必然全活，不需要在递归里兜 deleted 链。
func notebookDepthTx(ctx context.Context, tx pgx.Tx, userID, id uuid.UUID) (int, error) {
	var depth int
	err := tx.QueryRow(ctx, `
		WITH RECURSIVE up AS (
			SELECT id, parent_id, 1 AS depth
			FROM brain.note_notebooks
			WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL
			UNION ALL
			SELECT p.id, p.parent_id, up.depth + 1
			FROM brain.note_notebooks p
			JOIN up ON p.id = up.parent_id
			WHERE p.deleted_at IS NULL
		)
		SELECT COALESCE(MAX(depth), 0) FROM up
	`, id, userID).Scan(&depth)
	return depth, err
}

// notebookSubtreeTx —— 以 id 为根的活本子树：height = 子树高度（自身=1），
// contains = target 是否在子树内（含自身）。reparent 防环与深度校验共用。
func notebookSubtreeTx(ctx context.Context, tx pgx.Tx, userID, id, target uuid.UUID) (height int, contains bool, err error) {
	err = tx.QueryRow(ctx, `
		WITH RECURSIVE down AS (
			SELECT id, 1 AS height
			FROM brain.note_notebooks
			WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL
			UNION ALL
			SELECT c.id, down.height + 1
			FROM brain.note_notebooks c
			JOIN down ON c.parent_id = down.id
			WHERE c.deleted_at IS NULL
		)
		SELECT COALESCE(MAX(height), 0), COALESCE(BOOL_OR(id = $3), false) FROM down
	`, id, userID, target).Scan(&height, &contains)
	return height, contains, err
}

func (s *Store) ListNotebooks(ctx context.Context, userID uuid.UUID) ([]*Notebook, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, user_id, name, parent_id, position, created_at, updated_at
		FROM brain.note_notebooks
		WHERE user_id = $1 AND deleted_at IS NULL
		ORDER BY position, created_at
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Notebook
	for rows.Next() {
		nb := &Notebook{}
		if err := rows.Scan(&nb.ID, &nb.UserID, &nb.Name, &nb.ParentID, &nb.Position, &nb.CreatedAt, &nb.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, nb)
	}
	return out, rows.Err()
}

// GetNotebook —— 不按 deleted_at 过滤，调用方（restore 等）需要自己判断。
func (s *Store) GetNotebook(ctx context.Context, id, userID uuid.UUID) (*Notebook, bool, error) {
	nb := &Notebook{}
	var deletedAt *time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT id, user_id, name, parent_id, position, deleted_at, created_at, updated_at
		FROM brain.note_notebooks WHERE id = $1 AND user_id = $2
	`, id, userID).Scan(&nb.ID, &nb.UserID, &nb.Name, &nb.ParentID, &nb.Position, &deletedAt, &nb.CreatedAt, &nb.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, ErrNotFound
	}
	if err != nil {
		return nil, false, err
	}
	return nb, deletedAt == nil, nil
}

type UpdateNotebookInput struct {
	ID       uuid.UUID
	UserID   uuid.UUID
	Name     *string
	Position *float64
	// ParentID —— nil = 不动；配合 MoveToRoot 升到根（同 UpdateNoteInput
	// 的 NotebookID/MoveToRoot 写法）。
	ParentID   *uuid.UUID
	MoveToRoot bool
	ActorID    string
}

func (s *Store) UpdateNotebook(ctx context.Context, in UpdateNotebookInput) (*Notebook, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	cur := &Notebook{}
	err = tx.QueryRow(ctx, `
		SELECT id, user_id, name, parent_id, position, created_at, updated_at
		FROM brain.note_notebooks WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL
	`, in.ID, in.UserID).Scan(&cur.ID, &cur.UserID, &cur.Name, &cur.ParentID, &cur.Position, &cur.CreatedAt, &cur.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	name := cur.Name
	if in.Name != nil {
		name = *in.Name
	}
	pos := cur.Position
	if in.Position != nil {
		pos = *in.Position
	}
	parentID := cur.ParentID
	if in.MoveToRoot {
		parentID = nil
	} else if in.ParentID != nil {
		parentID = in.ParentID
	}
	// reparent 校验：目标父本存在/同用户/未软删；不能落到自身或后代
	// 之下（成环）；移动后整个子树深度不超 maxNotebookDepth。
	reparenting := in.MoveToRoot || in.ParentID != nil
	if reparenting && parentID != nil {
		if *parentID == in.ID {
			return nil, ErrNotebookCycle
		}
		pDepth, derr := notebookDepthTx(ctx, tx, in.UserID, *parentID)
		if derr != nil {
			return nil, derr
		}
		if pDepth == 0 {
			return nil, ErrInvalidParent
		}
		height, contains, serr := notebookSubtreeTx(ctx, tx, in.UserID, in.ID, *parentID)
		if serr != nil {
			return nil, serr
		}
		if contains {
			return nil, ErrNotebookCycle
		}
		if pDepth+height > maxNotebookDepth {
			return nil, ErrNotebookDepth
		}
	}
	nb := &Notebook{}
	err = tx.QueryRow(ctx, `
		UPDATE brain.note_notebooks SET name = $3, parent_id = $4, position = $5, updated_at = now()
		WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL
		RETURNING id, user_id, name, parent_id, position, created_at, updated_at
	`, in.ID, in.UserID, name, parentID, pos).Scan(
		&nb.ID, &nb.UserID, &nb.Name, &nb.ParentID, &nb.Position, &nb.CreatedAt, &nb.UpdatedAt,
	)
	if isUniqueViolation(err) {
		return nil, ErrDuplicateName
	}
	if err != nil {
		return nil, fmt.Errorf("update notebook: %w", err)
	}
	if err := emitEvent(ctx, tx, in.UserID, "user", in.ActorID, "notebook.updated", map[string]any{
		"notebook_id": nb.ID, "name": nb.Name, "position": nb.Position, "parent_id": nb.ParentID,
	}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return nb, nil
}

// SoftDeleteNotebook —— 软删笔记本；挂在它下面的笔记不动
// （还原时父已删则置根，见 RestoreNote）。子笔记本的 parent_id 同事务
// 上移一层（指向被删本自己的父本，根级的子本变根），保证活本树里
// 不会挂着已删的祖先。
func (s *Store) SoftDeleteNotebook(ctx context.Context, id, userID uuid.UUID, actorID string) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var parentID *uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT parent_id FROM brain.note_notebooks
		WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL
	`, id, userID).Scan(&parentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE brain.note_notebooks SET parent_id = $3, updated_at = now()
		WHERE parent_id = $1 AND user_id = $2 AND deleted_at IS NULL
	`, id, userID, parentID); err != nil {
		return fmt.Errorf("promote child notebooks: %w", err)
	}
	tag, err := tx.Exec(ctx, `
		UPDATE brain.note_notebooks SET deleted_at = now(), updated_at = now()
		WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL
	`, id, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if err := emitEvent(ctx, tx, userID, "user", actorID, "notebook.deleted", map[string]any{
		"notebook_id": id,
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ─── Notes ──────────────────────────────────────────────

const noteColumns = `id, user_id, notebook_id, title, content_md, is_todo,
	todo_completed_at, position, version, deleted_at, created_at, updated_at,
	source_url, author, archived_at, promoted_page_id`

func scanNote(row pgx.Row) (*Note, error) {
	n := &Note{}
	err := row.Scan(
		&n.ID, &n.UserID, &n.NotebookID, &n.Title, &n.ContentMD, &n.IsTodo,
		&n.TodoCompletedAt, &n.Position, &n.Version, &n.DeletedAt, &n.CreatedAt, &n.UpdatedAt,
		&n.SourceURL, &n.Author, &n.ArchivedAt, &n.PromotedPageID,
	)
	if err != nil {
		return nil, err
	}
	return n, nil
}

type CreateNoteInput struct {
	// ID —— 客户端生成的 uuid；主键冲突时幂等返回已存在记录（离线重放安全）。
	// 为空则服务端生成。
	ID              *uuid.UUID
	UserID          uuid.UUID
	NotebookID      *uuid.UUID
	Title           string
	ContentMD       string
	IsTodo          bool
	TodoCompletedAt *time.Time
	Position        float64
	// SourceURL / Author —— webclip 剪藏来源（可选）。
	SourceURL *string
	Author    *string
	ActorID   string
}

// CreateNote 返回 (note, replayed, err)。replayed=true 表示同 id 记录
// 已存在（离线重放），未做任何写入。
func (s *Store) CreateNote(ctx context.Context, in CreateNoteInput) (*Note, bool, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback(ctx)

	id := uuid.New()
	if in.ID != nil {
		id = *in.ID
	}
	n, err := scanNote(tx.QueryRow(ctx, `
		INSERT INTO brain.note_notes (id, user_id, notebook_id, title, content_md, is_todo, todo_completed_at, position, source_url, author)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (id) DO NOTHING
		RETURNING `+noteColumns,
		id, in.UserID, in.NotebookID, in.Title, in.ContentMD, in.IsTodo, in.TodoCompletedAt, in.Position, in.SourceURL, in.Author,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		// 主键冲突 —— 幂等重放：返回已存在记录（仅限同用户；
		// 别人的 uuid 撞上对调用方表现为 not found）。
		existing, gerr := s.getNoteTx(ctx, tx, id, in.UserID)
		if gerr != nil {
			return nil, false, gerr
		}
		if cerr := tx.Commit(ctx); cerr != nil {
			return nil, false, cerr
		}
		return existing, true, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("insert note: %w", err)
	}
	// 附件对账（设计 §5 M4）：新笔记无历史行，只在确有引用时 upsert。
	if len(extractFileIDs(in.ContentMD)) > 0 {
		if err := s.reconcileAttachmentsTx(ctx, tx, n.ID, in.UserID, in.ContentMD); err != nil {
			return nil, false, err
		}
	}
	if err := emitEvent(ctx, tx, in.UserID, "user", in.ActorID, "note.created", notePayload(n)); err != nil {
		return nil, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, false, err
	}
	return n, false, nil
}

func (s *Store) getNoteTx(ctx context.Context, tx pgx.Tx, id, userID uuid.UUID) (*Note, error) {
	n, err := scanNote(tx.QueryRow(ctx, `
		SELECT `+noteColumns+` FROM brain.note_notes WHERE id = $1 AND user_id = $2
	`, id, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return n, nil
}

// GetNote —— 只看活笔记（deleted_at IS NULL）。
func (s *Store) GetNote(ctx context.Context, id, userID uuid.UUID) (*Note, error) {
	n, err := scanNote(s.pool.QueryRow(ctx, `
		SELECT `+noteColumns+` FROM brain.note_notes
		WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL
	`, id, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return n, nil
}

type ListNotesFilter struct {
	UserID     uuid.UUID
	NotebookID *uuid.UUID // nil = 不过滤
	RootOnly   bool       // true = 只看根（notebook_id IS NULL）
	Tag        string     // 标签名（用户 scope 内，大小写不敏感）；空 = 不过滤
	TodoOnly   bool
	// Archived —— "" 默认排除已归档；"only" 只看已归档（回收站语义不变，
	// deleted_at 永远排除）。
	Archived string
	Limit    int
	Offset   int
}

func (s *Store) ListNotes(ctx context.Context, f ListNotesFilter) ([]*Note, error) {
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	q := `
		SELECT n.` + noteColumns + `
		FROM brain.note_notes n
	`
	args := []any{f.UserID}
	cond := ` WHERE n.user_id = $1 AND n.deleted_at IS NULL`
	if f.Archived == "only" {
		cond += ` AND n.archived_at IS NOT NULL`
	} else {
		cond += ` AND n.archived_at IS NULL`
	}
	if f.RootOnly {
		cond += ` AND n.notebook_id IS NULL`
	} else if f.NotebookID != nil {
		args = append(args, *f.NotebookID)
		cond += fmt.Sprintf(` AND n.notebook_id = $%d`, len(args))
	}
	if f.TodoOnly {
		cond += ` AND n.is_todo`
	}
	if f.Tag != "" {
		args = append(args, f.Tag)
		q += ` JOIN brain.note_note_tags nt ON nt.note_id = n.id
		       JOIN brain.note_tags t ON t.id = nt.tag_id
		`
		cond += fmt.Sprintf(` AND t.user_id = $1 AND t.deleted_at IS NULL AND lower(t.name) = lower($%d)`, len(args))
	}
	args = append(args, f.Limit, f.Offset)
	q += cond + fmt.Sprintf(` ORDER BY n.position, n.created_at DESC LIMIT $%d OFFSET $%d`, len(args)-1, len(args))

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*Note, 0, 32)
	for rows.Next() {
		n := &Note{}
		if err := rows.Scan(
			&n.ID, &n.UserID, &n.NotebookID, &n.Title, &n.ContentMD, &n.IsTodo,
			&n.TodoCompletedAt, &n.Position, &n.Version, &n.DeletedAt, &n.CreatedAt, &n.UpdatedAt,
			&n.SourceURL, &n.Author, &n.ArchivedAt, &n.PromotedPageID,
		); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (s *Store) ListTrash(ctx context.Context, userID uuid.UUID, limit int) ([]*Note, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+noteColumns+` FROM brain.note_notes
		WHERE user_id = $1 AND deleted_at IS NOT NULL
		ORDER BY deleted_at DESC LIMIT $2
	`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*Note, 0, 32)
	for rows.Next() {
		n := &Note{}
		if err := rows.Scan(
			&n.ID, &n.UserID, &n.NotebookID, &n.Title, &n.ContentMD, &n.IsTodo,
			&n.TodoCompletedAt, &n.Position, &n.Version, &n.DeletedAt, &n.CreatedAt, &n.UpdatedAt,
			&n.SourceURL, &n.Author, &n.ArchivedAt, &n.PromotedPageID,
		); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// ─── Attachments ────────────────────────────────────────

// biuFileURIRe —— 正文里的附件引用 `biu-file://<uuid>`（设计 §4 D5）。
var biuFileURIRe = regexp.MustCompile(`biu-file://([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})`)

// extractFileIDs —— 解析 content_md 里的全部 biu-file 引用，去重保序。
func extractFileIDs(content string) []uuid.UUID {
	seen := map[uuid.UUID]bool{}
	var out []uuid.UUID
	for _, m := range biuFileURIRe.FindAllStringSubmatch(content, -1) {
		id, err := uuid.Parse(m[1])
		if err != nil || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// reconcileAttachmentsTx —— 事务内对账 note_attachments：
//   - 正文引用到的 (note_id, file_id) upsert（is_associated=true,
//     last_seen_at=now()）——仅限本人 files.objects 里 ready 且未删的行，
//     防止引用别人的 file id；
//   - 该 note 不再引用的行删除，释放孤儿 GC 的引用计数。
func (s *Store) reconcileAttachmentsTx(ctx context.Context, tx pgx.Tx, noteID, userID uuid.UUID, content string) error {
	fileIDs := extractFileIDs(content)
	if fileIDs == nil {
		fileIDs = []uuid.UUID{}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO brain.note_attachments (note_id, file_id, is_associated, last_seen_at)
		SELECT $1, o.id, true, now()
		  FROM files.objects o
		 WHERE o.id = ANY($2::uuid[]) AND o.user_id = $3
		   AND o.status = 'ready' AND o.deleted_at IS NULL
		ON CONFLICT (note_id, file_id) DO UPDATE
		  SET is_associated = true, last_seen_at = now()
	`, noteID, fileIDs, userID); err != nil {
		return fmt.Errorf("upsert note attachments: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM brain.note_attachments
		 WHERE note_id = $1 AND NOT (file_id = ANY($2::uuid[]))
	`, noteID, fileIDs); err != nil {
		return fmt.Errorf("prune note attachments: %w", err)
	}
	return nil
}

// ─── Search ─────────────────────────────────────────────

// SearchHit —— 全文搜索单条命中。Snippet 已用 ts_headline 包好
// <mark>...</mark> 高亮；Rank 是 ts_rank 原始分（normalize=1）。
type SearchHit struct {
	ID              uuid.UUID
	NotebookID      *uuid.UUID
	Title           string
	IsTodo          bool
	TodoCompletedAt *time.Time
	UpdatedAt       time.Time
	Snippet         string
	Rank            float64
}

const (
	SearchDefaultLimit = 20
	SearchMaxLimit     = 50
	// searchSnippetCap —— ts_headline 在长内容上慢，截断到 ~4KB 控制
	// cost（同 chat.SearchMessages 的惯例）。
	searchSnippetCap = 4000
)

// SearchNotes —— tsv (title 权重 A / content_md 权重 B) + GIN 全文搜索，
// 走 websearch_to_tsquery（同 search/bm25 的惯例）；严格 user_id 隔离，
// 排除回收站与已归档；排序 rank DESC, updated_at DESC。
func (s *Store) SearchNotes(ctx context.Context, userID uuid.UUID, query string, limit int) ([]SearchHit, error) {
	if limit <= 0 {
		limit = SearchDefaultLimit
	}
	if limit > SearchMaxLimit {
		limit = SearchMaxLimit
	}
	rows, err := s.pool.Query(ctx, `
WITH q AS (
  SELECT websearch_to_tsquery('biumind_zhcn', $1) AS tsq
)
SELECT n.id, n.notebook_id, n.title, n.is_todo, n.todo_completed_at, n.updated_at,
       ts_rank(n.tsv, q.tsq, 1) AS rank,
       ts_headline('biumind_zhcn', left(n.content_md, $4), q.tsq,
                   'MaxWords=30, MinWords=10, StartSel=<mark>, StopSel=</mark>') AS snippet
  FROM brain.note_notes n
  CROSS JOIN q
 WHERE n.user_id = $2
   AND n.deleted_at IS NULL
   AND n.archived_at IS NULL
   AND n.tsv @@ q.tsq
 ORDER BY rank DESC, n.updated_at DESC
 LIMIT $3
`, query, userID, limit, searchSnippetCap)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]SearchHit, 0, limit)
	for rows.Next() {
		var h SearchHit
		if err := rows.Scan(&h.ID, &h.NotebookID, &h.Title, &h.IsTodo,
			&h.TodoCompletedAt, &h.UpdatedAt, &h.Rank, &h.Snippet); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// UpdateNote —— If-Match 乐观锁；版本不匹配返回 ErrConflict，
// 调用方再 GetNote 拿当前内容一起回 409。
type UpdateNoteInput struct {
	ID                 uuid.UUID
	UserID             uuid.UUID
	IfMatchVersion     int
	Title              *string
	ContentMD          *string
	NotebookID         *uuid.UUID // nil = 不动；配合 MoveToRoot 移回根
	MoveToRoot         bool
	IsTodo             *bool
	TodoCompletedAt    *time.Time // nil = 不动；配合 ClearTodoCompleted 清除
	ClearTodoCompleted bool
	Position           *float64
	// SourceURL / Author —— nil = 不动（webclip 元数据）。
	SourceURL *string
	Author    *string
	ActorID   string
}

func (s *Store) UpdateNote(ctx context.Context, in UpdateNoteInput) (*Note, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	cur, err := s.getNoteTx(ctx, tx, in.ID, in.UserID)
	if err != nil {
		return nil, err
	}
	if cur.DeletedAt != nil {
		return nil, ErrNotFound
	}
	if in.IfMatchVersion != 0 && cur.Version != in.IfMatchVersion {
		return nil, ErrConflict
	}

	title := cur.Title
	if in.Title != nil {
		title = *in.Title
	}
	content := cur.ContentMD
	if in.ContentMD != nil {
		content = *in.ContentMD
	}
	notebookID := cur.NotebookID
	if in.MoveToRoot {
		notebookID = nil
	} else if in.NotebookID != nil {
		notebookID = in.NotebookID
	}
	isTodo := cur.IsTodo
	if in.IsTodo != nil {
		isTodo = *in.IsTodo
	}
	todoCompletedAt := cur.TodoCompletedAt
	if in.ClearTodoCompleted {
		todoCompletedAt = nil
	} else if in.TodoCompletedAt != nil {
		todoCompletedAt = in.TodoCompletedAt
	}
	pos := cur.Position
	if in.Position != nil {
		pos = *in.Position
	}
	sourceURL := cur.SourceURL
	if in.SourceURL != nil {
		sourceURL = in.SourceURL
	}
	author := cur.Author
	if in.Author != nil {
		author = in.Author
	}

	// 保存前快照：title/content_md 实质变化时先把旧状态存为 edit 版本。
	// 距上一条 edit 版本不足 revisionWindow 则跳过 —— 窗口合并，
	// 窗口内只保留窗口起点的旧快照（Nowen revisions 模式）。
	if title != cur.Title || content != cur.ContentMD {
		if err := snapshotRevisionTx(ctx, tx, cur); err != nil {
			return nil, err
		}
	}

	n, err := scanNote(tx.QueryRow(ctx, `
		UPDATE brain.note_notes
		SET title = $3, content_md = $4, notebook_id = $5, is_todo = $6,
		    todo_completed_at = $7, position = $8, source_url = $10, author = $11,
		    version = version + 1, updated_at = now()
		WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL AND version = $9
		RETURNING `+noteColumns,
		in.ID, in.UserID, title, content, notebookID, isTodo, todoCompletedAt, pos, cur.Version, sourceURL, author,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrConflict
	}
	if err != nil {
		return nil, fmt.Errorf("update note: %w", err)
	}
	// content_md 变更时事务内对账附件引用（含"清空引用"的 prune）。
	if in.ContentMD != nil {
		if err := s.reconcileAttachmentsTx(ctx, tx, n.ID, in.UserID, content); err != nil {
			return nil, err
		}
	}
	if err := emitEvent(ctx, tx, in.UserID, "user", in.ActorID, "note.updated", notePayload(n)); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return n, nil
}

func (s *Store) SoftDeleteNote(ctx context.Context, id, userID uuid.UUID, actorID string) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx, `
		UPDATE brain.note_notes SET deleted_at = now(), version = version + 1, updated_at = now()
		WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL
	`, id, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	// 删除也是一条事件 —— 客户端同步的持久 tombstone（设计 §4 D4）。
	if err := emitEvent(ctx, tx, userID, "user", actorID, "note.deleted", map[string]any{
		"note_id": id,
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// RestoreNote —— 从回收站还原；原笔记本已删（或不存在）则置根（设计 §4 D1）。
func (s *Store) RestoreNote(ctx context.Context, id, userID uuid.UUID, actorID string) (*Note, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	cur, err := s.getNoteTx(ctx, tx, id, userID)
	if err != nil {
		return nil, err
	}
	if cur.DeletedAt == nil {
		return nil, ErrNotFound // 不在回收站
	}
	notebookID := cur.NotebookID
	if notebookID != nil {
		_, alive, gerr := s.GetNotebook(ctx, *notebookID, userID)
		if gerr != nil || !alive {
			notebookID = nil
		}
	}
	n, err := scanNote(tx.QueryRow(ctx, `
		UPDATE brain.note_notes
		SET deleted_at = NULL, notebook_id = $3, version = version + 1, updated_at = now()
		WHERE id = $1 AND user_id = $2 AND deleted_at IS NOT NULL
		RETURNING `+noteColumns,
		id, userID, notebookID,
	))
	if err != nil {
		return nil, fmt.Errorf("restore note: %w", err)
	}
	if err := emitEvent(ctx, tx, userID, "user", actorID, "note.restored", notePayload(n)); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return n, nil
}

// ─── Archive / Promote ──────────────────────────────────

// ArchiveNote —— 归档（区别于回收站 deleted_at）。幂等：已归档则直接
// 返回当前状态，不发重复事件。
func (s *Store) ArchiveNote(ctx context.Context, id, userID uuid.UUID, actorID string) (*Note, error) {
	return s.setArchived(ctx, id, userID, true, actorID)
}

// UnarchiveNote —— 归档还原：archived_at 置 NULL，promoted_page_id 保留
// （页面已建，回链不丢）。幂等同 ArchiveNote。
func (s *Store) UnarchiveNote(ctx context.Context, id, userID uuid.UUID, actorID string) (*Note, error) {
	return s.setArchived(ctx, id, userID, false, actorID)
}

func (s *Store) setArchived(ctx context.Context, id, userID uuid.UUID, archived bool, actorID string) (*Note, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	cur, err := s.getNoteTx(ctx, tx, id, userID)
	if err != nil {
		return nil, err
	}
	if cur.DeletedAt != nil {
		return nil, ErrNotFound // 回收站内笔记不参与归档
	}
	if (cur.ArchivedAt != nil) == archived {
		// 已是目标状态 —— 幂等返回，不写版本不发事件。
		if cerr := tx.Commit(ctx); cerr != nil {
			return nil, cerr
		}
		return cur, nil
	}
	var n *Note
	if archived {
		n, err = scanNote(tx.QueryRow(ctx, `
			UPDATE brain.note_notes
			SET archived_at = now(), version = version + 1, updated_at = now()
			WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL AND archived_at IS NULL
			RETURNING `+noteColumns, id, userID))
	} else {
		n, err = scanNote(tx.QueryRow(ctx, `
			UPDATE brain.note_notes
			SET archived_at = NULL, version = version + 1, updated_at = now()
			WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL AND archived_at IS NOT NULL
			RETURNING `+noteColumns, id, userID))
	}
	if err != nil {
		return nil, fmt.Errorf("set archived: %w", err)
	}
	if err := emitEvent(ctx, tx, userID, "user", actorID, "note.updated", notePayload(n)); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return n, nil
}

// MarkPromoted —— 「转入知识库」成功后回写：promoted_page_id=pageID，
// 同时归档（archived_at 保留原值，未归档则置 now）。已 promote 的笔记
// 由调用方（api 层）短路，这里仍保持幂等：pageID 相同则直接返回。
func (s *Store) MarkPromoted(ctx context.Context, id, userID, pageID uuid.UUID, actorID string) (*Note, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	cur, err := s.getNoteTx(ctx, tx, id, userID)
	if err != nil {
		return nil, err
	}
	if cur.DeletedAt != nil {
		return nil, ErrNotFound
	}
	if cur.PromotedPageID != nil && *cur.PromotedPageID == pageID {
		if cerr := tx.Commit(ctx); cerr != nil {
			return nil, cerr
		}
		return cur, nil // 幂等重放
	}
	n, err := scanNote(tx.QueryRow(ctx, `
		UPDATE brain.note_notes
		SET promoted_page_id = $3, archived_at = COALESCE(archived_at, now()),
		    version = version + 1, updated_at = now()
		WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL
		RETURNING `+noteColumns, id, userID, pageID))
	if err != nil {
		return nil, fmt.Errorf("mark promoted: %w", err)
	}
	if err := emitEvent(ctx, tx, userID, "user", actorID, "note.updated", notePayload(n)); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return n, nil
}

// PurgeNote —— 回收站物理删除（只允许已软删的笔记）。
func (s *Store) PurgeNote(ctx context.Context, id, userID uuid.UUID, actorID string) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx, `
		DELETE FROM brain.note_notes
		WHERE id = $1 AND user_id = $2 AND deleted_at IS NOT NULL
	`, id, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if err := emitEvent(ctx, tx, userID, "user", actorID, "note.purged", map[string]any{
		"note_id": id,
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ─── Revisions ──────────────────────────────────────────

const (
	// revisionWindow —— edit 快照的窗口合并间隔：距该笔记上一条
	// change_type='edit' 的版本不足 5 分钟则不再快照。
	revisionWindow = 5 * time.Minute
	// PruneRevisions 的默认清理门槛（Nowen 惯例）。
	PruneDefaultKeepRecent = 50
	PruneDefaultKeepDays   = 30
	// RevisionRestoreSummary —— restore 自动备份版本的固定摘要。
	RevisionRestoreSummary = "恢复前自动备份"
	// RevisionCopySuffix —— save-as-copy 新笔记的标题后缀。
	RevisionCopySuffix = "（历史副本）"
)

// snapshotRevisionTx —— 把 cur（更新前的旧状态）存为 edit 版本；
// 距上一条 edit 版本不足 revisionWindow 则跳过（窗口合并）。
func snapshotRevisionTx(ctx context.Context, tx pgx.Tx, cur *Note) error {
	var lastEdit time.Time
	err := tx.QueryRow(ctx, `
		SELECT created_at FROM brain.note_revisions
		WHERE note_id = $1 AND change_type = 'edit'
		ORDER BY created_at DESC LIMIT 1
	`, cur.ID).Scan(&lastEdit)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if err == nil && time.Since(lastEdit) < revisionWindow {
		return nil
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO brain.note_revisions (note_id, user_id, title, content_md, change_type)
		VALUES ($1, $2, $3, $4, 'edit')
	`, cur.ID, cur.UserID, cur.Title, cur.ContentMD); err != nil {
		return fmt.Errorf("snapshot revision: %w", err)
	}
	return nil
}

const revisionColumns = `id, note_id, user_id, title, content_md, change_type, change_summary, created_at`

// ListRevisions —— 该笔记的版本列表（新→旧），不含 content_md。
// 回收站内笔记也可列出（便于查看历史），但严格 user_id 隔离。
func (s *Store) ListRevisions(ctx context.Context, noteID, userID uuid.UUID, limit, offset int) ([]*Revision, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	var exists bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM brain.note_notes WHERE id = $1 AND user_id = $2)
	`, noteID, userID).Scan(&exists)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrNotFound
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, note_id, user_id, title, change_type, change_summary, created_at
		FROM brain.note_revisions
		WHERE note_id = $1 AND user_id = $2
		ORDER BY created_at DESC, id DESC
		LIMIT $3 OFFSET $4
	`, noteID, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*Revision, 0, limit)
	for rows.Next() {
		r := &Revision{}
		if err := rows.Scan(&r.ID, &r.NoteID, &r.UserID, &r.Title,
			&r.ChangeType, &r.ChangeSummary, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetRevision —— 单条版本（含完整 title/content_md），严格 user 隔离。
func (s *Store) GetRevision(ctx context.Context, noteID, revisionID, userID uuid.UUID) (*Revision, error) {
	r := &Revision{}
	err := s.pool.QueryRow(ctx, `
		SELECT `+revisionColumns+` FROM brain.note_revisions
		WHERE id = $1 AND note_id = $2 AND user_id = $3
	`, revisionID, noteID, userID).Scan(
		&r.ID, &r.NoteID, &r.UserID, &r.Title, &r.ContentMD,
		&r.ChangeType, &r.ChangeSummary, &r.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return r, nil
}

// RestoreRevision —— 覆盖式恢复：事务内先把当前状态存为
// change_type='restore' 的自动备份版本，再把笔记覆盖为该版本内容。
// 覆盖走与 UpdateNote 相同的更新路径（version 乐观锁 +1、附件对账、
// note.updated 事件），回收站内笔记不可恢复。
func (s *Store) RestoreRevision(ctx context.Context, noteID, revisionID, userID uuid.UUID, actorID string) (*Note, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	cur, err := s.getNoteTx(ctx, tx, noteID, userID)
	if err != nil {
		return nil, err
	}
	if cur.DeletedAt != nil {
		return nil, ErrNotFound // 回收站内笔记不可恢复
	}
	rev := &Revision{}
	err = tx.QueryRow(ctx, `
		SELECT `+revisionColumns+` FROM brain.note_revisions
		WHERE id = $1 AND note_id = $2 AND user_id = $3
	`, revisionID, noteID, userID).Scan(
		&rev.ID, &rev.NoteID, &rev.UserID, &rev.Title, &rev.ContentMD,
		&rev.ChangeType, &rev.ChangeSummary, &rev.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	// 恢复前自动备份：当前状态存为 restore 版本（永久保留，不参与窗口合并）。
	if _, err := tx.Exec(ctx, `
		INSERT INTO brain.note_revisions (note_id, user_id, title, content_md, change_type, change_summary)
		VALUES ($1, $2, $3, $4, 'restore', $5)
	`, cur.ID, cur.UserID, cur.Title, cur.ContentMD, RevisionRestoreSummary); err != nil {
		return nil, fmt.Errorf("backup before restore: %w", err)
	}

	n, err := scanNote(tx.QueryRow(ctx, `
		UPDATE brain.note_notes
		SET title = $3, content_md = $4, version = version + 1, updated_at = now()
		WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL AND version = $5
		RETURNING `+noteColumns,
		noteID, userID, rev.Title, rev.ContentMD, cur.Version,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrConflict
	}
	if err != nil {
		return nil, fmt.Errorf("restore note revision: %w", err)
	}
	// 恢复后正文可能引用不同附件，同更新路径事务内对账。
	if err := s.reconcileAttachmentsTx(ctx, tx, n.ID, userID, n.ContentMD); err != nil {
		return nil, err
	}
	if err := emitEvent(ctx, tx, userID, "user", actorID, "note.updated", notePayload(n)); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return n, nil
}

// SaveRevisionAsCopy —— 以该版本内容新建笔记：同 notebook、复制标签关联、
// 标题追加 RevisionCopySuffix。新建走 CreateNote（note.created 事件），
// 标签走 SetNoteTags（note.tags_updated 事件），不新发明事件类型。
func (s *Store) SaveRevisionAsCopy(ctx context.Context, noteID, revisionID, userID uuid.UUID, actorID string) (*Note, error) {
	rev, err := s.GetRevision(ctx, noteID, revisionID, userID)
	if err != nil {
		return nil, err
	}
	var notebookID *uuid.UUID
	err = s.pool.QueryRow(ctx, `
		SELECT notebook_id FROM brain.note_notes WHERE id = $1 AND user_id = $2
	`, noteID, userID).Scan(&notebookID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	n, _, err := s.CreateNote(ctx, CreateNoteInput{
		UserID: userID, NotebookID: notebookID,
		Title: rev.Title + RevisionCopySuffix, ContentMD: rev.ContentMD,
		ActorID: actorID,
	})
	if err != nil {
		return nil, err
	}

	// 复制源笔记的标签关联（标签都是本人 scope 内的活标签才挂得上）。
	tagRows, err := s.pool.Query(ctx, `
		SELECT nt.tag_id FROM brain.note_note_tags nt
		JOIN brain.note_tags t ON t.id = nt.tag_id
		WHERE nt.note_id = $1 AND t.user_id = $2 AND t.deleted_at IS NULL
	`, noteID, userID)
	if err != nil {
		return nil, err
	}
	var tagIDs []uuid.UUID
	for tagRows.Next() {
		var tagID uuid.UUID
		if err := tagRows.Scan(&tagID); err != nil {
			tagRows.Close()
			return nil, err
		}
		tagIDs = append(tagIDs, tagID)
	}
	tagRows.Close()
	if err := tagRows.Err(); err != nil {
		return nil, err
	}
	if len(tagIDs) > 0 {
		if err := s.SetNoteTags(ctx, n.ID, userID, tagIDs, actorID); err != nil {
			return nil, err
		}
	}
	return n, nil
}

// PruneRevisions —— 清理历史版本：只删 change_type='edit' 且
// 超过 keepDays 天、且不在该笔记最近 keepRecent 条 edit 版本内的行；
// restore 版本永久保留。周期由 cmd/brain/main.go 的 prune worker
// 驱动（启动一轮 + 每 24h）。返回删除行数。
func (s *Store) PruneRevisions(ctx context.Context, keepRecent, keepDays int) (int64, error) {
	if keepRecent <= 0 {
		keepRecent = PruneDefaultKeepRecent
	}
	if keepDays <= 0 {
		keepDays = PruneDefaultKeepDays
	}
	tag, err := s.pool.Exec(ctx, `
		WITH ranked AS (
			SELECT id, ROW_NUMBER() OVER (PARTITION BY note_id ORDER BY created_at DESC, id DESC) AS rn
			FROM brain.note_revisions
			WHERE change_type = 'edit'
		)
		DELETE FROM brain.note_revisions r
		USING ranked k
		WHERE r.id = k.id
		  AND k.rn > $1
		  AND r.created_at < now() - make_interval(days => $2)
	`, keepRecent, keepDays)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// ─── Tags ───────────────────────────────────────────────

// CreateTag —— (scope_key, lower(name)) 幂等：已存在则直接返回。
func (s *Store) CreateTag(ctx context.Context, userID uuid.UUID, name, actorID string) (*Tag, error) {
	scopeKey := tagScopeKey(userID)
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	t := &Tag{}
	err = tx.QueryRow(ctx, `
		INSERT INTO brain.note_tags (user_id, scope_key, name)
		VALUES ($1, $2, $3)
		ON CONFLICT DO NOTHING
		RETURNING id, user_id, scope_key, name, created_at, updated_at
	`, userID, scopeKey, name).Scan(&t.ID, &t.UserID, &t.ScopeKey, &t.Name, &t.CreatedAt, &t.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		// 撞唯一索引（含部分唯一索引，ON CONFLICT 无法推断表达式索引，
		// 用 DO NOTHING + 回查兜底）。
		err = tx.QueryRow(ctx, `
			SELECT id, user_id, scope_key, name, created_at, updated_at
			FROM brain.note_tags
			WHERE scope_key = $1 AND lower(name) = lower($2) AND deleted_at IS NULL
		`, scopeKey, name).Scan(&t.ID, &t.UserID, &t.ScopeKey, &t.Name, &t.CreatedAt, &t.UpdatedAt)
	}
	if err != nil {
		return nil, fmt.Errorf("create tag: %w", err)
	}
	if err := emitEvent(ctx, tx, userID, "user", actorID, "tag.created", map[string]any{
		"tag_id": t.ID, "name": t.Name,
	}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return t, nil
}

func (s *Store) ListTags(ctx context.Context, userID uuid.UUID) ([]*Tag, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, user_id, scope_key, name, created_at, updated_at
		FROM brain.note_tags
		WHERE user_id = $1 AND deleted_at IS NULL
		ORDER BY lower(name)
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Tag
	for rows.Next() {
		t := &Tag{}
		if err := rows.Scan(&t.ID, &t.UserID, &t.ScopeKey, &t.Name, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// SetNoteTags —— 整组替换笔记的标签关联（仅限本人活笔记 + 本人活标签）。
func (s *Store) SetNoteTags(ctx context.Context, noteID, userID uuid.UUID, tagIDs []uuid.UUID, actorID string) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := s.GetNote(ctx, noteID, userID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM brain.note_note_tags WHERE note_id = $1
	`, noteID); err != nil {
		return err
	}
	for _, tagID := range tagIDs {
		tag, err := tx.Exec(ctx, `
			INSERT INTO brain.note_note_tags (note_id, tag_id)
			SELECT $1, t.id FROM brain.note_tags t
			WHERE t.id = $2 AND t.user_id = $3 AND t.deleted_at IS NULL
			ON CONFLICT DO NOTHING
		`, noteID, tagID, userID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound // 标签不存在或不属于本人
		}
	}
	if err := emitEvent(ctx, tx, userID, "user", actorID, "note.tags_updated", map[string]any{
		"note_id": noteID, "tag_ids": tagIDs,
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ─── Events ─────────────────────────────────────────────

func emitEvent(ctx context.Context, tx pgx.Tx, userID uuid.UUID, actorType, actorID, eventType string, payload map[string]any) error {
	pl, _ := json.Marshal(payload)
	_, err := tx.Exec(ctx, `
		INSERT INTO brain.events (scope, actor_type, actor_id, event_type, payload)
		VALUES ($1, $2, $3, $4, $5)
	`, scopeFor(userID), actorType, actorID, eventType, pl)
	return err
}

// EventsSince —— 增量追赶；since=0 表示从头拉。
func (s *Store) EventsSince(ctx context.Context, userID uuid.UUID, since int64, limit int) ([]Event, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, scope, actor_type, actor_id, event_type, payload, schema_ver, created_at
		FROM brain.events WHERE scope = $1 AND id > $2 ORDER BY id LIMIT $3
	`, scopeFor(userID), since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Event, 0, 32)
	for rows.Next() {
		var e Event
		var payload []byte
		if err := rows.Scan(&e.ID, &e.Scope, &e.ActorType, &e.ActorID, &e.EventType,
			&payload, &e.SchemaVer, &e.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(payload, &e.Payload)
		out = append(out, e)
	}
	return out, rows.Err()
}

// LatestEventID —— 用户 scope 当前最大事件 id；changes 响应的 latest 游标
// （即使本次增量为空，客户端也能推进 checkpoint）。
func (s *Store) LatestEventID(ctx context.Context, userID uuid.UUID) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(MAX(id), 0) FROM brain.events WHERE scope = $1
	`, scopeFor(userID)).Scan(&id)
	return id, err
}
