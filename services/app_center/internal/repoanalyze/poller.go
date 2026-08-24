// GitHub release poller (Repo Apps M2.1, tech plan §2.5).
//
// Scans app_center.apps for gh_*-source rows whose repo_meta.next_poll_at
// is due, then re-resolves the latest ref per repo:
//
//   - releases/latest with a conditional GET (ETag from repo_meta.etag;
//     304 short-circuits the whole diff, pattern from github.go);
//   - repos without releases fall back to /commits/{default_branch} and
//     diff on the head sha.
//
// State lives in the apps.repo_meta jsonb (no extra columns, migration
// 00002): latest_ref / latest_sha / etag / last_polled_at /
// next_poll_at / consecutive_failures / poll_interval_sec /
// update_available. update_available flips true when the freshly
// resolved ref (or sha, for branch-pinned installs) diverges from the
// installed_* pair CreateRepoApp pinned; the flip also writes an
// app.upgraded{action:update_available} event so Realtime can wake the
// client banner (I4: mutation + event in one tx).
//
// Scheduling follows rankings.Scheduler (worker pool, due-query per
// tick) rather than triggers.Dispatcher: polling is a read-mostly
// single-replica job. TODO: if app_center ever scales out, claim due
// rows with FOR UPDATE SKIP LOCKED like the trigger dispatcher does.
//
// Failure backoff mirrors rankings.UpdateFetchState: consecutive_failures
// increments and next_poll_at = now + interval * 2^failures, capped at
// 24h.

package repoanalyze

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/biumind/biumind/services/app_center/internal/events"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// DefaultPollInterval is the per-repo poll cadence when the operator
	// doesn't override REPO_POLL_INTERVAL.
	DefaultPollInterval = 6 * time.Hour
	// maxPollBackoff caps the exponential failure backoff.
	maxPollBackoff         = 24 * time.Hour
	defaultPollConcurrency = 4
	defaultPollBatch       = 50
)

// Poller re-resolves latest refs for repo apps on a due schedule.
type Poller struct {
	Pool   *pgxpool.Pool
	Client *Client
	Logger *slog.Logger

	// Interval is the base poll cadence (success path); failures back
	// off exponentially from it. Defaults to DefaultPollInterval.
	Interval time.Duration
	// Concurrency is the worker-pool width (default 4, like rankings).
	Concurrency int
	// BatchSize caps rows claimed per tick (default 50).
	BatchSize int
}

func NewPoller(pool *pgxpool.Pool, client *Client) *Poller {
	return &Poller{
		Pool:        pool,
		Client:      client,
		Logger:      slog.Default(),
		Interval:    DefaultPollInterval,
		Concurrency: defaultPollConcurrency,
		BatchSize:   defaultPollBatch,
	}
}

// PollStats summarises one PollAll tick.
type PollStats struct {
	Considered int
	OK         int
	Errors     int
	Updates    int // rows where update_available flipped to true
}

// dueApp is one catalogue row due for a poll.
type dueApp struct {
	ID         string
	Identifier string
	Meta       map[string]any // decoded repo_meta; empty map when NULL
}

// PollAll claims the due batch and polls each repo across the worker
// pool. A row-level failure never aborts the tick — it is recorded as
// backoff state on that row and counted in PollStats.Errors.
func (p *Poller) PollAll(ctx context.Context) (PollStats, error) {
	stats := PollStats{}
	if p.Pool == nil || p.Client == nil {
		return stats, errors.New("repoanalyze: poller not wired")
	}
	apps, err := p.dueApps(ctx)
	if err != nil {
		return stats, err
	}
	stats.Considered = len(apps)
	if len(apps) == 0 {
		return stats, nil
	}

	conc := p.Concurrency
	if conc <= 0 {
		conc = defaultPollConcurrency
	}
	jobs := make(chan dueApp)
	var wg sync.WaitGroup
	var mu sync.Mutex
	for i := 0; i < conc; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for app := range jobs {
				updated, err := p.pollOne(ctx, app)
				mu.Lock()
				if err != nil {
					stats.Errors++
					p.Logger.Warn("repo poller: poll failed",
						"identifier", app.Identifier, "err", err.Error())
				} else {
					stats.OK++
					if updated {
						stats.Updates++
					}
				}
				mu.Unlock()
			}
		}()
	}
	for _, app := range apps {
		select {
		case <-ctx.Done():
		case jobs <- app:
		}
	}
	close(jobs)
	wg.Wait()
	return stats, nil
}

// dueApps returns gh_* catalogue rows whose next_poll_at is due (or was
// never set). New repos poll immediately after install.
func (p *Poller) dueApps(ctx context.Context) ([]dueApp, error) {
	limit := p.BatchSize
	if limit <= 0 {
		limit = defaultPollBatch
	}
	rows, err := p.Pool.Query(ctx, `
		SELECT id, identifier, repo_meta
		  FROM app_center.apps
		 WHERE source LIKE 'gh\_%'
		   AND (repo_meta IS NULL
		        OR repo_meta->>'next_poll_at' IS NULL
		        OR (repo_meta->>'next_poll_at')::timestamptz < now())
		 ORDER BY repo_meta->>'next_poll_at' NULLS FIRST
		 LIMIT $1
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("repoanalyze: due apps: %w", err)
	}
	defer rows.Close()

	out := make([]dueApp, 0)
	for rows.Next() {
		var (
			app  dueApp
			meta []byte
		)
		if err := rows.Scan(&app.ID, &app.Identifier, &meta); err != nil {
			return nil, fmt.Errorf("repoanalyze: scan due app: %w", err)
		}
		app.Meta = map[string]any{}
		if len(meta) > 0 {
			if err := json.Unmarshal(meta, &app.Meta); err != nil {
				// A corrupt repo_meta must not kill the tick — treat as
				// empty state (the poll re-resolves everything anyway).
				app.Meta = map[string]any{}
			}
		}
		out = append(out, app)
	}
	return out, rows.Err()
}

// pollOne re-resolves one repo's latest ref and writes the poll state
// back. updated reports whether update_available flipped to true.
func (p *Poller) pollOne(ctx context.Context, app dueApp) (updated bool, err error) {
	owner, repo, err := ParseRepoURL(metaString(app.Meta, "url"))
	if err != nil {
		return false, p.writeFailure(ctx, app, err)
	}

	etag := metaString(app.Meta, "etag")
	tag, newETag, err := p.Client.LatestRelease(ctx, owner, repo, etag)
	switch {
	case errors.Is(err, ErrNotModified):
		// 304 — upstream unchanged. Advance the schedule only; keep
		// latest_*/update_available exactly as they are.
		return false, p.writeState(ctx, app, map[string]any{
			"last_polled_at":       time.Now().UTC(),
			"next_poll_at":         time.Now().UTC().Add(p.interval()),
			"consecutive_failures": 0,
			"poll_interval_sec":    int(p.interval().Seconds()),
		}, nil)
	case err != nil:
		return false, p.writeFailure(ctx, app, err)
	}

	var newRef, newSHA string
	if tag != "" {
		newRef = tag
		// Resolve the tag to its commit sha (commits/{ref} accepts tags).
		sha, err := p.Client.HeadSHA(ctx, owner, repo, tag)
		if err != nil {
			return false, p.writeFailure(ctx, app, err)
		}
		newSHA = sha
	} else {
		// No releases — diff on the default branch head sha instead.
		branch := metaString(app.Meta, "default_branch")
		if branch == "" {
			branch = "main"
		}
		sha, err := p.Client.HeadSHA(ctx, owner, repo, branch)
		if err != nil {
			return false, p.writeFailure(ctx, app, err)
		}
		newRef, newSHA = branch, sha
	}

	installedRef := metaString(app.Meta, "installed_ref")
	installedSHA := metaString(app.Meta, "installed_sha")
	updateAvailable := installedRef != "" && newRef != installedRef
	// Branch-pinned installs keep the same ref across commits — the sha
	// is the only signal there. (Extension over the ref-only rule for
	// ref_type=branch installs.)
	if !updateAvailable && newRef == installedRef &&
		installedSHA != "" && newSHA != "" && newSHA != installedSHA {
		updateAvailable = true
	}

	patch := map[string]any{
		"latest_ref":           newRef,
		"latest_sha":           newSHA,
		"update_available":     updateAvailable,
		"last_polled_at":       time.Now().UTC(),
		"next_poll_at":         time.Now().UTC().Add(p.interval()),
		"consecutive_failures": 0,
		"poll_interval_sec":    int(p.interval().Seconds()),
	}
	if newETag != "" {
		patch["etag"] = newETag
	}

	// Notify only on the false→true flip; a repeated poll of the same
	// new release must not spam Realtime.
	var flip *events.Event
	if updateAvailable && !metaBool(app.Meta, "update_available") {
		flip = &events.Event{
			ScopeKind: "app",
			ScopeID:   app.ID,
			ActorType: events.ActorScheduler,
			ActorID:   "repo-poller",
			Type:      events.AppUpgraded,
			Payload: map[string]any{
				"action":        "update_available",
				"identifier":    app.Identifier,
				"ref":           newRef,
				"sha":           newSHA,
				"installed_ref": installedRef,
			},
		}
	}
	return updateAvailable && flip != nil, p.writeState(ctx, app, patch, flip)
}

// writeFailure records the backoff state for a failed poll. Returns nil
// when the state write succeeded — the original failure is logged by
// the caller via the returned error of pollOne instead.
func (p *Poller) writeFailure(ctx context.Context, app dueApp, cause error) error {
	failures := metaInt(app.Meta, "consecutive_failures") + 1
	backoff := p.interval()
	for i := 0; i < failures; i++ {
		backoff *= 2
		if backoff >= maxPollBackoff {
			backoff = maxPollBackoff
			break
		}
	}
	if err := p.writeState(ctx, app, map[string]any{
		"last_polled_at":       time.Now().UTC(),
		"next_poll_at":         time.Now().UTC().Add(backoff),
		"consecutive_failures": failures,
		"last_error":           cause.Error(),
	}, nil); err != nil {
		return err
	}
	return cause
}

// writeState merges patch into repo_meta (`||` jsonb concat — only the
// keys the poller owns are touched, so a concurrent complete-endpoint
// write of installed_* is not clobbered). When flip is non-nil the
// UPDATE and the event share one tx (I4).
func (p *Poller) writeState(ctx context.Context, app dueApp, patch map[string]any, flip *events.Event) error {
	raw, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("repoanalyze: marshal patch: %w", err)
	}
	const q = `
		UPDATE app_center.apps
		   SET repo_meta  = COALESCE(repo_meta, '{}'::jsonb) || $2::jsonb,
		       updated_at = now()
		 WHERE id = $1
	`
	if flip == nil {
		if _, err := p.Pool.Exec(ctx, q, app.ID, raw); err != nil {
			return fmt.Errorf("repoanalyze: write poll state: %w", err)
		}
		return nil
	}

	tx, err := p.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("repoanalyze: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, q, app.ID, raw); err != nil {
		return fmt.Errorf("repoanalyze: write poll state: %w", err)
	}
	if err := events.Write(ctx, tx, *flip); err != nil {
		return fmt.Errorf("repoanalyze: write update_available event: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("repoanalyze: commit poll state: %w", err)
	}
	return nil
}

func (p *Poller) interval() time.Duration {
	if p.Interval <= 0 {
		return DefaultPollInterval
	}
	return p.Interval
}

// ─── repo_meta accessors ─────────────────────────────────────────────

func metaString(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func metaBool(m map[string]any, key string) bool {
	v, _ := m[key].(bool)
	return v
}

func metaInt(m map[string]any, key string) int {
	// json.Unmarshal decodes numbers as float64.
	if v, ok := m[key].(float64); ok {
		return int(v)
	}
	return 0
}
