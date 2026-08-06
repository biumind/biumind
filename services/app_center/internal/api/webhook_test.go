package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/biumind/biumind/packages/go-sdk/biu/biuapp"
	"github.com/biumind/biumind/services/app_center/internal/triggers"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Webhook integration tests need a DB. Skip when DATABASE_URL is
// unset; CI matrix runs the integration leg after `task migrate up`.

func openWebhookDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping webhook integration tests")
	}
	p, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(p.Close)
	for _, table := range []string{
		"app_center.installations",
		"app_center.scheduler_jobs",
	} {
		var exists bool
		if err := p.QueryRow(context.Background(),
			`SELECT EXISTS (SELECT 1 FROM information_schema.tables
			    WHERE table_schema = split_part($1, '.', 1)
			      AND table_name = split_part($1, '.', 2))`,
			table,
		).Scan(&exists); err != nil {
			t.Fatalf("table check %s: %v", table, err)
		}
		if !exists {
			t.Skipf("%s missing; apply services/app_center/migrations", table)
		}
	}
	return p
}

// hookCounter implements biuapp.App + LifecycleHooks/TriggerHandler
// so we can verify webhook dispatch hit OnTrigger.
type hookCounter struct {
	manifest biuapp.Manifest
	called   int
	lastEv   biuapp.TriggerEvent
}

func (h *hookCounter) Manifest() biuapp.Manifest                      { return h.manifest }
func (h *hookCounter) Init(ctx context.Context, _ biuapp.Deps) error  { return nil }
func (h *hookCounter) Invoke(_ context.Context, action string, in json.RawMessage) (any, error) {
	return map[string]any{"action": action}, nil
}
func (h *hookCounter) OnTrigger(_ context.Context, ev biuapp.TriggerEvent) error {
	h.called++
	h.lastEv = ev
	return nil
}

func newHookApp(slug string) *hookCounter {
	return &hookCounter{manifest: biuapp.Manifest{
		Name:        slug,
		Version:     "0.1.0",
		Description: "webhook test app",
		Actions:     []biuapp.ActionSpec{{Name: "on_callback", Risk: biuapp.RiskLow}},
	}}
}

func seedWebhookInstall(t *testing.T, pool *pgxpool.Pool, slug, path string) (uuid.UUID, []byte) {
	t.Helper()
	ctx := context.Background()
	installID := uuid.New()
	secret, err := triggers.Generate()
	if err != nil {
		t.Fatal(err)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO app_center.installations
			(id, scope, scope_id, app_id, identifier, version,
			 enabled, permissions_granted, config, forced, webhook_secret)
		VALUES ($1, 'user', $2, $3, $4, '0.1.0',
			true, '{}', '{}', false, $5)
	`, installID, uuid.New(), "app_"+slug, slug, secret); err != nil {
		t.Fatalf("seed install: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO app_center.scheduler_jobs
			(install_id, identifier, name, kind, webhook_path, action, enabled)
		VALUES ($1, $2, 'cb', 'webhook', $3, 'on_callback', true)
	`, installID, slug, path); err != nil {
		t.Fatalf("seed job: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM app_center.installations WHERE id = $1`, installID)
	})
	return installID, secret
}

// servWebhook builds a minimal Server with webhook plumbing wired and
// the app registered. Returns the handler so tests can call ServeHTTP
// directly.
func servWebhook(t *testing.T, pool *pgxpool.Pool, app biuapp.App) http.Handler {
	t.Helper()
	reg := biuapp.NewRegistry(biuapp.Deps{})
	if err := reg.Register(context.Background(), app); err != nil {
		t.Fatal(err)
	}
	srv := &Server{}
	srv.SetPool(pool)
	srv.SetBiuappRegistry(reg)
	mux := http.NewServeMux()
	srv.MountWebhooks(mux)
	return mux
}

// ─── Tests ──────────────────────────────────────────────────

func TestWebhook_HappyPath(t *testing.T) {
	pool := openWebhookDB(t)
	app := newHookApp("webhook-happy")
	installID, secret := seedWebhookInstall(t, pool, "webhook-happy", "/cb")
	srv := servWebhook(t, pool, app)

	body := []byte(`{"event":"hello"}`)
	sig := triggers.Sign(secret, body)

	req := httptest.NewRequest(http.MethodPost,
		"/webhooks/app_center/"+installID.String()+"/cb",
		bytes.NewReader(body))
	req.Header.Set("X-BiuMind-App-Signature", sig)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		body, _ := io.ReadAll(rec.Result().Body)
		t.Fatalf("status = %d, body = %s", rec.Code, body)
	}
	if app.called != 1 {
		t.Errorf("OnTrigger called %d times, want 1", app.called)
	}
	if app.lastEv.TriggerKind != biuapp.TriggerWebhook {
		t.Errorf("trigger kind = %v", app.lastEv.TriggerKind)
	}
	if app.lastEv.Action != "on_callback" {
		t.Errorf("action = %q", app.lastEv.Action)
	}
}

func TestWebhook_InvalidSignature(t *testing.T) {
	pool := openWebhookDB(t)
	app := newHookApp("webhook-bad-sig")
	installID, _ := seedWebhookInstall(t, pool, "webhook-bad-sig", "/cb")
	srv := servWebhook(t, pool, app)

	req := httptest.NewRequest(http.MethodPost,
		"/webhooks/app_center/"+installID.String()+"/cb",
		bytes.NewReader([]byte(`{}`)))
	req.Header.Set("X-BiuMind-App-Signature", "0000000000000000")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
	if app.called != 0 {
		t.Errorf("OnTrigger should NOT have been called on bad sig, got %d", app.called)
	}
}

func TestWebhook_MissingSignature(t *testing.T) {
	pool := openWebhookDB(t)
	app := newHookApp("webhook-no-sig")
	installID, _ := seedWebhookInstall(t, pool, "webhook-no-sig", "/cb")
	srv := servWebhook(t, pool, app)

	req := httptest.NewRequest(http.MethodPost,
		"/webhooks/app_center/"+installID.String()+"/cb",
		bytes.NewReader([]byte(`{}`)))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestWebhook_UnknownInstall(t *testing.T) {
	pool := openWebhookDB(t)
	app := newHookApp("webhook-unknown")
	srv := servWebhook(t, pool, app)

	req := httptest.NewRequest(http.MethodPost,
		"/webhooks/app_center/"+uuid.NewString()+"/cb",
		bytes.NewReader([]byte(`{}`)))
	req.Header.Set("X-BiuMind-App-Signature", "deadbeef")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestWebhook_UnmountedPath(t *testing.T) {
	pool := openWebhookDB(t)
	app := newHookApp("webhook-other-path")
	installID, secret := seedWebhookInstall(t, pool, "webhook-other-path", "/cb")
	srv := servWebhook(t, pool, app)

	body := []byte(`{}`)
	sig := triggers.Sign(secret, body)
	req := httptest.NewRequest(http.MethodPost,
		"/webhooks/app_center/"+installID.String()+"/wrong-path",
		bytes.NewReader(body))
	req.Header.Set("X-BiuMind-App-Signature", sig)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestWebhook_EmptyBodyRejected(t *testing.T) {
	pool := openWebhookDB(t)
	app := newHookApp("webhook-empty")
	installID, secret := seedWebhookInstall(t, pool, "webhook-empty", "/cb")
	srv := servWebhook(t, pool, app)

	sig := triggers.Sign(secret, nil)
	req := httptest.NewRequest(http.MethodPost,
		"/webhooks/app_center/"+installID.String()+"/cb",
		bytes.NewReader(nil))
	req.Header.Set("X-BiuMind-App-Signature", sig)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}
