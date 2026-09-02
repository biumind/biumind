// Auto-resolve — the lint worker's closing pass (P2 #20 ①), ported from
// llm_wiki sweep-reviews.ts.
//
// Structural lint findings are deterministic: the set of dedupe_keys a
// full project scan produces IS the set of currently-triggering
// conditions. Any open kind=lint review whose key isn't in that set has
// had its trigger fixed out from under it (dead link repaired, orphan
// gained an inbound link, page deleted) → resolve it automatically
// instead of leaving the user to triage ghosts.
//
// kind=dedup / kind=merge pairs auto-resolve when either referenced
// page no longer exists (deleted or merged away — merge already
// resolves its own pair, this catches direct deletions).
//
// Only dedupe_keys with the "lint:" prefix are eligible: semantic lint
// shares kind=lint but writes "semantic:" keys (see semantic.go) whose
// trigger conditions this pass cannot re-verify, and kind=sweep has its
// own semantics (staleness doesn't "disappear" on a schedule).
package reviews

import (
	"context"
	"strings"

	"github.com/google/uuid"
)

// autoresolveProject runs the closing pass after a successful FULL
// structural scan of one project. currentKeys holds every LintDedupeKey
// the scan produced — collected pre-LLM-filter, because the filter
// judges actionability, not existence: a filtered-out finding still
// means the condition is present and its review must stay open.
// truncated holds page ids whose block list was capped at
// MaxBlocksPerPage; their lint items are skipped since an unseen tail
// block could still hold the triggering link. Returns the number of
// reviews resolved. Resolve goes through Store.SetStatus (open→resolved
// is idempotent).
func (w *LintWorker) autoresolveProject(
	ctx context.Context,
	projectID uuid.UUID,
	currentKeys map[string]struct{},
	truncated map[uuid.UUID]bool,
) int {
	items, err := w.store.ListOpenByKinds(ctx, projectID,
		[]string{KindLint, KindDedup, KindMerge})
	if err != nil {
		w.logger.Warn("lint autoresolve: list failed",
			"project_id", projectID, "err", err)
		return 0
	}
	if len(items) == 0 {
		return 0
	}
	stale, pairs := classifyOpenForResolve(items, currentKeys, truncated)
	if len(pairs) > 0 {
		live, lerr := w.fetchLivePageIDs(ctx, pairs)
		if lerr != nil {
			// Liveness unverifiable → skip pair resolution this round
			// (fail-closed: wrongly resolving a live pair loses a real
			// finding; skipping just retries next tick).
			w.logger.Warn("lint autoresolve: page liveness failed",
				"project_id", projectID, "err", lerr)
		} else {
			stale = append(stale, itemsWithDeadPages(pairs, live)...)
		}
	}
	resolved := 0
	for _, id := range stale {
		if rerr := w.store.SetStatus(ctx, id, StatusResolved); rerr != nil {
			w.logger.Warn("lint autoresolve: resolve failed",
				"review_id", id, "err", rerr)
			continue
		}
		resolved++
	}
	return resolved
}

// classifyOpenForResolve partitions open review items into:
//   - lintStale: kind=lint items with a "lint:" dedupe_key absent from
//     currentKeys (condition gone). Items on truncated pages are skipped
//     — the scan didn't see all their blocks, so a missing key proves
//     nothing.
//   - pairs: kind=dedup / kind=merge items, returned for page-liveness
//     checking (needs a DB lookup the pure layer can't do).
func classifyOpenForResolve(
	items []*Item,
	currentKeys map[string]struct{},
	truncated map[uuid.UUID]bool,
) (lintStale []uuid.UUID, pairs []*Item) {
	for _, it := range items {
		switch it.Kind {
		case KindLint:
			if !strings.HasPrefix(it.DedupeKey, "lint:") {
				continue // semantic-lint row — trigger not re-verifiable here
			}
			if len(it.PageIDs) > 0 && truncated[it.PageIDs[0]] {
				continue
			}
			if _, ok := currentKeys[it.DedupeKey]; !ok {
				lintStale = append(lintStale, it.ID)
			}
		case KindDedup, KindMerge:
			pairs = append(pairs, it)
		}
	}
	return lintStale, pairs
}

// itemsWithDeadPages returns the ids of pair items referencing at least
// one page missing from `live` (deleted or merged away). Items with no
// page_ids can't be verified and stay open.
func itemsWithDeadPages(items []*Item, live map[uuid.UUID]struct{}) []uuid.UUID {
	var out []uuid.UUID
	for _, it := range items {
		if len(it.PageIDs) == 0 {
			continue
		}
		for _, pid := range it.PageIDs {
			if _, ok := live[pid]; !ok {
				out = append(out, it.ID)
				break
			}
		}
	}
	return out
}

// fetchLivePageIDs returns the set of still-live page ids referenced by
// the given items (one round trip).
func (w *LintWorker) fetchLivePageIDs(ctx context.Context, items []*Item) (map[uuid.UUID]struct{}, error) {
	seen := map[uuid.UUID]struct{}{}
	var ids []uuid.UUID
	for _, it := range items {
		for _, pid := range it.PageIDs {
			if _, ok := seen[pid]; !ok {
				seen[pid] = struct{}{}
				ids = append(ids, pid)
			}
		}
	}
	live := map[uuid.UUID]struct{}{}
	if len(ids) == 0 {
		return live, nil
	}
	rows, err := w.pool.Query(ctx, `
		SELECT id FROM brain.pages WHERE id = ANY($1) AND deleted_at IS NULL
	`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		live[id] = struct{}{}
	}
	return live, rows.Err()
}
