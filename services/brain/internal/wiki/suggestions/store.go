// brain.wiki_suggestions + brain.wiki_suggestion_votes CRUD。
package suggestions

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("not found")

type Suggestion struct {
	ID        uuid.UUID
	AuthorID  uuid.UUID
	Title     string
	Body      string
	Category  string
	Status    string
	Votes     int
	MyVote    bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Store struct{ pool *pgxpool.Pool }

func New(p *pgxpool.Pool) *Store { return &Store{pool: p} }

const selectSuggestionWithVotes = `
	SELECT
		s.id, s.author_id, s.title, s.body, s.category, s.status,
		s.created_at, s.updated_at,
		COALESCE(vc.cnt, 0) AS votes,
		EXISTS (SELECT 1 FROM brain.wiki_suggestion_votes v
			WHERE v.suggestion_id = s.id AND v.voter_id = $1) AS my_vote
	FROM brain.wiki_suggestions s
	LEFT JOIN (
		SELECT suggestion_id, count(*) AS cnt
		FROM brain.wiki_suggestion_votes
		GROUP BY suggestion_id
	) vc ON vc.suggestion_id = s.id
	WHERE s.deleted_at IS NULL
`

func scan(row pgx.Row) (*Suggestion, error) {
	s := &Suggestion{}
	err := row.Scan(
		&s.ID, &s.AuthorID, &s.Title, &s.Body, &s.Category, &s.Status,
		&s.CreatedAt, &s.UpdatedAt, &s.Votes, &s.MyVote,
	)
	if err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) ListPublic(
	ctx context.Context, voterID uuid.UUID, limit int,
) ([]*Suggestion, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, selectSuggestionWithVotes+
		` ORDER BY votes DESC, s.created_at DESC LIMIT $2`,
		voterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*Suggestion, 0, limit)
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) ListMine(
	ctx context.Context, authorID uuid.UUID, limit int,
) ([]*Suggestion, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, selectSuggestionWithVotes+
		` AND s.author_id = $2 ORDER BY s.created_at DESC LIMIT $3`,
		authorID, authorID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*Suggestion, 0, limit)
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) Get(
	ctx context.Context, id, voterID uuid.UUID,
) (*Suggestion, error) {
	row := s.pool.QueryRow(ctx, selectSuggestionWithVotes+
		` AND s.id = $2`, voterID, id)
	v, err := scan(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return v, nil
}

func (s *Store) Create(
	ctx context.Context, authorID uuid.UUID,
	title, body, category string,
) (*Suggestion, error) {
	if category == "" {
		category = "feature"
	}
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `
		INSERT INTO brain.wiki_suggestions (author_id, title, body, category)
		VALUES ($1, $2, $3, $4)
		RETURNING id
	`, authorID, title, body, category).Scan(&id)
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, id, authorID)
}

func (s *Store) Patch(
	ctx context.Context, id, authorID uuid.UUID,
	title, body, category, status string,
) (*Suggestion, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE brain.wiki_suggestions
		SET title = COALESCE(NULLIF($3, ''), title),
		    body = COALESCE(NULLIF($4, ''), body),
		    category = COALESCE(NULLIF($5, ''), category),
		    status = COALESCE(NULLIF($6, ''), status),
		    updated_at = now()
		WHERE id = $1 AND author_id = $2 AND deleted_at IS NULL
	`, id, authorID, title, body, category, status)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrNotFound
	}
	return s.Get(ctx, id, authorID)
}

func (s *Store) SoftDelete(
	ctx context.Context, id, authorID uuid.UUID,
) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE brain.wiki_suggestions SET deleted_at=now()
		WHERE id=$1 AND author_id=$2 AND deleted_at IS NULL
	`, id, authorID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Vote toggle：up=true 时 INSERT IGNORE，否则 DELETE。返回最新计数。
func (s *Store) Vote(
	ctx context.Context, suggestionID, voterID uuid.UUID, up bool,
) (int, error) {
	if up {
		_, err := s.pool.Exec(ctx, `
			INSERT INTO brain.wiki_suggestion_votes (suggestion_id, voter_id)
			VALUES ($1, $2)
			ON CONFLICT (suggestion_id, voter_id) DO NOTHING
		`, suggestionID, voterID)
		if err != nil {
			return 0, err
		}
	} else {
		_, err := s.pool.Exec(ctx, `
			DELETE FROM brain.wiki_suggestion_votes
			WHERE suggestion_id=$1 AND voter_id=$2
		`, suggestionID, voterID)
		if err != nil {
			return 0, err
		}
	}
	var n int
	err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM brain.wiki_suggestion_votes
		WHERE suggestion_id=$1
	`, suggestionID).Scan(&n)
	return n, err
}
