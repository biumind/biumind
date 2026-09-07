// Wikilink rewriting — the single place that knows how to rename a
// `[[target]]` inside body_md without collateral damage.
//
// Two callers:
//
//   - MergePages: fold `[[duplicate-title]]` → `[[canonical-title]]`
//     across every other live page in the project, same tx
//   - reviews apply-fix: rewrite one dead `[[target]]` to the suggested
//     live title (POST /reviews/{id}/apply-fix)
//
// Matching is exact-target and case-insensitive. Substring traps like
// `[[Alpha2]]` when renaming "Alpha" NEVER match — that false-positive
// class is the lesson from llm_wiki's source-delete-decision incident,
// where a naive string replace corrupted unrelated links.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/biumind/biumind/services/brain/internal/wiki/mdparse"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// RewriteWikilinks rewrites every `[[oldTarget]]` and `[[oldTarget|alias]]`
// occurrence in body to point at newTarget, preserving the alias verbatim.
// The target segment must equal oldTarget modulo case and surrounding
// whitespace; longer/shorter targets that merely contain oldTarget as a
// substring are left untouched.
//
// Returns the rewritten body and the number of links rewritten. A zero
// count means the body is returned unchanged.
func RewriteWikilinks(body, oldTarget, newTarget string) (string, int) {
	oldT := strings.TrimSpace(oldTarget)
	newT := strings.TrimSpace(newTarget)
	if oldT == "" || newT == "" || body == "" {
		return body, 0
	}
	re := regexp.MustCompile(`(?i)\[\[\s*` + regexp.QuoteMeta(oldT) + `\s*(\|[^\]\n]*)?\]\]`)
	n := 0
	out := re.ReplaceAllStringFunc(body, func(m string) string {
		// ReplaceAllStringFunc + FindStringSubmatch (not ReplaceAllString)
		// so a `$` in newTarget is treated literally, not as a group ref.
		alias := re.FindStringSubmatch(m)[1]
		n++
		return "[[" + newT + alias + "]]"
	})
	return out, n
}

// FindLivePageByTitle returns the most recently updated live page in
// projectID whose title equals `title` case-insensitively (both sides
// trimmed). reviews apply-fix uses it to re-validate a suggested target
// that may have been renamed or deleted since the lint scan ran.
func (s *Store) FindLivePageByTitle(ctx context.Context, projectID uuid.UUID, title string) (*Page, error) {
	p := &Page{}
	fm := []byte("{}")
	err := s.pool.QueryRow(ctx, `
		SELECT id, project_id, parent_id, title, frontmatter, body_md, share_mode, version, created_at, updated_at
		FROM brain.pages
		WHERE project_id = $1 AND deleted_at IS NULL
		  AND lower(btrim(title)) = lower(btrim($2))
		ORDER BY updated_at DESC
		LIMIT 1
	`, projectID, title).Scan(
		&p.ID, &p.ProjectID, &p.ParentID, &p.Title, &fm, &p.BodyMd, &p.ShareMode, &p.Version,
		&p.CreatedAt, &p.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(fm, &p.Frontmatter)
	return p, nil
}

// rewriteMergeBacklinksTx rewrites `[[oldTitle]]` → `[[newTitle]]` in
// every OTHER live page of the project, inside the merge transaction.
// Returns the number of pages rewritten.
//
// Each affected page goes through the same pipeline as UpdatePageBody:
// pre-write revision snapshot → body_md update (version+1) →
// reconcileBlocksTx re-projection → page.updated event. body_md stays
// authoritative and blocks never drift.
//
// Two-pass locking: first an unlocked LIKE pre-filter to find candidate
// pages cheaply, then SELECT ... FOR UPDATE on just the matching ids.
// The row lock serialises against a concurrent editor save — their
// UpdatePageBody carries an OCC version check, so whoever commits second
// either rewrites the freshest body (us) or gets a clean 409 (them).
func rewriteMergeBacklinksTx(
	ctx context.Context, tx pgx.Tx,
	projectID, canonicalID, duplicateID uuid.UUID,
	oldTitle, newTitle, actor, runID string,
) (int, error) {
	if strings.TrimSpace(oldTitle) == "" || strings.EqualFold(
		strings.TrimSpace(oldTitle), strings.TrimSpace(newTitle)) {
		// Untitled duplicate has no links to rewrite; same-title merge
		// leaves every [[title]] already resolving to canonical.
		return 0, nil
	}

	// Pass 1: cheap candidate scan (no lock). `body_md LIKE '%[['` keeps
	// seq-scan cost proportional to pages that can contain links at all;
	// the exact-match decision happens in Go via RewriteWikilinks.
	rows, err := tx.Query(ctx, `
		SELECT id, body_md FROM brain.pages
		WHERE project_id = $1 AND deleted_at IS NULL
		  AND id <> $2 AND id <> $3
		  AND body_md LIKE '%[[%'
	`, projectID, canonicalID, duplicateID)
	if err != nil {
		return 0, fmt.Errorf("merge rewrite candidates: %w", err)
	}
	var candidates []uuid.UUID
	for rows.Next() {
		var (
			id   uuid.UUID
			body string
		)
		if err := rows.Scan(&id, &body); err != nil {
			rows.Close()
			return 0, err
		}
		if _, n := RewriteWikilinks(body, oldTitle, newTitle); n > 0 {
			candidates = append(candidates, id)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(candidates) == 0 {
		return 0, nil
	}

	// Pass 2: lock + re-read so the rewrite applies to the freshest body.
	lockRows, err := tx.Query(ctx, `
		SELECT id, title, body_md FROM brain.pages
		WHERE id = ANY($1) AND deleted_at IS NULL
		FOR UPDATE
	`, candidates)
	if err != nil {
		return 0, fmt.Errorf("merge rewrite lock: %w", err)
	}
	type lockedPage struct {
		id    uuid.UUID
		title string
		body  string
	}
	var locked []lockedPage
	for lockRows.Next() {
		var lp lockedPage
		if err := lockRows.Scan(&lp.id, &lp.title, &lp.body); err != nil {
			lockRows.Close()
			return 0, err
		}
		locked = append(locked, lp)
	}
	lockRows.Close()
	if err := lockRows.Err(); err != nil {
		return 0, err
	}

	rewritten := 0
	for _, lp := range locked {
		newBody, n := RewriteWikilinks(lp.body, oldTitle, newTitle)
		if n == 0 {
			continue // link disappeared between the two passes
		}
		if err := snapshotPageRevisionTx(ctx, tx, lp.id, projectID, actor, runID); err != nil {
			return rewritten, fmt.Errorf("snapshot pre-rewrite %s: %w", lp.id, err)
		}
		var version int
		if err := tx.QueryRow(ctx, `
			UPDATE brain.pages SET body_md = $2,
			    version = version + 1, updated_at = now()
			WHERE id = $1 AND deleted_at IS NULL
			RETURNING version
		`, lp.id, newBody).Scan(&version); err != nil {
			return rewritten, fmt.Errorf("rewrite body %s: %w", lp.id, err)
		}
		if err := reconcileBlocksTx(ctx, tx, lp.id, mdparse.ParseBlocks(newBody)); err != nil {
			return rewritten, fmt.Errorf("reproject blocks %s: %w", lp.id, err)
		}
		if err := emitEvent(ctx, tx, projectID, "user", actor, "page.updated", map[string]any{
			"page_id": lp.id, "version": version, "title": lp.title,
			"body": true, "cause": "merge_rewrite",
		}); err != nil {
			return rewritten, err
		}
		rewritten++
	}
	return rewritten, nil
}
