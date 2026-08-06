// brain.wiki_conversations + brain.wiki_messages CRUD。
package chat

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("not found")

type Conversation struct {
	ID        uuid.UUID
	ProjectID uuid.UUID
	OwnerID   uuid.UUID
	Title     string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Message struct {
	ID             uuid.UUID
	ConversationID uuid.UUID
	Role           string
	Content        string
	Metadata       map[string]any
	CreatedAt      time.Time
}

type Store struct{ pool *pgxpool.Pool }

func New(p *pgxpool.Pool) *Store { return &Store{pool: p} }

// ─── Conversations ──────────────────────────────────────────────

func (s *Store) ListConversations(
	ctx context.Context, projectID, ownerID uuid.UUID, limit int,
) ([]*Conversation, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, project_id, owner_id, title, created_at, updated_at
		FROM brain.wiki_conversations
		WHERE project_id=$1 AND owner_id=$2 AND deleted_at IS NULL
		ORDER BY updated_at DESC
		LIMIT $3
	`, projectID, ownerID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*Conversation, 0, limit)
	for rows.Next() {
		c := &Conversation{}
		if err := rows.Scan(&c.ID, &c.ProjectID, &c.OwnerID, &c.Title,
			&c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) GetConversation(ctx context.Context, id uuid.UUID) (*Conversation, error) {
	c := &Conversation{}
	err := s.pool.QueryRow(ctx, `
		SELECT id, project_id, owner_id, title, created_at, updated_at
		FROM brain.wiki_conversations WHERE id=$1 AND deleted_at IS NULL
	`, id).Scan(&c.ID, &c.ProjectID, &c.OwnerID, &c.Title,
		&c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return c, nil
}

func (s *Store) CreateConversation(
	ctx context.Context, projectID, ownerID uuid.UUID, title string,
) (*Conversation, error) {
	c := &Conversation{}
	err := s.pool.QueryRow(ctx, `
		INSERT INTO brain.wiki_conversations (project_id, owner_id, title)
		VALUES ($1, $2, $3)
		RETURNING id, project_id, owner_id, title, created_at, updated_at
	`, projectID, ownerID, title).Scan(
		&c.ID, &c.ProjectID, &c.OwnerID, &c.Title,
		&c.CreatedAt, &c.UpdatedAt,
	)
	return c, err
}

func (s *Store) PatchConversation(
	ctx context.Context, id uuid.UUID, title string,
) (*Conversation, error) {
	c := &Conversation{}
	err := s.pool.QueryRow(ctx, `
		UPDATE brain.wiki_conversations
		SET title=$2, updated_at=now()
		WHERE id=$1 AND deleted_at IS NULL
		RETURNING id, project_id, owner_id, title, created_at, updated_at
	`, id, title).Scan(&c.ID, &c.ProjectID, &c.OwnerID, &c.Title,
		&c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return c, nil
}

func (s *Store) SoftDeleteConversation(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE brain.wiki_conversations SET deleted_at=now()
		WHERE id=$1 AND deleted_at IS NULL
	`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ─── Messages ───────────────────────────────────────────────────

func (s *Store) ListMessages(
	ctx context.Context, conversationID uuid.UUID, limit int,
) ([]*Message, error) {
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, conversation_id, role, content, metadata, created_at
		FROM brain.wiki_messages
		WHERE conversation_id=$1
		ORDER BY created_at ASC
		LIMIT $2
	`, conversationID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*Message, 0, limit)
	for rows.Next() {
		m := &Message{}
		var meta []byte
		if err := rows.Scan(&m.ID, &m.ConversationID, &m.Role,
			&m.Content, &meta, &m.CreatedAt); err != nil {
			return nil, err
		}
		if len(meta) > 0 {
			_ = json.Unmarshal(meta, &m.Metadata)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) CreateMessage(
	ctx context.Context, conversationID uuid.UUID, role, content string,
	metadata map[string]any,
) (*Message, error) {
	if metadata == nil {
		metadata = map[string]any{}
	}
	metaJSON, _ := json.Marshal(metadata)
	m := &Message{}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var rawMeta []byte
	err = tx.QueryRow(ctx, `
		INSERT INTO brain.wiki_messages
			(conversation_id, role, content, metadata)
		VALUES ($1, $2, $3, $4)
		RETURNING id, conversation_id, role, content, metadata, created_at
	`, conversationID, role, content, metaJSON).Scan(
		&m.ID, &m.ConversationID, &m.Role, &m.Content,
		&rawMeta, &m.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	if len(rawMeta) > 0 {
		_ = json.Unmarshal(rawMeta, &m.Metadata)
	}
	// 更新 conversation.updated_at（让 list 排序正确）
	_, err = tx.Exec(ctx, `
		UPDATE brain.wiki_conversations SET updated_at=now() WHERE id=$1
	`, conversationID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return m, nil
}

func (s *Store) GetMessage(ctx context.Context, id uuid.UUID) (*Message, error) {
	m := &Message{}
	var meta []byte
	err := s.pool.QueryRow(ctx, `
		SELECT id, conversation_id, role, content, metadata, created_at
		FROM brain.wiki_messages WHERE id=$1
	`, id).Scan(&m.ID, &m.ConversationID, &m.Role, &m.Content,
		&meta, &m.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if len(meta) > 0 {
		_ = json.Unmarshal(meta, &m.Metadata)
	}
	return m, nil
}

func (s *Store) DeleteMessage(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM brain.wiki_messages WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
