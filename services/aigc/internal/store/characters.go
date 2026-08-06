package store

// characters.go — aigc.characters 仓储 (数字人角色).

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Character 对应 aigc.characters 一行.
// UserID==nil 是系统内置角色 (公开可见).
type Character struct {
	ID           uuid.UUID
	UserID       *uuid.UUID
	Name         string
	AvatarURL    string // "cas:<sha>"
	VoiceDefault string
	Config       []byte // raw jsonb
	IsPublic     bool
	CreatedAt    time.Time
}

const characterColumns = `id, user_id, name, avatar_url, voice_default, config, is_public, created_at`

func scanCharacter(r scanner) (*Character, error) {
	c := &Character{}
	var avatarURL, voiceDefault *string
	err := r.Scan(&c.ID, &c.UserID, &c.Name, &avatarURL, &voiceDefault, &c.Config, &c.IsPublic, &c.CreatedAt)
	if err != nil {
		return nil, err
	}
	if avatarURL != nil {
		c.AvatarURL = *avatarURL
	}
	if voiceDefault != nil {
		c.VoiceDefault = *voiceDefault
	}
	return c, nil
}

// CreateCharacterArgs — 用户创建角色. user_id=nil 仅 admin 能调 (系统内置).
type CreateCharacterArgs struct {
	UserID       *uuid.UUID
	Name         string
	AvatarURL    string
	VoiceDefault string
	Config       any
	IsPublic     bool
}

func (s *Store) CreateCharacter(ctx context.Context, a CreateCharacterArgs) (*Character, error) {
	var configJSON []byte
	if a.Config != nil {
		var err error
		configJSON, err = json.Marshal(a.Config)
		if err != nil {
			return nil, fmt.Errorf("marshal config: %w", err)
		}
	}
	row := s.pool.QueryRow(ctx, `
		INSERT INTO aigc.characters
			(user_id, name, avatar_url, voice_default, config, is_public)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING `+characterColumns,
		a.UserID, a.Name, nullableStr(a.AvatarURL), nullableStr(a.VoiceDefault),
		nullableJSON(configJSON), a.IsPublic,
	)
	return rowOrErr(scanCharacter(row))
}

// GetCharacter 取单个 (不区分 owner; 调用方按需校验).
func (s *Store) GetCharacter(ctx context.Context, id uuid.UUID) (*Character, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT `+characterColumns+`
		FROM aigc.characters
		WHERE id = $1
	`, id)
	return rowOrErr(scanCharacter(row))
}

// ListCharactersArgs — UserID 为空时 (admin 看全部); 普通用户传自己的 uid + IncludePublic
// 拿 自己的 ∪ 系统内置 (user_id IS NULL).
type ListCharactersArgs struct {
	UserID        *uuid.UUID
	IncludePublic bool
	Limit, Offset int
}

func (s *Store) ListCharacters(ctx context.Context, a ListCharactersArgs) ([]*Character, error) {
	limit := a.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	if a.UserID == nil {
		// admin: 全表
		rows, err := s.pool.Query(ctx, `
			SELECT `+characterColumns+`
			FROM aigc.characters
			ORDER BY created_at DESC
			LIMIT $1 OFFSET $2
		`, limit, max0(a.Offset))
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		return collectCharacters(rows)
	}

	// 普通用户: 自己 + (可选) 系统内置 + (可选) 其他用户的 public
	q := `SELECT ` + characterColumns + ` FROM aigc.characters WHERE user_id = $1`
	args := []any{*a.UserID}
	if a.IncludePublic {
		q += ` OR (user_id IS NULL OR is_public = true)`
	}
	q += fmt.Sprintf(` ORDER BY (user_id IS NULL) ASC, created_at DESC LIMIT $%d OFFSET $%d`,
		len(args)+1, len(args)+2)
	args = append(args, limit, max0(a.Offset))

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectCharacters(rows)
}

func collectCharacters(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}) ([]*Character, error) {
	var out []*Character
	for rows.Next() {
		c, err := scanCharacter(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// DeleteCharacter 删自己的角色 (user_id 必须匹配, 否则返回 ErrNotFound).
// 系统内置角色 (user_id IS NULL) 不能通过此接口删, 需要 admin 走单独路径.
func (s *Store) DeleteCharacter(ctx context.Context, userID uuid.UUID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM aigc.characters
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
