// Package fxsync pulls USD↔CNY fx rates from a public source on a
// daily cron. Without this, admins have to remember to update fx_rates
// every week — runbook §2.6's stale banner exists precisely because
// they forget.
//
// Source default: open.er-api.com — free, no auth, hourly upstream
// refresh. Override via MODEL_RELAY_FX_SYNC_URL for private / mirror /
// alternative provider (ECB / Stripe etc., as long as the JSON shape
// matches: { rates: {CNY: number, ...} }).
//
// Frequency: once a day. The upstream itself only refreshes daily, so
// pulling more often is wasted RTT.
//
// admin手填 vs cron: cron覆盖admin. The semantics are "cron = single
// source of truth; admin manual edits are temporary overrides until
// next sync". Set MODEL_RELAY_FX_SYNC_DISABLED=1 to disable cron and
// leave manual control to admin.
//
// Failure mode: log + metric, never touches existing rows. Stale banner
// keeps surfacing the issue if cron has been stuck for >14d.

package fxsync

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/biumind/biumind/packages/go-sdk/biu/metrics"
	"github.com/biumind/biumind/services/model-relay/internal/registry"
)

// DefaultURL is open.er-api.com's free endpoint with USD as base.
const DefaultURL = "https://open.er-api.com/v6/latest/USD"

// upstreamResponse mirrors the open.er-api.com schema. Only the bits
// we actually consume.
//
//	{
//	  "result": "success",
//	  "time_last_update_utc": "Sat, 31 May 2026 00:00:01 +0000",
//	  "base_code": "USD",
//	  "rates": { "USD": 1, "CNY": 7.198, ... }
//	}
type upstreamResponse struct {
	Result   string             `json:"result"`
	BaseCode string             `json:"base_code"`
	UpdateAt string             `json:"time_last_update_utc"`
	Rates    map[string]float64 `json:"rates"`
}

// Syncer pulls fx rates from URL and upserts USD↔CNY into the store.
// One per process; method calls are concurrency-safe (the underlying
// repo handles its own locking).
type Syncer struct {
	Store  *registry.Store
	Client *http.Client // nil → 10s timeout default
	URL    string       // empty → DefaultURL
	Logger *slog.Logger // nil → slog.Default()
}

func (s *Syncer) httpClient() *http.Client {
	if s.Client != nil {
		return s.Client
	}
	return &http.Client{Timeout: 10 * time.Second}
}

func (s *Syncer) url() string {
	if s.URL != "" {
		return s.URL
	}
	return DefaultURL
}

func (s *Syncer) logger() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

// SyncOnce pulls upstream and upserts:
//   - USD → CNY at the reported rate
//   - CNY → USD at 1/rate (round-trip preservation)
//
// USD/USD and CNY/CNY self-reflexive rows are seeded as 1.0 in
// 00003_seed.sql and never touched here.
//
// Returns nil on success even if some upserts hit transient errors —
// individual failures are logged + counted; cron pressing on is more
// useful than aborting the whole run.
func (s *Syncer) SyncOnce(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", s.url(), nil)
	if err != nil {
		metrics.RecordFxSync("internal_error")
		return fmt.Errorf("fxsync: build request: %w", err)
	}
	req.Header.Set("User-Agent", "BiuMind-model-relay/1.0 (+fxsync)")
	req.Header.Set("Accept", "application/json")

	resp, err := s.httpClient().Do(req)
	if err != nil {
		metrics.RecordFxSync("network_error")
		return fmt.Errorf("fxsync: get %s: %w", s.url(), err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4*1024))
		metrics.RecordFxSync("upstream_error")
		return fmt.Errorf("fxsync: %s returned %d: %s",
			s.url(), resp.StatusCode, body)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if err != nil {
		metrics.RecordFxSync("network_error")
		return fmt.Errorf("fxsync: read body: %w", err)
	}

	var up upstreamResponse
	if err := json.Unmarshal(body, &up); err != nil {
		metrics.RecordFxSync("parse_error")
		return fmt.Errorf("fxsync: parse: %w", err)
	}

	// Some endpoints respond 200 + { result: "error", ... }. Treat as
	// upstream_error so dashboards split it from network failures.
	if up.Result != "" && up.Result != "success" {
		metrics.RecordFxSync("upstream_error")
		return fmt.Errorf("fxsync: upstream not ok (result=%q)", up.Result)
	}
	if up.BaseCode != "USD" {
		metrics.RecordFxSync("parse_error")
		return fmt.Errorf("fxsync: expected base USD, got %q", up.BaseCode)
	}
	cnyRate, ok := up.Rates["CNY"]
	if !ok || cnyRate <= 0 {
		metrics.RecordFxSync("parse_error")
		return fmt.Errorf("fxsync: missing or zero CNY rate")
	}

	if _, err := s.Store.FxRates.Upsert(ctx, registry.FxRateUpsert{
		FromCurrency: registry.CurrencyUSD,
		ToCurrency:   registry.CurrencyCNY,
		Rate:         cnyRate,
		Source:       "cron",
	}); err != nil {
		metrics.RecordFxSync("db_error")
		return fmt.Errorf("fxsync: upsert USD→CNY: %w", err)
	}

	if _, err := s.Store.FxRates.Upsert(ctx, registry.FxRateUpsert{
		FromCurrency: registry.CurrencyCNY,
		ToCurrency:   registry.CurrencyUSD,
		Rate:         1.0 / cnyRate,
		Source:       "cron",
	}); err != nil {
		metrics.RecordFxSync("db_error")
		return fmt.Errorf("fxsync: upsert CNY→USD: %w", err)
	}

	metrics.RecordFxSync("ok")
	s.logger().Info("fxsync: rates updated",
		"usd_cny", cnyRate,
		"upstream_ts", up.UpdateAt,
	)
	return nil
}

// RunCron blocks on a ticker, calling SyncOnce every interval. Returns
// when ctx is cancelled. Caller usually runs this in a goroutine and
// passes a context tied to the service shutdown signal.
//
// First run fires after `firstDelay` (commonly 1-5 min after boot to
// avoid restart storms slamming the upstream).
func (s *Syncer) RunCron(ctx context.Context, interval, firstDelay time.Duration) {
	timer := time.NewTimer(firstDelay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		if err := s.SyncOnce(ctx); err != nil {
			if ctx.Err() == nil { // don't spam during shutdown
				s.logger().Warn("fxsync: SyncOnce failed", "err", err.Error())
			}
		}
		timer.Reset(interval)
	}
}
