// PostgreSQL store for rankings.boards / snapshots / items_seen.
// Schema is in services/app_center/migrations/00007_rankings_schema.sql.

package rankings

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("rankings: board not found")

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

type Board struct {
	ID                  string
	Name                string
	Color               string
	Enabled             bool
	RefreshSec          int
	ExpectedDomain      string
	LastFetchedAt       time.Time
	LastStatus          string
	LastError           string
	ConsecutiveFailures int
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

const boardCols = `id, name, COALESCE(color, 'gray'), enabled, refresh_sec,
	COALESCE(expected_domain, ''),
	COALESCE(last_fetched_at, '0001-01-01'::timestamptz),
	last_status, last_error, consecutive_failures, created_at, updated_at`

func scanBoard(r pgx.Rows) (*Board, error) {
	var b Board
	if err := r.Scan(
		&b.ID, &b.Name, &b.Color, &b.Enabled, &b.RefreshSec, &b.ExpectedDomain,
		&b.LastFetchedAt, &b.LastStatus, &b.LastError, &b.ConsecutiveFailures,
		&b.CreatedAt, &b.UpdatedAt,
	); err != nil {
		return nil, err
	}
	if b.LastFetchedAt.Year() < 2 {
		b.LastFetchedAt = time.Time{}
	}
	return &b, nil
}

func (s *Store) ListBoards(ctx context.Context) ([]*Board, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+boardCols+` FROM rankings.boards ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("rankings: list boards: %w", err)
	}
	defer rows.Close()
	out := make([]*Board, 0)
	for rows.Next() {
		b, err := scanBoard(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s *Store) GetBoard(ctx context.Context, id string) (*Board, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+boardCols+` FROM rankings.boards WHERE id=$1`, id)
	if err != nil {
		return nil, fmt.Errorf("rankings: get board: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, ErrNotFound
	}
	return scanBoard(rows)
}

// DueBoards returns enabled boards whose last_fetched_at + refresh_sec
// < now() (or never fetched).
func (s *Store) DueBoards(ctx context.Context, limit int) ([]*Board, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+boardCols+`
		  FROM rankings.boards
		 WHERE enabled = true
		   AND (last_fetched_at IS NULL
		        OR last_fetched_at + (refresh_sec * interval '1 second') < now())
		 ORDER BY last_fetched_at NULLS FIRST
		 LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("rankings: due boards: %w", err)
	}
	defer rows.Close()
	out := make([]*Board, 0)
	for rows.Next() {
		b, err := scanBoard(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

type FetchOutcome struct {
	Status string // ok|warn|error
	ErrMsg string
}

func (s *Store) UpdateFetchState(ctx context.Context, boardID string, o FetchOutcome) error {
	failExpr := "consecutive_failures = 0"
	if o.Status == "error" || o.Status == "warn" {
		failExpr = "consecutive_failures = consecutive_failures + 1"
	}
	q := `UPDATE rankings.boards
	         SET last_fetched_at = now(),
	             last_status     = $2,
	             last_error      = $3,
	             updated_at      = now(),
	             ` + failExpr + `
	       WHERE id = $1`
	_, err := s.pool.Exec(ctx, q, boardID, o.Status, o.ErrMsg)
	if err != nil {
		return fmt.Errorf("rankings: update board: %w", err)
	}
	return nil
}

// NewItem describes a single item that was first seen in this snapshot
// (not present in items_seen prior). Returned by IngestSnapshot for
// the radar matcher to consume.
type NewItem struct {
	BoardID   string
	Title     string
	URL       string
	TitleHash []byte
}

// IngestSnapshot writes the snapshot row and upserts items_seen,
// returning the items detected as "first seen" (xmax=0). Designed to
// run inside the scheduler's per-board worker; expected_domain
// validation must already have passed.
func (s *Store) IngestSnapshot(ctx context.Context, snap *Snapshot) ([]NewItem, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("rankings: begin: %w", err)
	}
	defer tx.Rollback(ctx)

	// 1. snapshot row — items as JSONB
	itemsJSON, err := json.Marshal(snap.Items)
	if err != nil {
		return nil, fmt.Errorf("rankings: marshal items: %w", err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO rankings.snapshots (board_id, captured_at, updated_time, items)
		VALUES ($1, now(), $2, $3)
		ON CONFLICT (board_id, captured_at) DO NOTHING`,
		snap.BoardID, nullableInt64(snap.UpdatedTime), itemsJSON)
	if err != nil {
		return nil, fmt.Errorf("rankings: insert snapshot: %w", err)
	}

	// 2. items_seen UPSERT, RETURNING xmax = 0 to detect true inserts
	newItems := make([]NewItem, 0)
	for _, it := range snap.Items {
		hash := titleHash(it.Title)
		var isNew bool
		err := tx.QueryRow(ctx, `
			INSERT INTO rankings.items_seen (board_id, title_hash, title, url)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (board_id, title_hash) DO UPDATE
			   SET last_seen_at = now()
			RETURNING (xmax = 0)`,
			snap.BoardID, hash, it.Title, it.URL,
		).Scan(&isNew)
		if err != nil {
			return nil, fmt.Errorf("rankings: upsert items_seen: %w", err)
		}
		if isNew {
			newItems = append(newItems, NewItem{
				BoardID: snap.BoardID, Title: it.Title, URL: it.URL, TitleHash: hash,
			})
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("rankings: commit: %w", err)
	}
	return newItems, nil
}

// LatestSnapshot returns the most recent snapshot for the board (nil
// + ErrNotFound when empty).
func (s *Store) LatestSnapshot(ctx context.Context, boardID string) (*StoredSnapshot, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id, board_id, captured_at, COALESCE(updated_time, 0), items
		  FROM rankings.snapshots
		 WHERE board_id = $1
		 ORDER BY captured_at DESC
		 LIMIT 1`, boardID)
	var s2 StoredSnapshot
	var rawItems []byte
	if err := row.Scan(&s2.ID, &s2.BoardID, &s2.CapturedAt, &s2.UpdatedTime, &rawItems); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("rankings: latest snapshot: %w", err)
	}
	if err := json.Unmarshal(rawItems, &s2.Items); err != nil {
		return nil, fmt.Errorf("rankings: decode items: %w", err)
	}
	return &s2, nil
}

// PreviousSnapshot returns the second-most-recent snapshot for rank
// delta calculations. nil + ErrNotFound if only one exists.
func (s *Store) PreviousSnapshot(ctx context.Context, boardID string) (*StoredSnapshot, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id, board_id, captured_at, COALESCE(updated_time, 0), items
		  FROM rankings.snapshots
		 WHERE board_id = $1
		 ORDER BY captured_at DESC
		 OFFSET 1 LIMIT 1`, boardID)
	var s2 StoredSnapshot
	var rawItems []byte
	if err := row.Scan(&s2.ID, &s2.BoardID, &s2.CapturedAt, &s2.UpdatedTime, &rawItems); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("rankings: prev snapshot: %w", err)
	}
	if err := json.Unmarshal(rawItems, &s2.Items); err != nil {
		return nil, fmt.Errorf("rankings: decode items: %w", err)
	}
	return &s2, nil
}

// IsItemNew returns whether the (board, title) pair is currently
// flagged as "新进榜" — first_seen_at within the last 24h.
func (s *Store) IsItemNew(ctx context.Context, boardID, title string) (bool, error) {
	var v bool
	err := s.pool.QueryRow(ctx, `
		SELECT first_seen_at > now() - interval '24 hours'
		  FROM rankings.items_seen
		 WHERE board_id = $1 AND title_hash = $2`,
		boardID, titleHash(title),
	).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return v, nil
}

// GC trims old snapshots (>24h, keep 1 per hour up to 7d) and removes
// items_seen rows older than 7d. Run from the daily cron.
func (s *Store) GC(ctx context.Context) error {
	// Drop snapshots older than 24h that aren't on the hour boundary,
	// keeping a sparser history out to 7d.
	_, err := s.pool.Exec(ctx, `
		DELETE FROM rankings.snapshots
		 WHERE captured_at < now() - interval '24 hours'
		   AND captured_at > now() - interval '7 days'
		   AND captured_at::timestamp(0) NOT IN (
		     SELECT date_trunc('hour', captured_at)
		       FROM rankings.snapshots
		      WHERE board_id = snapshots.board_id
		        AND captured_at < now() - interval '24 hours'
		   )`)
	if err != nil {
		return fmt.Errorf("rankings: gc snapshots: %w", err)
	}
	_, err = s.pool.Exec(ctx, `
		DELETE FROM rankings.snapshots
		 WHERE captured_at < now() - interval '7 days'`)
	if err != nil {
		return fmt.Errorf("rankings: gc old snapshots: %w", err)
	}
	_, err = s.pool.Exec(ctx, `
		DELETE FROM rankings.items_seen
		 WHERE last_seen_at < now() - interval '7 days'`)
	if err != nil {
		return fmt.Errorf("rankings: gc items_seen: %w", err)
	}
	return nil
}

type StoredSnapshot struct {
	ID          int64
	BoardID     string
	CapturedAt  time.Time
	UpdatedTime int64
	Items       []Item
}

func titleHash(title string) []byte {
	h := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(title))))
	return h[:]
}

func nullableInt64(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}
