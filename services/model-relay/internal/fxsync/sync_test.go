package fxsync

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/biumind/biumind/services/model-relay/internal/registry"
)

func openDB(t *testing.T) (*pgxpool.Pool, *registry.Store) {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping fxsync integration tests")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pgx: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool, registry.NewStore(pool)
}

// stubUpstream serves a configurable JSON body — covers happy path +
// the various failure modes.
func stubUpstream(t *testing.T, body string, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status > 0 {
			w.WriteHeader(status)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

const sampleOK = `{
  "result": "success",
  "base_code": "USD",
  "time_last_update_utc": "Sat, 31 May 2026 00:00:01 +0000",
  "rates": {"USD": 1, "CNY": 7.25, "EUR": 0.92}
}`

func TestSync_HappyPath(t *testing.T) {
	pool, store := openDB(t)
	srv := stubUpstream(t, sampleOK, 200)

	syncer := &Syncer{Store: store, URL: srv.URL}
	ctx := context.Background()
	if err := syncer.SyncOnce(ctx); err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}

	// Verify USD→CNY rate now matches upstream
	usdCny, err := store.FxRates.GetRate(ctx, registry.CurrencyUSD, registry.CurrencyCNY)
	if err != nil {
		t.Fatalf("get rate: %v", err)
	}
	if usdCny != 7.25 {
		t.Fatalf("USD→CNY = %v, want 7.25", usdCny)
	}

	// Reverse rate is computed as 1/rate
	cnyUsd, _ := store.FxRates.GetRate(ctx, registry.CurrencyCNY, registry.CurrencyUSD)
	expected := 1.0 / 7.25
	if cnyUsd < expected*0.999 || cnyUsd > expected*1.001 {
		t.Fatalf("CNY→USD = %v, want ~%v", cnyUsd, expected)
	}

	// source should now be 'cron'
	rows, _ := store.FxRates.List(ctx)
	for _, r := range rows {
		if r.FromCurrency == registry.CurrencyUSD && r.ToCurrency == registry.CurrencyCNY {
			if r.Source != "cron" {
				t.Errorf("USD→CNY source=%q, want cron", r.Source)
			}
		}
	}

	// Restore seed value so other tests aren't surprised
	t.Cleanup(func() {
		_, _ = store.FxRates.Upsert(context.Background(), registry.FxRateUpsert{
			FromCurrency: registry.CurrencyUSD, ToCurrency: registry.CurrencyCNY,
			Rate: 7.20, Source: "manual",
		})
		_, _ = store.FxRates.Upsert(context.Background(), registry.FxRateUpsert{
			FromCurrency: registry.CurrencyCNY, ToCurrency: registry.CurrencyUSD,
			Rate: 0.138889, Source: "manual",
		})
	})
	_ = pool
}

func TestSync_UpstreamHTTPError(t *testing.T) {
	_, store := openDB(t)
	srv := stubUpstream(t, `{"error":"down"}`, 503)
	syncer := &Syncer{Store: store, URL: srv.URL}

	err := syncer.SyncOnce(context.Background())
	if err == nil {
		t.Fatalf("expected error on 503")
	}
	// Existing rate should be untouched
	r, _ := store.FxRates.GetRate(context.Background(), registry.CurrencyUSD, registry.CurrencyCNY)
	if r == 7.25 {
		t.Fatalf("rate was overwritten on failure: %v", r)
	}
}

func TestSync_UpstreamErrorBody(t *testing.T) {
	_, store := openDB(t)
	// 200 OK but result != success — open.er-api uses this for "missing key" type errors
	srv := stubUpstream(t, `{"result":"error","error-type":"unsupported-code"}`, 200)
	syncer := &Syncer{Store: store, URL: srv.URL}

	err := syncer.SyncOnce(context.Background())
	if err == nil || !contains(err.Error(), "upstream not ok") {
		t.Fatalf("expected 'upstream not ok' error, got %v", err)
	}
}

func TestSync_ParseError(t *testing.T) {
	_, store := openDB(t)
	srv := stubUpstream(t, `<html>not json</html>`, 200)
	syncer := &Syncer{Store: store, URL: srv.URL}

	err := syncer.SyncOnce(context.Background())
	if err == nil {
		t.Fatalf("expected parse error")
	}
}

func TestSync_MissingCNYRate(t *testing.T) {
	_, store := openDB(t)
	srv := stubUpstream(t,
		`{"result":"success","base_code":"USD","rates":{"EUR":0.9}}`, 200)
	syncer := &Syncer{Store: store, URL: srv.URL}

	err := syncer.SyncOnce(context.Background())
	if err == nil || !contains(err.Error(), "CNY rate") {
		t.Fatalf("expected missing CNY error, got %v", err)
	}
}

func TestSync_WrongBase(t *testing.T) {
	_, store := openDB(t)
	srv := stubUpstream(t,
		`{"result":"success","base_code":"EUR","rates":{"CNY":7.5}}`, 200)
	syncer := &Syncer{Store: store, URL: srv.URL}

	err := syncer.SyncOnce(context.Background())
	if err == nil || !contains(err.Error(), "expected base USD") {
		t.Fatalf("expected base mismatch, got %v", err)
	}
}

func TestSync_NetworkError(t *testing.T) {
	_, store := openDB(t)
	syncer := &Syncer{
		Store: store,
		URL:   "http://127.0.0.1:1", // unreachable port
		Client: &http.Client{Timeout: 200 * time.Millisecond},
	}
	err := syncer.SyncOnce(context.Background())
	if err == nil {
		t.Fatalf("expected network error")
	}
}

func TestRunCron_FirstDelayThenInterval(t *testing.T) {
	_, store := openDB(t)
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(sampleOK))
	}))
	defer srv.Close()

	syncer := &Syncer{Store: store, URL: srv.URL}
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()

	syncer.RunCron(ctx, 100*time.Millisecond, 50*time.Millisecond)

	// Within 250ms after a 50ms first delay + 100ms intervals: ≈3 hits.
	// Tolerate ±1 to keep CI flake-resistant.
	if hits < 2 || hits > 4 {
		t.Fatalf("expected ~3 hits in 250ms, got %d", hits)
	}

	// Restore seed
	_, _ = store.FxRates.Upsert(context.Background(), registry.FxRateUpsert{
		FromCurrency: registry.CurrencyUSD, ToCurrency: registry.CurrencyCNY,
		Rate: 7.20, Source: "manual",
	})
}

func TestSync_ContextCancelled(t *testing.T) {
	_, store := openDB(t)
	syncer := &Syncer{Store: store, URL: "http://127.0.0.1:1"}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := syncer.SyncOnce(ctx)
	if err == nil {
		t.Fatalf("expected error from cancelled ctx")
	}
	// errors.Is(ctx.Err(), context.Canceled) inner detail is fine; we
	// just want the call to return promptly.
	_ = errors.Is
}

// tiny substring helper avoids stdlib strings import for one call.
func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || hasInfix(s, sub))
}

func hasInfix(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
