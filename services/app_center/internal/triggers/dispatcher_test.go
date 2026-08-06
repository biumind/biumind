package triggers

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/biumind/biumind/packages/go-sdk/biu/biuapp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func testLogger(t *testing.T) *slog.Logger {
	return slog.New(slog.NewTextHandler(testLogWriter{t: t}, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

type testLogWriter struct{ t *testing.T }

func (w testLogWriter) Write(b []byte) (int, error) {
	w.t.Logf("%s", b)
	return len(b), nil
}

func openDispatcherDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping dispatcher integration tests")
	}
	p, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(p.Close)
	return p
}

// ─── App stubs ─────────────────────────────────────────────

type tickApp struct {
	manifest biuapp.Manifest
	count    int32
	mu       sync.Mutex
	failOn   string
}

func (a *tickApp) Manifest() biuapp.Manifest                     { return a.manifest }
func (a *tickApp) Init(ctx context.Context, _ biuapp.Deps) error { return nil }
func (a *tickApp) Invoke(_ context.Context, action string, _ json.RawMessage) (any, error) {
	atomic.AddInt32(&a.count, 1)
	a.mu.Lock()
	defer a.mu.Unlock()
	if action == a.failOn {
		return nil, errors.New("forced fail")
	}
	return map[string]any{"action": action}, nil
}

func newTickApp(slug string) *tickApp {
	return &tickApp{manifest: biuapp.Manifest{
		Name:        slug,
		Version:     "0.1.0",
		Description: "dispatcher test app",
		Actions:     []biuapp.ActionSpec{{Name: "tick", Risk: biuapp.RiskLow}},
	}}
}

// seedJob inserts an installation + a cron scheduler_jobs row with
// next_run already past so the dispatcher will claim it on the first
// tick.
func seedJob(t *testing.T, pool *pgxpool.Pool, slug string) (uuid.UUID, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	installID := uuid.New()
	jobID := uuid.New()

	if _, err := pool.Exec(ctx, `
		INSERT INTO app_center.installations
			(id, scope, scope_id, app_id, identifier, version,
			 enabled, permissions_granted, config, forced)
		VALUES ($1, 'user', $2, $3, $4, '0.1.0',
			true, '{}', '{}', false)
	`, installID, uuid.New(), "app_"+slug, slug); err != nil {
		t.Fatalf("seed install: %v", err)
	}

	past := time.Now().Add(-1 * time.Minute).UTC()
	if _, err := pool.Exec(ctx, `
		INSERT INTO app_center.scheduler_jobs
			(id, install_id, identifier, name, kind, cron_expr,
			 action, input, next_run, enabled)
		VALUES ($1, $2, $3, 'tick', 'cron', '5 * * * *',
		        'tick', '{}', $4, true)
	`, jobID, installID, slug, past); err != nil {
		t.Fatalf("seed job: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM app_center.installations WHERE id = $1`, installID)
	})
	return installID, jobID
}

// ─── Tests ──────────────────────────────────────────────────

func TestDispatcher_FiresDueJob(t *testing.T) {
	pool := openDispatcherDB(t)
	app := newTickApp("disp-fire")
	reg := biuapp.NewRegistry(biuapp.Deps{})
	if err := reg.Register(context.Background(), app); err != nil {
		t.Fatal(err)
	}
	_, jobID := seedJob(t, pool, "disp-fire")

	d := &Dispatcher{Pool: pool, Registry: reg, BatchSize: 10}
	d.tick(context.Background())
	d.wg.Wait() // wait for fire goroutines

	if got := atomic.LoadInt32(&app.count); got != 1 {
		t.Errorf("Invoke count = %d, want 1", got)
	}

	// Verify job state advanced (next_run pushed forward, last_status=ok).
	var (
		nextRun    time.Time
		lastStatus string
	)
	if err := pool.QueryRow(context.Background(), `
		SELECT next_run, last_status FROM app_center.scheduler_jobs WHERE id = $1
	`, jobID).Scan(&nextRun, &lastStatus); err != nil {
		t.Fatal(err)
	}
	if lastStatus != "ok" {
		t.Errorf("last_status = %q, want ok", lastStatus)
	}
	if !nextRun.After(time.Now()) {
		t.Errorf("next_run = %v, want future", nextRun)
	}
}

func TestDispatcher_RecordsErrorOnAppFailure(t *testing.T) {
	pool := openDispatcherDB(t)
	app := newTickApp("disp-err")
	app.failOn = "tick"
	reg := biuapp.NewRegistry(biuapp.Deps{})
	_ = reg.Register(context.Background(), app)
	_, jobID := seedJob(t, pool, "disp-err")

	d := &Dispatcher{Pool: pool, Registry: reg, Logger: testLogger(t)}
	d.tick(context.Background())
	d.wg.Wait()

	var (
		lastStatus string
		consec     int
	)
	if err := pool.QueryRow(context.Background(), `
		SELECT last_status, consecutive_failures FROM app_center.scheduler_jobs WHERE id = $1
	`, jobID).Scan(&lastStatus, &consec); err != nil {
		t.Fatal(err)
	}
	if lastStatus != "error" {
		t.Errorf("last_status = %q, want error", lastStatus)
	}
	if consec != 1 {
		t.Errorf("consecutive_failures = %d, want 1", consec)
	}
}

func TestDispatcher_SkipsLockedJob(t *testing.T) {
	pool := openDispatcherDB(t)
	app := newTickApp("disp-lock")
	reg := biuapp.NewRegistry(biuapp.Deps{})
	_ = reg.Register(context.Background(), app)
	_, jobID := seedJob(t, pool, "disp-lock")

	// Manually stamp locked_until far into the future — simulates
	// another replica having claimed this row.
	if _, err := pool.Exec(context.Background(),
		`UPDATE app_center.scheduler_jobs SET locked_until = now() + interval '1 hour' WHERE id = $1`,
		jobID); err != nil {
		t.Fatal(err)
	}

	d := &Dispatcher{Pool: pool, Registry: reg}
	d.tick(context.Background())
	d.wg.Wait()

	if got := atomic.LoadInt32(&app.count); got != 0 {
		t.Errorf("locked job should not fire, got count = %d", got)
	}
}

func TestDispatcher_AuditAndEvents(t *testing.T) {
	pool := openDispatcherDB(t)
	app := newTickApp("disp-audit")
	reg := biuapp.NewRegistry(biuapp.Deps{})
	_ = reg.Register(context.Background(), app)
	installID, _ := seedJob(t, pool, "disp-audit")

	d := &Dispatcher{Pool: pool, Registry: reg}
	d.tick(context.Background())
	d.wg.Wait()

	// Invocations row.
	var invCount int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM app_center.invocations
		 WHERE install_id = $1 AND caller = 'scheduler'
	`, installID).Scan(&invCount); err != nil {
		t.Fatal(err)
	}
	if invCount != 1 {
		t.Errorf("invocations count = %d, want 1", invCount)
	}

	// Event row.
	var evCount int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM app_center.events
		 WHERE scope = $1 AND event_type = 'app.trigger_fired'
	`, "install:"+installID.String()).Scan(&evCount); err != nil {
		t.Fatal(err)
	}
	if evCount != 1 {
		t.Errorf("events count = %d, want 1", evCount)
	}
}
