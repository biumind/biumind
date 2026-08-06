// Outbox poller — Postgres integration test.
//
// Standard pattern across BiuMind: skip when DATABASE_URL_TEST is
// unset so the unit suite stays green offline; CI sets the env var
// against an ephemeral pg container.

package outbox

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func openTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("DATABASE_URL_TEST")
	if url == "" {
		t.Skip("DATABASE_URL_TEST unset; skipping postgres integration test")
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	// Smoke check + create the events table if missing. We don't
	// run goose here; tests that need the full migration use the
	// dedicated install_test.go fixtures. For the poller we only need
	// the events table.
	if _, err := pool.Exec(t.Context(), `
		CREATE SCHEMA IF NOT EXISTS app_center;
		CREATE TABLE IF NOT EXISTS app_center.events (
			id           bigserial PRIMARY KEY,
			scope        text NOT NULL,
			actor_type   text NOT NULL,
			actor_id     text NOT NULL DEFAULT '',
			event_type   text NOT NULL,
			payload      jsonb NOT NULL DEFAULT '{}'::jsonb,
			schema_ver   int NOT NULL DEFAULT 1,
			created_at   timestamptz NOT NULL DEFAULT now(),
			published_at timestamptz
		);
		TRUNCATE app_center.events RESTART IDENTITY;
	`); err != nil {
		t.Fatal(err)
	}
	return pool
}

func TestPoller_DrainsToPublisher(t *testing.T) {
	pool := openTestDB(t)
	mem := &Memory{}
	p := &Poller{Pool: pool, Publisher: mem, Batch: 50, Interval: 100 * time.Millisecond}

	// Seed three events covering three scope kinds.
	_, err := pool.Exec(t.Context(), `
		INSERT INTO app_center.events (scope, actor_type, event_type, payload) VALUES
		  ('install:abc',     'user',  'app.installed',          '{"v":1}'::jsonb),
		  ('user:user-1',     'user',  'sidebar.layout_changed', '{"version":2}'::jsonb),
		  ('app:app_rss',     'system','app.published',          '{"identifier":"rss"}'::jsonb);
	`)
	if err != nil {
		t.Fatal(err)
	}

	// One scan should drain all three; we run processBatch directly so
	// the test doesn't have to wait for a tick.
	n, err := p.processBatch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("processed=%d, want 3", n)
	}
	if len(mem.Events) != 3 {
		t.Fatalf("captured=%d, want 3", len(mem.Events))
	}

	wantTopics := map[string]bool{
		"app:install:abc":     false,
		"sidebar:user:user-1": false,
		"app:catalog:app_rss": false,
	}
	for _, e := range mem.Events {
		if _, ok := wantTopics[e.Topic]; !ok {
			t.Errorf("unexpected topic: %s", e.Topic)
			continue
		}
		wantTopics[e.Topic] = true
	}
	for k, seen := range wantTopics {
		if !seen {
			t.Errorf("missing topic: %s", k)
		}
	}

	// All rows must be stamped published_at.
	var unpublished int
	if err := pool.QueryRow(t.Context(),
		`SELECT count(*) FROM app_center.events WHERE published_at IS NULL`,
	).Scan(&unpublished); err != nil {
		t.Fatal(err)
	}
	if unpublished != 0 {
		t.Errorf("unpublished rows after drain: %d", unpublished)
	}
}

func TestPoller_PublishFailureLeavesRowUnpublished(t *testing.T) {
	pool := openTestDB(t)
	failer := &failingPublisher{err: errors.New("upstream down")}
	p := &Poller{Pool: pool, Publisher: failer, Batch: 50}

	_, err := pool.Exec(t.Context(), `
		INSERT INTO app_center.events (scope, actor_type, event_type, payload)
		VALUES ('install:abc', 'user', 'app.installed', '{}'::jsonb);
	`)
	if err != nil {
		t.Fatal(err)
	}

	n, err := p.processBatch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("expected 0 processed on publish failure, got %d", n)
	}

	var unpublished int
	if err := pool.QueryRow(t.Context(),
		`SELECT count(*) FROM app_center.events WHERE published_at IS NULL`,
	).Scan(&unpublished); err != nil {
		t.Fatal(err)
	}
	if unpublished != 1 {
		t.Errorf("expected row to remain unpublished, got %d", unpublished)
	}
}

func TestPoller_BadPayloadMarkedToAvoidLoop(t *testing.T) {
	pool := openTestDB(t)
	mem := &Memory{}
	p := &Poller{Pool: pool, Publisher: mem, Batch: 50}

	// Pgxpool encodes invalid jsonb at write time; we bypass by
	// inserting via text cast that accepts the bytes literally.
	// (A truly malformed jsonb can't sit in the table — but the
	// poller's defensive marking still matters; we exercise it via
	// a payload that's valid jsonb but not an object, which would
	// fail json.Unmarshal into map[string]any.)
	_, err := pool.Exec(t.Context(), `
		INSERT INTO app_center.events (scope, actor_type, event_type, payload)
		VALUES ('install:abc', 'user', 'app.installed', '"not-an-object"'::jsonb);
	`)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := p.processBatch(t.Context()); err != nil {
		t.Fatal(err)
	}
	var unpublished int
	if err := pool.QueryRow(t.Context(),
		`SELECT count(*) FROM app_center.events WHERE published_at IS NULL`,
	).Scan(&unpublished); err != nil {
		t.Fatal(err)
	}
	if unpublished != 0 {
		t.Errorf("bad-payload row should be marked published to avoid loop, got %d unpublished", unpublished)
	}
}

func TestPoller_RunReturnsOnContextCancel(t *testing.T) {
	pool := openTestDB(t)
	p := &Poller{Pool: pool, Publisher: Noop{}, Interval: 10 * time.Millisecond}

	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	err := p.Run(ctx)
	if err == nil {
		t.Error("expected ctx error")
	}
}

type failingPublisher struct{ err error }

func (f *failingPublisher) Publish(_ context.Context, _, _ string, _ map[string]any) error {
	return f.err
}
