package installs

import (
	"context"
	"errors"
	"testing"

	"github.com/biumind/biumind/packages/go-sdk/biu/biuapp"
	"github.com/biumind/biumind/services/app_center/internal/repoanalyze"
	"github.com/google/uuid"
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

	buildID, err := in.QueueRedeploy(context.Background(), row.ID, row.Identifier, userID)
	if err != nil {
		t.Fatalf("QueueRedeploy: %v", err)
	}
	if buildID == "" {
		t.Fatal("QueueRedeploy returned empty build id")
	}

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
