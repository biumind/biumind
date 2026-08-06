// usage.go — GET /v1/me/usage  (用户 JWT, 账户级用量)
//
// Backs the client 数据统计 · 用量 page. Account-wide aggregations over
// model_relay.usage_log for the authenticated user, in 积分 (credits):
//   - header cards: today / month spend, month request count, active models
//   - daily trend buckets (credits + tokens) for the selected month
//   - per-model breakdown for the selected month
//   - per-call list (paginated, newest first) with derived TPS
//
// Query params:
//   - mo (YYYY-MM, optional): selected month; default = current month (UTC).
//   - page (1-based, optional, default 1) / page_size (optional, default 20,
//     max 100): per-call list pagination.
//
// Spend is 积分; the authoritative ledger remains identity.credit_logs.
// Provider-level grouping is intentionally unsupported (no provider column).

package api

import (
	"context"
	"net/http"
	"strconv"
	"time"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/biumind/biumind/services/model-relay/internal/registry"
	"github.com/google/uuid"
)

// usageReader is the minimal slice of UsageLogRepo this handler needs — an
// interface so tests can inject a fake.
type usageReader interface {
	UsageSummaryFor(ctx context.Context, userID uuid.UUID, monthStart, monthEnd, todayStart time.Time) (registry.UsageSummary, error)
	UsageDailyFor(ctx context.Context, userID uuid.UUID, monthStart, monthEnd time.Time) ([]registry.UsageDailyBucket, error)
	UsageByModelFor(ctx context.Context, userID uuid.UUID, monthStart, monthEnd time.Time, limit int) ([]registry.UsageModelBucket, error)
	ListByUserMonth(ctx context.Context, userID uuid.UUID, monthStart, monthEnd time.Time, limit, offset int) ([]registry.UsageLog, int64, error)
}

// UsageHandler serves GET /v1/me/usage. Holds only the usage_log read repo.
type UsageHandler struct {
	Usage usageReader
}

func (h *UsageHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "")
		return
	}
	if h.Usage == nil {
		writeJSONErr(w, http.StatusServiceUnavailable, "usage_unavailable", "")
		return
	}
	claims, ok := bauth.ClaimsFrom(r.Context())
	if !ok || claims.UserID == "" {
		writeJSONErr(w, http.StatusUnauthorized, "no_user", "")
		return
	}
	uid, err := uuid.Parse(claims.UserID)
	if err != nil {
		writeJSONErr(w, http.StatusUnauthorized, "bad_user", "")
		return
	}

	q := r.URL.Query()
	now := time.Now().UTC()
	monthStart := monthStartFromQuery(q.Get("mo"), now)
	monthEnd := monthStart.AddDate(0, 1, 0)
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	page := atoiClamp(q.Get("page"), 1, 1, 1_000_000)
	pageSize := atoiClamp(q.Get("page_size"), 20, 1, 100)
	offset := (page - 1) * pageSize

	ctx := r.Context()
	summary, err := h.Usage.UsageSummaryFor(ctx, uid, monthStart, monthEnd, todayStart)
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, "summary_failed", err.Error())
		return
	}
	daily, err := h.Usage.UsageDailyFor(ctx, uid, monthStart, monthEnd)
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, "daily_failed", err.Error())
		return
	}
	byModel, err := h.Usage.UsageByModelFor(ctx, uid, monthStart, monthEnd, 50)
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, "by_model_failed", err.Error())
		return
	}
	rows, total, err := h.Usage.ListByUserMonth(ctx, uid, monthStart, monthEnd, pageSize, offset)
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}

	calls := make([]map[string]any, 0, len(rows))
	for _, u := range rows {
		calls = append(calls, map[string]any{
			"model":         u.ModelCode,
			"input_tokens":  u.InputTokens,
			"output_tokens": u.OutputTokens,
			"tps":           tps(u.OutputTokens, u.LatencyMs),
			"latency_ms":    u.LatencyMs,
			"credits":       u.CreditsCharged,
			"status":        u.Status,
			"created_at":    u.CreatedAt.UTC().Format(time.RFC3339),
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"month":     monthStart.Format("2006-01"),
		"summary":   summary,
		"daily":     daily,
		"by_model":  byModel,
		"calls":     calls,
		"page":      page,
		"page_size": pageSize,
		"total":     total,
	})
}

// monthStartFromQuery parses a YYYY-MM month into its UTC first-of-month, or
// falls back to the current month when empty / unparseable.
func monthStartFromQuery(mo string, now time.Time) time.Time {
	if mo != "" {
		if t, err := time.Parse("2006-01", mo); err == nil {
			return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
		}
	}
	return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// tps derives tokens-per-second from output tokens and total latency. Guards
// against a zero/negative latency (returns 0 rather than dividing).
func tps(outputTokens int64, latencyMs int) float64 {
	if latencyMs <= 0 || outputTokens <= 0 {
		return 0
	}
	return float64(outputTokens) * 1000.0 / float64(latencyMs)
}

func atoiClamp(s string, def, lo, hi int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	if n < lo {
		return lo
	}
	if n > hi {
		return hi
	}
	return n
}
