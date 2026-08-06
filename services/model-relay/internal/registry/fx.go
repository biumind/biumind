// FxRateRepo is the CRUD surface for model_relay.fx_rates.
//
// Tiny table — typically 4 rows (USD↔CNY + self-reflexive). The hot
// path is GetRate, called once per usage write. We don't bother with
// caching at this layer; the registry cache (M2.2) handles invalidation
// on UPDATE.
//
// Self-reflexive rows (USD→USD, CNY→CNY) are seeded as 1.0 in 00003
// so the usage writer can blindly do GetRate(origin, settle) without
// branching for "same currency".

package registry

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type FxRateRepo struct {
	pool *pgxpool.Pool
}

func (r *FxRateRepo) List(ctx context.Context) ([]FxRate, error) {
	const q = `
		SELECT from_currency, to_currency, rate, source, updated_at, updated_by
		FROM model_relay.fx_rates
		ORDER BY from_currency, to_currency
	`
	rows, err := r.pool.Query(ctx, q)
	if err != nil {
		return nil, translateErr("fx.list", err)
	}
	defer rows.Close()

	out := make([]FxRate, 0, 4)
	for rows.Next() {
		var f FxRate
		if err := rows.Scan(
			&f.FromCurrency, &f.ToCurrency, &f.Rate,
			&f.Source, &f.UpdatedAt, &f.UpdatedBy,
		); err != nil {
			return nil, translateErr("fx.list.scan", err)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// GetRate returns the rate for a (from, to) pair, or ErrNotFound. The
// usage writer hits this for every settled request — keep it cheap;
// the registry cache (M2.2) is in front.
func (r *FxRateRepo) GetRate(ctx context.Context, from, to Currency) (float64, error) {
	const q = `SELECT rate FROM model_relay.fx_rates WHERE from_currency = $1 AND to_currency = $2`
	var rate float64
	if err := r.pool.QueryRow(ctx, q, from, to).Scan(&rate); err != nil {
		return 0, translateErr("fx.get_rate", err)
	}
	return rate, nil
}

// Upsert sets or updates a rate. source is "manual" (admin form) or
// "cron" (Phase 2 auto-refresh).
type FxRateUpsert struct {
	FromCurrency Currency
	ToCurrency   Currency
	Rate         float64
	Source       string
	UpdatedBy    *uuid.UUID
}

func (in FxRateUpsert) validate() error {
	if in.FromCurrency != CurrencyCNY && in.FromCurrency != CurrencyUSD {
		return fmt.Errorf("fx: invalid from_currency %q", in.FromCurrency)
	}
	if in.ToCurrency != CurrencyCNY && in.ToCurrency != CurrencyUSD {
		return fmt.Errorf("fx: invalid to_currency %q", in.ToCurrency)
	}
	if in.Rate <= 0 {
		return fmt.Errorf("fx: rate must be > 0")
	}
	if in.Source != "manual" && in.Source != "cron" {
		return fmt.Errorf("fx: invalid source %q", in.Source)
	}
	return nil
}

func (r *FxRateRepo) Upsert(ctx context.Context, in FxRateUpsert) (*FxRate, error) {
	if err := in.validate(); err != nil {
		return nil, err
	}
	const q = `
		INSERT INTO model_relay.fx_rates
			(from_currency, to_currency, rate, source, updated_at, updated_by)
		VALUES ($1, $2, $3, $4, now(), $5)
		ON CONFLICT (from_currency, to_currency) DO UPDATE
		   SET rate       = EXCLUDED.rate,
		       source     = EXCLUDED.source,
		       updated_at = EXCLUDED.updated_at,
		       updated_by = EXCLUDED.updated_by
		RETURNING from_currency, to_currency, rate, source, updated_at, updated_by
	`
	var f FxRate
	err := r.pool.QueryRow(ctx, q,
		in.FromCurrency, in.ToCurrency, in.Rate, in.Source, in.UpdatedBy,
	).Scan(
		&f.FromCurrency, &f.ToCurrency, &f.Rate,
		&f.Source, &f.UpdatedAt, &f.UpdatedBy,
	)
	if err != nil {
		return nil, translateErr("fx.upsert", err)
	}
	return &f, nil
}

// StalestRate returns the (from,to) pair whose updated_at is oldest.
// Admin "汇率提醒" banner uses this to surface "距上次更新 N 天".
func (r *FxRateRepo) StalestRate(ctx context.Context) (*FxRate, time.Duration, error) {
	const q = `
		SELECT from_currency, to_currency, rate, source, updated_at, updated_by
		FROM model_relay.fx_rates
		WHERE source = 'manual'   -- self-reflexive 1:1 are also manual
		ORDER BY updated_at ASC
		LIMIT 1
	`
	var f FxRate
	err := r.pool.QueryRow(ctx, q).Scan(
		&f.FromCurrency, &f.ToCurrency, &f.Rate,
		&f.Source, &f.UpdatedAt, &f.UpdatedBy,
	)
	if err != nil {
		return nil, 0, translateErr("fx.stalest", err)
	}
	return &f, time.Since(f.UpdatedAt), nil
}
