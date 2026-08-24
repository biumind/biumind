package installs

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/biumind/biumind/packages/go-sdk/biu/biuapp"
	"github.com/biumind/biumind/services/app_center/internal/repoanalyze"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ─── Pure unit tests (no DB) ───────────────────────────────────────

func TestRejectSecretConfig(t *testing.T) {
	schema := []repoanalyze.EnvField{
		{Name: "API_KEY", Secret: true},
		{Name: "FLAVOUR", Secret: false},
	}

	// Non-secret keys pass through.
	if err := rejectSecretConfig(map[string]any{"FLAVOUR": "vanilla"}, schema); err != nil {
		t.Errorf("non-secret config rejected: %v", err)
	}
	// Empty / nil config is fine.
	if err := rejectSecretConfig(nil, schema); err != nil {
		t.Errorf("nil config rejected: %v", err)
	}
	// A secret key must be refused, wrapped in the sentinel.
	err := rejectSecretConfig(map[string]any{"API_KEY": "sk-live"}, schema)
	if !errors.Is(err, ErrSecretConfigField) {
		t.Errorf("want ErrSecretConfigField, got %v", err)
	}
	// Keys not present in the schema are not policed.
	if err := rejectSecretConfig(map[string]any{"UNKNOWN": "x"}, schema); err != nil {
		t.Errorf("unknown key rejected: %v", err)
	}
}

func TestRuntimeStatusFor(t *testing.T) {
	cases := map[string]string{
		"":          "stopped",
		"live":      "running",
		"failed":    "failed",
		"queued":    "starting",
		"building":  "starting",
		"deploying": "starting",
		"surprise":  "stopped",
	}
	for in, want := range cases {
		if got := RuntimeStatusFor(in); got != want {
			t.Errorf("RuntimeStatusFor(%q) = %q, want %q", in, got, want)
		}
	}
}

// ─── Integration tests (DATABASE_URL-gated) ────────────────────────

// repoAppAnalysis builds a minimal valid analysis result for the given
// identifier — the same shape repoanalyze.synthesiseDraft produces.
func repoAppAnalysis(identifier string) *repoanalyze.Result {
	return &repoanalyze.Result{
		ManifestDraft: biuapp.Manifest{
			Name:        identifier,
			Version:     "1.2.3",
			Description: "test repo app",
			Author:      "octocat",
			Permissions: []string{"net.outbound"},
			Actions:     []biuapp.ActionSpec{},
			ManifestExt: biuapp.ManifestExt{
				Identifier: identifier,
				Title:      "Hello",
				Kind:       "webview",
				Category:   "utility",
				Views: []biuapp.ViewSpec{{
					ID:     "home",
					Route:  "/apps/" + identifier,
					Title:  "Hello",
					Layout: biuapp.LayoutWebView,
					URL:    "http://127.0.0.1:0/",
				}},
			},
		},
		EnvSchema: []repoanalyze.EnvField{
			{Name: "API_KEY", Secret: true},
			{Name: "FLAVOUR", Secret: false, Default: "vanilla", Optional: true},
		},
		RepoMeta: repoanalyze.RepoMeta{
			URL:           "https://github.com/octocat/hello",
			DefaultBranch: "main",
			LatestRef:     "v1.2.3",
			LatestSHA:     "deadbeef",
		},
	}
}

// TestCreateRepoApp_HappyPath covers the five-step flow: catalogue row
// with tier/repo_meta, app.published event, registry stub, install row.
func TestCreateRepoApp_HappyPath(t *testing.T) {
	pool := openDB(t)
	reg := biuapp.NewRegistry(biuapp.Deps{})
	in := New(pool, reg, nil)

	identifier := "gh-octocat-hello-" + uuid.NewString()[:8]
	userID := uuid.NewString()
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `DELETE FROM app_center.repo_builds WHERE install_id IN
			(SELECT id FROM app_center.installations WHERE identifier = $1)`, identifier)
		_, _ = pool.Exec(ctx, `DELETE FROM app_center.events WHERE scope = $1
			OR scope IN (SELECT 'install:' || id::text FROM app_center.installations WHERE identifier = $2)`,
			"app:app_"+identifier, identifier)
		_, _ = pool.Exec(ctx, `DELETE FROM app_center.installations WHERE identifier = $1`, identifier)
		_, _ = pool.Exec(ctx, `DELETE FROM app_center.apps WHERE identifier = $1`, identifier)
	})

	row, err := in.CreateRepoApp(context.Background(), RepoAppRequest{
		Analysis: repoAppAnalysis(identifier),
		RefType:  "release",
		Config:   map[string]any{"FLAVOUR": "chocolate"},
		UserID:   userID,
	})
	if err != nil {
		t.Fatalf("CreateRepoApp: %v", err)
	}
	if row.Identifier != identifier || row.Scope != "user" || row.ScopeID != userID {
		t.Errorf("unexpected install row: %+v", row)
	}
	if row.Config["FLAVOUR"] != "chocolate" {
		t.Errorf("config not persisted: %+v", row.Config)
	}
	if _, ok := reg.Get(identifier); !ok {
		t.Error("stub not registered in the in-memory registry")
	}

	// Catalogue row carries the repo columns.
	var tier, source, adapterSource string
	var repoMeta []byte
	if err := pool.QueryRow(context.Background(), `
		SELECT COALESCE(tier, ''), source, COALESCE(adapter_source, ''), repo_meta
		  FROM app_center.apps WHERE identifier = $1
	`, identifier).Scan(&tier, &source, &adapterSource, &repoMeta); err != nil {
		t.Fatalf("read apps row: %v", err)
	}
	if tier != "private" || source != "gh_private" || adapterSource != "auto" {
		t.Errorf("apps row tier/source/adapter_source = %q/%q/%q", tier, source, adapterSource)
	}
	if len(repoMeta) == 0 {
		t.Error("repo_meta not persisted")
	}

	// app.published event exists.
	var eventCount int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM app_center.events
		 WHERE scope = $1 AND event_type = 'app.published'
	`, "app:app_"+identifier).Scan(&eventCount); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if eventCount != 1 {
		t.Errorf("app.published events = %d, want 1", eventCount)
	}
}

// TestCreateRepoApp_RejectsSecret covers the D9 boundary end-to-end:
// nothing is written when a secret env field shows up in config.
func TestCreateRepoApp_RejectsSecret(t *testing.T) {
	pool := openDB(t)
	reg := biuapp.NewRegistry(biuapp.Deps{})
	in := New(pool, reg, nil)

	identifier := "gh-octocat-secret-" + uuid.NewString()[:8]
	_, err := in.CreateRepoApp(context.Background(), RepoAppRequest{
		Analysis: repoAppAnalysis(identifier),
		RefType:  "release",
		Config:   map[string]any{"API_KEY": "sk-live"},
		UserID:   uuid.NewString(),
	})
	if !errors.Is(err, ErrSecretConfigField) {
		t.Fatalf("want ErrSecretConfigField, got %v", err)
	}
	var n int
	_ = pool.QueryRow(context.Background(),
		`SELECT count(*) FROM app_center.apps WHERE identifier = $1`, identifier).Scan(&n)
	if n != 0 {
		t.Error("apps row must not be written on secret rejection")
	}
}

// TestQueueRedeploy covers redeploy queueing + build listing + the
// ownership fold (other user's install → ErrNotFound).
func TestQueueRedeploy(t *testing.T) {
	pool := openDB(t)
	reg := biuapp.NewRegistry(biuapp.Deps{})
	in := New(pool, reg, nil)

	identifier := "gh-octocat-redeploy-" + uuid.NewString()[:8]
	userID := uuid.NewString()
	row, err := in.CreateRepoApp(context.Background(), RepoAppRequest{
		Analysis: repoAppAnalysis(identifier),
		RefType:  "branch",
		UserID:   userID,
	})
	if err != nil {
		t.Fatalf("CreateRepoApp: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `DELETE FROM app_center.events WHERE scope = $1`, "install:"+row.ID)
		_, _ = pool.Exec(ctx, `DELETE FROM app_center.repo_builds WHERE install_id = $1`, row.ID)
		_, _ = pool.Exec(ctx, `DELETE FROM app_center.installations WHERE id = $1`, row.ID)
		_, _ = pool.Exec(ctx, `DELETE FROM app_center.events WHERE scope = $1`, "app:app_"+identifier)
		_, _ = pool.Exec(ctx, `DELETE FROM app_center.apps WHERE identifier = $1`, identifier)
	})

	// Ownership: another user must not see this install.
	if _, err := in.OwnedRepoInstall(context.Background(), row.ID, uuid.NewString()); !errors.Is(err, ErrNotFound) {
		t.Errorf("foreign owner: want ErrNotFound, got %v", err)
	}
	if _, err := in.OwnedRepoInstall(context.Background(), row.ID, userID); err != nil {
		t.Errorf("owner lookup: %v", err)
	}

	// No builds yet → empty status → runtime "stopped".
	status, err := in.LatestBuildStatus(context.Background(), row.ID)
	if err != nil || status != "" {
		t.Errorf("LatestBuildStatus = %q, %v; want empty", status, err)
	}

	queued, err := in.QueueRedeploy(context.Background(), row.ID, row.Identifier, userID)
	if err != nil {
		t.Fatalf("QueueRedeploy: %v", err)
	}
	if queued.BuildID == "" {
		t.Fatal("QueueRedeploy returned empty build id")
	}
	// The client hands ref/sha to the CLI (biu repo-app update --ref) —
	// they must ride the response.
	if queued.Ref != "v1.2.3" || queued.SHA != "deadbeef" {
		t.Errorf("queued ref/sha = %q/%q, want v1.2.3/deadbeef", queued.Ref, queued.SHA)
	}
	buildID := queued.BuildID

	builds, err := in.ListBuilds(context.Background(), row.ID, 20)
	if err != nil {
		t.Fatalf("ListBuilds: %v", err)
	}
	if len(builds) != 1 || builds[0].ID != buildID || builds[0].Status != "queued" {
		t.Errorf("builds = %+v", builds)
	}
	if builds[0].Ref != "v1.2.3" || builds[0].SHA != "deadbeef" {
		t.Errorf("build ref/sha = %q/%q, want v1.2.3/deadbeef", builds[0].Ref, builds[0].SHA)
	}

	status, err = in.LatestBuildStatus(context.Background(), row.ID)
	if err != nil || status != "queued" {
		t.Errorf("LatestBuildStatus = %q, %v; want queued", status, err)
	}

	// redeploy audit event exists (queued marker on app.upgraded).
	var eventCount int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM app_center.events
		 WHERE scope = $1 AND event_type = 'app.upgraded'
		   AND payload->>'action' = 'redeploy_queued'
	`, "install:"+row.ID).Scan(&eventCount); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if eventCount != 1 {
		t.Errorf("redeploy_queued events = %d, want 1", eventCount)
	}
}

// ─── Build completion (M2.3) + repo-app upgrade check (M2.2) ────────

// setupRepoAppWithBuild creates a repo app + install + one queued build
// and returns the install row / build / cleanup-registered identifiers.
func setupRepoAppWithBuild(t *testing.T, in *Installer, label string) (row *Installation, queued *QueuedRedeploy, userID string) {
	t.Helper()
	pool := in.Pool
	identifier := "gh-octocat-" + label + "-" + uuid.NewString()[:8]
	userID = uuid.NewString()
	var err error
	row, err = in.CreateRepoApp(context.Background(), RepoAppRequest{
		Analysis: repoAppAnalysis(identifier),
		RefType:  "release",
		UserID:   userID,
	})
	if err != nil {
		t.Fatalf("CreateRepoApp: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `DELETE FROM app_center.events WHERE scope = $1`, "install:"+row.ID)
		_, _ = pool.Exec(ctx, `DELETE FROM app_center.repo_builds WHERE install_id = $1`, row.ID)
		_, _ = pool.Exec(ctx, `DELETE FROM app_center.installations WHERE id = $1`, row.ID)
		_, _ = pool.Exec(ctx, `DELETE FROM app_center.events WHERE scope = $1`, "app:app_"+identifier)
		_, _ = pool.Exec(ctx, `DELETE FROM app_center.apps WHERE identifier = $1`, identifier)
	})
	queued, err = in.QueueRedeploy(context.Background(), row.ID, row.Identifier, userID)
	if err != nil {
		t.Fatalf("QueueRedeploy: %v", err)
	}
	return row, queued, userID
}

// repoMetaField reads one key out of the catalogue row's repo_meta.
func repoMetaField(t *testing.T, pool interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}, identifier, key string) string {
	t.Helper()
	var val string
	if err := pool.QueryRow(context.Background(), `
		SELECT COALESCE(repo_meta->>$2, '') FROM app_center.apps
		 WHERE identifier = $1 ORDER BY created_at DESC LIMIT 1
	`, identifier, key).Scan(&val); err != nil {
		t.Fatalf("read repo_meta.%s: %v", key, err)
	}
	return val
}

func countEvents(t *testing.T, pool *pgxpool.Pool, scope, action string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM app_center.events
		 WHERE scope = $1 AND event_type = 'app.upgraded' AND payload->>'action' = $2
	`, scope, action).Scan(&n); err != nil {
		t.Fatalf("count events: %v", err)
	}
	return n
}

// TestCompleteRepoBuild_Live covers the live path end-to-end: build row
// terminal + duration, repo_meta installed_* advanced with the banner
// cleared, audit event — all in one tx.
func TestCompleteRepoBuild_Live(t *testing.T) {
	pool := openDB(t)
	in := New(pool, biuapp.NewRegistry(biuapp.Deps{}), nil)
	row, queued, userID := setupRepoAppWithBuild(t, in, "complete")

	// CreateRepoApp must have pinned installed_* (poller diff base).
	if got := repoMetaField(t, pool, row.Identifier, "installed_ref"); got != "v1.2.3" {
		t.Errorf("installed_ref = %q, want v1.2.3", got)
	}
	// Simulate the poller having flagged an update.
	if _, err := pool.Exec(context.Background(), `
		UPDATE app_center.apps
		   SET repo_meta = repo_meta || '{"update_available": true}'::jsonb
		 WHERE identifier = $1
	`, row.Identifier); err != nil {
		t.Fatalf("flag update_available: %v", err)
	}

	err := in.CompleteRepoBuild(context.Background(), row, queued.BuildID, CompleteBuildRequest{
		Status:       "live",
		SHA:          "cafebabe",
		LogRef:       "/tmp/runner/build.log",
		CallerUserID: userID,
	})
	if err != nil {
		t.Fatalf("CompleteRepoBuild live: %v", err)
	}

	// Build row: terminal + duration + log_ref.
	var status, logRef string
	var durationMs *int
	if err := pool.QueryRow(context.Background(), `
		SELECT status, COALESCE(log_ref, ''), duration_ms
		  FROM app_center.repo_builds WHERE id = $1
	`, queued.BuildID).Scan(&status, &logRef, &durationMs); err != nil {
		t.Fatalf("read build: %v", err)
	}
	if status != "live" {
		t.Errorf("build status = %q, want live", status)
	}
	if logRef != "/tmp/runner/build.log" {
		t.Errorf("log_ref = %q", logRef)
	}
	if durationMs == nil {
		t.Error("duration_ms not stamped")
	}

	// repo_meta: installed pair advanced, banner cleared, poll reset.
	if got := repoMetaField(t, pool, row.Identifier, "installed_ref"); got != queued.Ref {
		t.Errorf("installed_ref = %q, want %q", got, queued.Ref)
	}
	if got := repoMetaField(t, pool, row.Identifier, "installed_sha"); got != "cafebabe" {
		t.Errorf("installed_sha = %q, want cafebabe", got)
	}
	if got := repoMetaField(t, pool, row.Identifier, "update_available"); got != "false" {
		t.Errorf("update_available = %q, want false", got)
	}
	var nextPoll time.Time
	if err := pool.QueryRow(context.Background(), `
		SELECT (repo_meta->>'next_poll_at')::timestamptz FROM app_center.apps
		 WHERE identifier = $1 ORDER BY created_at DESC LIMIT 1
	`, row.Identifier).Scan(&nextPoll); err != nil {
		t.Fatalf("read next_poll_at: %v", err)
	}
	if !nextPoll.After(time.Now()) {
		t.Errorf("next_poll_at = %v, want a future reset", nextPoll)
	}

	if n := countEvents(t, pool, "install:"+row.ID, "redeploy_completed"); n != 1 {
		t.Errorf("redeploy_completed events = %d, want 1", n)
	}

	// Repeat complete on a terminal build is refused.
	err = in.CompleteRepoBuild(context.Background(), row, queued.BuildID, CompleteBuildRequest{
		Status: "live", CallerUserID: userID,
	})
	if !errors.Is(err, ErrBuildAlreadyFinal) {
		t.Errorf("duplicate complete: want ErrBuildAlreadyFinal, got %v", err)
	}
}

// TestCompleteRepoBuild_Failed covers the failure path: build goes
// failed, repo_meta keeps the old installed pair (runner rolled back).
func TestCompleteRepoBuild_Failed(t *testing.T) {
	pool := openDB(t)
	in := New(pool, biuapp.NewRegistry(biuapp.Deps{}), nil)
	row, queued, userID := setupRepoAppWithBuild(t, in, "failed")

	err := in.CompleteRepoBuild(context.Background(), row, queued.BuildID, CompleteBuildRequest{
		Status:       "failed",
		LogRef:       "/tmp/runner/err.log",
		CallerUserID: userID,
	})
	if err != nil {
		t.Fatalf("CompleteRepoBuild failed: %v", err)
	}

	var status string
	if err := pool.QueryRow(context.Background(),
		`SELECT status FROM app_center.repo_builds WHERE id = $1`, queued.BuildID,
	).Scan(&status); err != nil {
		t.Fatalf("read build: %v", err)
	}
	if status != "failed" {
		t.Errorf("build status = %q, want failed", status)
	}
	// installed_ref untouched — the runner is still on the old ref.
	if got := repoMetaField(t, pool, row.Identifier, "installed_ref"); got != "v1.2.3" {
		t.Errorf("installed_ref = %q, want unchanged v1.2.3", got)
	}
	if n := countEvents(t, pool, "install:"+row.ID, "redeploy_failed"); n != 1 {
		t.Errorf("redeploy_failed events = %d, want 1", n)
	}
}

// TestCompleteRepoBuild_Guards covers the folds: another install's
// build → ErrNotFound, invalid status → ErrInvalidBuildStatus.
func TestCompleteRepoBuild_Guards(t *testing.T) {
	pool := openDB(t)
	in := New(pool, biuapp.NewRegistry(biuapp.Deps{}), nil)
	row, queued, userID := setupRepoAppWithBuild(t, in, "guards")
	other, _, _ := setupRepoAppWithBuild(t, in, "guards2")

	if err := in.CompleteRepoBuild(context.Background(), other, queued.BuildID, CompleteBuildRequest{
		Status: "live", CallerUserID: userID,
	}); !errors.Is(err, ErrNotFound) {
		t.Errorf("foreign build: want ErrNotFound, got %v", err)
	}
	if err := in.CompleteRepoBuild(context.Background(), row, queued.BuildID, CompleteBuildRequest{
		Status: "building", CallerUserID: userID,
	}); !errors.Is(err, ErrInvalidBuildStatus) {
		t.Errorf("invalid status: want ErrInvalidBuildStatus, got %v", err)
	}
}

// TestCheckUpgradable_RepoApp covers the gh_* dispatch (M2.2): versions
// come from repo_meta, the contract matches the registry path, and a
// completed upgrade flips Available back to false.
func TestCheckUpgradable_RepoApp(t *testing.T) {
	pool := openDB(t)
	in := New(pool, biuapp.NewRegistry(biuapp.Deps{}), nil)
	row, queued, userID := setupRepoAppWithBuild(t, in, "upgradable")

	// Before the poller flags anything: no update.
	st, err := in.CheckUpgradable(context.Background(), row.ID)
	if err != nil {
		t.Fatalf("CheckUpgradable: %v", err)
	}
	if st.Available {
		t.Errorf("Available = true before any poll, want false: %+v", st)
	}
	if st.CurrentVersion != "v1.2.3" {
		t.Errorf("CurrentVersion = %q, want installed ref v1.2.3", st.CurrentVersion)
	}

	// Poller flags v2.0.0.
	if _, err := pool.Exec(context.Background(), `
		UPDATE app_center.apps
		   SET repo_meta = repo_meta ||
		       '{"latest_ref":"v2.0.0","latest_sha":"cafe","update_available":true}'::jsonb
		 WHERE identifier = $1
	`, row.Identifier); err != nil {
		t.Fatalf("flag update: %v", err)
	}
	st, err = in.CheckUpgradable(context.Background(), row.ID)
	if err != nil {
		t.Fatalf("CheckUpgradable flagged: %v", err)
	}
	if !st.Available {
		t.Errorf("Available = false after poller flag, want true: %+v", st)
	}
	if st.CurrentVersion != "v1.2.3" || st.TargetVersion != "v2.0.0" {
		t.Errorf("current/target = %q/%q, want v1.2.3/v2.0.0", st.CurrentVersion, st.TargetVersion)
	}
	if st.RequiresApproval {
		t.Error("repo-app upgrade must never require approval (no perms diff)")
	}
	if len(st.PermsDiff.Added) != 0 || len(st.PermsDiff.Removed) != 0 || len(st.PermsDiff.Unchanged) != 0 {
		t.Errorf("perms_diff must be empty, got %+v", st.PermsDiff)
	}

	// After a live complete the banner clears.
	if err := in.CompleteRepoBuild(context.Background(), row, queued.BuildID, CompleteBuildRequest{
		Status: "live", SHA: "cafe", CallerUserID: userID,
	}); err != nil {
		t.Fatalf("complete: %v", err)
	}
	st, err = in.CheckUpgradable(context.Background(), row.ID)
	if err != nil {
		t.Fatalf("CheckUpgradable after complete: %v", err)
	}
	if st.Available {
		t.Errorf("Available = true after live complete, want false: %+v", st)
	}
	if st.CurrentVersion != queued.Ref {
		t.Errorf("CurrentVersion = %q, want %q", st.CurrentVersion, queued.Ref)
	}
}
