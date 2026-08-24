package repoanalyze

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ─── Integration tests (DATABASE_URL-gated, restore_test.go convention)
//
// The poller is a DB-state machine: every case inserts a gh_private
// catalogue row with a scripted repo_meta, points the GitHub client at
// an httptest stub, runs one PollAll tick and asserts the merged
// repo_meta (plus the update_available event where it applies).

func openPollDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping poller integration tests")
	}
	p, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(p.Close)
	for _, table := range []string{"app_center.apps", "app_center.events"} {
		var exists bool
		if err := p.QueryRow(context.Background(),
			`SELECT EXISTS (SELECT 1 FROM information_schema.tables
			    WHERE table_schema = split_part($1, '.', 1)
			      AND table_name = split_part($1, '.', 2))`,
			table).Scan(&exists); err != nil || !exists {
			t.Skipf("table %s not available (%v) — run the service migrations first", table, err)
		}
	}
	return p
}

// insertPollApp writes a gh_private catalogue row with the given
// repo_meta and returns its id.
func insertPollApp(t *testing.T, pool *pgxpool.Pool, meta map[string]any) (id, identifier string) {
	t.Helper()
	identifier = "gh-poll-" + uuid.NewString()[:8]
	raw, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("marshal meta: %v", err)
	}
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO app_center.apps
			(id, identifier, name, description, source,
			 manifest, manifest_hash, version, category, status,
			 tier, repo_meta, adapter_source)
		VALUES ($1, $2, $2, '', 'gh_private',
		        '{}', $3, '0.1.0', 'utility', 'active',
		        'private', $4, 'auto')
		RETURNING id
	`, "app_"+identifier, identifier, strings.Repeat("a", 64), raw,
	).Scan(&id); err != nil {
		t.Fatalf("insert apps row: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `DELETE FROM app_center.events WHERE scope = $1`, "app:"+id)
		_, _ = pool.Exec(ctx, `DELETE FROM app_center.apps WHERE id = $1`, id)
	})
	return id, identifier
}

// pollGitHubStub scripts the three endpoints the poller consumes. All
// counters are atomic — the worker pool hits them concurrently.
//
// Isolation note: gated tests across packages share one DATABASE_URL,
// and `go test` runs packages in parallel — a tick can pick up gh_*
// rows inserted by another package's tests. Every test therefore uses
// a unique repo name so only its own row matches the stub's handlers
// (foreign rows 404 → failure path, which never touches latest_* /
// update_available), and assertions target the test's own row state
// rather than PollStats totals.
type pollGitHubStub struct {
	releaseStatus int    // status for releases/latest (default 200)
	releaseTag    string // tag_name when 200
	releaseETag   string // ETag sent with the 200 release response
	notModifiedOn string // If-None-Match value that earns a 304
	commitSHAs    map[string]string
	commitCalls   atomic.Int32
	releaseCalls  atomic.Int32
}

func (s *pollGitHubStub) server(t *testing.T, repo string) *httptest.Server {
	t.Helper()
	base := "/repos/octocat/" + repo
	mux := http.NewServeMux()
	mux.HandleFunc(base+"/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		s.releaseCalls.Add(1)
		if s.notModifiedOn != "" && r.Header.Get("If-None-Match") == s.notModifiedOn {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		status := s.releaseStatus
		if status == 0 {
			status = http.StatusOK
		}
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		if s.releaseETag != "" {
			w.Header().Set("ETag", s.releaseETag)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": s.releaseTag})
	})
	mux.HandleFunc(base+"/commits/", func(w http.ResponseWriter, r *http.Request) {
		s.commitCalls.Add(1)
		ref := r.URL.Path[len(base+"/commits/"):]
		sha, ok := s.commitSHAs[ref]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"sha": sha})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// uniqueRepo gives each test its own GitHub repo name (see the
// isolation note on pollGitHubStub).
func uniqueRepo() string { return "poll" + uuid.NewString()[:8] }

func newTestPoller(pool *pgxpool.Pool, ghURL string) *Poller {
	p := NewPoller(pool, NewClient(ghURL, ""))
	p.Interval = time.Hour
	p.Concurrency = 1 // deterministic assertions
	return p
}

// readPollMeta fetches the full repo_meta of one row.
func readPollMeta(t *testing.T, pool *pgxpool.Pool, id string) map[string]any {
	t.Helper()
	var raw []byte
	if err := pool.QueryRow(context.Background(),
		`SELECT repo_meta FROM app_center.apps WHERE id = $1`, id,
	).Scan(&raw); err != nil {
		t.Fatalf("read repo_meta: %v", err)
	}
	m := map[string]any{}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("decode repo_meta: %v", err)
	}
	return m
}

func baseMeta(repo string) map[string]any {
	return map[string]any{
		"url":            "https://github.com/octocat/" + repo,
		"default_branch": "main",
		"latest_ref":     "v1.0.0",
		"latest_sha":     "oldsha",
		"installed_ref":  "v1.0.0",
		"installed_sha":  "oldsha",
	}
}

// New release → latest_* advance, update_available flips, the flip
// event lands in app_center.events, schedule resets.
func TestPoller_NewReleaseSetsUpdateAvailable(t *testing.T) {
	pool := openPollDB(t)
	repo := uniqueRepo()
	stub := &pollGitHubStub{
		releaseTag:  "v2.0.0",
		releaseETag: `W/"rel-v2"`,
		commitSHAs:  map[string]string{"v2.0.0": "newsha"},
	}
	p := newTestPoller(pool, stub.server(t, repo).URL)

	id, _ := insertPollApp(t, pool, baseMeta(repo))
	stats, err := p.PollAll(context.Background())
	if err != nil {
		t.Fatalf("PollAll: %v", err)
	}
	if stats.Considered < 1 {
		t.Errorf("stats = %+v, want at least our row considered", stats)
	}

	m := readPollMeta(t, pool, id)
	if got := metaString(m, "latest_ref"); got != "v2.0.0" {
		t.Errorf("latest_ref = %q, want v2.0.0", got)
	}
	if got := metaString(m, "latest_sha"); got != "newsha" {
		t.Errorf("latest_sha = %q, want newsha", got)
	}
	if !metaBool(m, "update_available") {
		t.Error("update_available = false, want true")
	}
	if got := metaString(m, "etag"); got != `W/"rel-v2"` {
		t.Errorf("etag = %q, want W/\"rel-v2\"", got)
	}
	if got := metaInt(m, "consecutive_failures"); got != 0 {
		t.Errorf("consecutive_failures = %d, want 0", got)
	}
	if got := metaInt(m, "poll_interval_sec"); got != 3600 {
		t.Errorf("poll_interval_sec = %d, want 3600", got)
	}
	next, err := time.Parse(time.RFC3339Nano, metaString(m, "next_poll_at"))
	if err != nil || time.Until(next) < 50*time.Minute {
		t.Errorf("next_poll_at = %q (%v), want ~1h out", metaString(m, "next_poll_at"), err)
	}

	// Flip event on the app scope.
	var n int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM app_center.events
		 WHERE scope = $1 AND event_type = 'app.upgraded'
		   AND payload->>'action' = 'update_available'
		   AND payload->>'ref' = 'v2.0.0'
	`, "app:"+id).Scan(&n); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if n != 1 {
		t.Errorf("update_available events = %d, want 1", n)
	}

	// A second tick over the same state must not re-fire the event.
	m2 := baseMeta(repo)
	m2["latest_ref"], m2["latest_sha"] = "v2.0.0", "newsha"
	m2["update_available"] = true
	id2, _ := insertPollApp(t, pool, m2)
	if _, err := p.PollAll(context.Background()); err != nil {
		t.Fatalf("PollAll 2: %v", err)
	}
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM app_center.events
		 WHERE scope = $1 AND payload->>'action' = 'update_available'
	`, "app:"+id2).Scan(&n); err != nil {
		t.Fatalf("count events 2: %v", err)
	}
	if n != 0 {
		t.Errorf("repeat poll re-fired update_available event (%d), want 0", n)
	}
}

// 304 short-circuits: no commit fallback call, no state churn besides
// the schedule, no event.
func TestPoller_NotModifiedShortCircuits(t *testing.T) {
	pool := openPollDB(t)
	repo := uniqueRepo()
	stub := &pollGitHubStub{
		releaseTag:    "v2.0.0",
		notModifiedOn: `W/"cached"`,
		commitSHAs:    map[string]string{"v2.0.0": "newsha"},
	}
	p := newTestPoller(pool, stub.server(t, repo).URL)

	meta := baseMeta(repo)
	meta["etag"] = `W/"cached"`
	id, _ := insertPollApp(t, pool, meta)

	if _, err := p.PollAll(context.Background()); err != nil {
		t.Fatalf("PollAll: %v", err)
	}
	if stub.commitCalls.Load() != 0 {
		t.Errorf("commit endpoint hit %d times on a 304, want 0", stub.commitCalls.Load())
	}

	m := readPollMeta(t, pool, id)
	if got := metaString(m, "latest_ref"); got != "v1.0.0" {
		t.Errorf("latest_ref = %q, want untouched v1.0.0", got)
	}
	if metaBool(m, "update_available") {
		t.Error("update_available flipped on a 304")
	}
	if got := metaInt(m, "consecutive_failures"); got != 0 {
		t.Errorf("consecutive_failures = %d, want 0", got)
	}
	if metaString(m, "next_poll_at") == "" {
		t.Error("next_poll_at not advanced on 304")
	}
}

// Upstream failure → consecutive_failures increments and the next poll
// backs off to interval * 2^failures.
func TestPoller_FailureBackoff(t *testing.T) {
	pool := openPollDB(t)
	repo := uniqueRepo()
	stub := &pollGitHubStub{releaseStatus: http.StatusInternalServerError}
	p := newTestPoller(pool, stub.server(t, repo).URL)

	meta := baseMeta(repo)
	meta["consecutive_failures"] = 1 // second consecutive failure
	id, _ := insertPollApp(t, pool, meta)

	before := time.Now()
	if _, err := p.PollAll(context.Background()); err != nil {
		t.Fatalf("PollAll: %v", err)
	}

	m := readPollMeta(t, pool, id)
	if got := metaInt(m, "consecutive_failures"); got != 2 {
		t.Errorf("consecutive_failures = %d, want 2", got)
	}
	// failures=2 → backoff = interval * 2^2 = 4h.
	next, err := time.Parse(time.RFC3339Nano, metaString(m, "next_poll_at"))
	if err != nil {
		t.Fatalf("parse next_poll_at: %v", err)
	}
	delay := next.Sub(before)
	if delay < 3*time.Hour+50*time.Minute || delay > 4*time.Hour+time.Minute {
		t.Errorf("backoff = %v, want ~4h (interval 1h * 2^2)", delay)
	}
	// A failure must not touch the resolved refs.
	if got := metaString(m, "latest_ref"); got != "v1.0.0" {
		t.Errorf("latest_ref = %q after failure, want untouched v1.0.0", got)
	}
}

// Repos without releases diff on the default-branch head sha; a moved
// branch flags the update for branch-pinned installs.
func TestPoller_NoReleaseUsesBranchSHA(t *testing.T) {
	pool := openPollDB(t)
	repo := uniqueRepo()
	stub := &pollGitHubStub{
		releaseStatus: http.StatusNotFound, // LatestRelease → "", nil
		commitSHAs:    map[string]string{"main": "branchsha2"},
	}
	p := newTestPoller(pool, stub.server(t, repo).URL)

	meta := baseMeta(repo)
	meta["latest_ref"], meta["installed_ref"] = "main", "main"
	meta["latest_sha"], meta["installed_sha"] = "branchsha1", "branchsha1"
	id, _ := insertPollApp(t, pool, meta)

	if _, err := p.PollAll(context.Background()); err != nil {
		t.Fatalf("PollAll: %v", err)
	}

	m := readPollMeta(t, pool, id)
	if got := metaString(m, "latest_ref"); got != "main" {
		t.Errorf("latest_ref = %q, want main", got)
	}
	if got := metaString(m, "latest_sha"); got != "branchsha2" {
		t.Errorf("latest_sha = %q, want branchsha2", got)
	}
	if !metaBool(m, "update_available") {
		t.Error("update_available = false, want true (branch moved)")
	}
}
