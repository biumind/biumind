package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/biumind/biumind/services/model-relay/internal/registry"
	"github.com/google/uuid"
)

func TestTPS(t *testing.T) {
	cases := []struct {
		out     int64
		latency int
		want    float64
	}{
		{0, 0, 0},
		{100, 0, 0},   // zero latency → guard
		{0, 1000, 0},  // zero output → guard
		{1000, 1000, 1000.0},
		{50, 500, 100.0},
	}
	for _, c := range cases {
		if got := tps(c.out, c.latency); got != c.want {
			t.Errorf("tps(%d,%d)=%v want %v", c.out, c.latency, got, c.want)
		}
	}
}

func TestMonthStartFromQuery(t *testing.T) {
	now := time.Date(2026, 6, 25, 13, 0, 0, 0, time.UTC)
	// Explicit month.
	got := monthStartFromQuery("2026-03", now)
	if got != time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC) {
		t.Errorf("explicit month: got %v", got)
	}
	// Empty / garbage → current month.
	for _, in := range []string{"", "nope", "2026-13"} {
		got := monthStartFromQuery(in, now)
		if got != time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC) {
			t.Errorf("fallback for %q: got %v", in, got)
		}
	}
}

func TestAtoiClamp(t *testing.T) {
	if atoiClamp("", 20, 1, 100) != 20 {
		t.Error("empty → default")
	}
	if atoiClamp("5", 20, 1, 100) != 5 {
		t.Error("valid passthrough")
	}
	if atoiClamp("0", 20, 1, 100) != 1 {
		t.Error("below lo → lo")
	}
	if atoiClamp("999", 20, 1, 100) != 100 {
		t.Error("above hi → hi")
	}
	if atoiClamp("x", 20, 1, 100) != 20 {
		t.Error("garbage → default")
	}
}

// fakeUsage implements usageReader for the handler test.
type fakeUsage struct {
	summary registry.UsageSummary
	total   int64
}

func (f *fakeUsage) UsageSummaryFor(context.Context, uuid.UUID, time.Time, time.Time, time.Time) (registry.UsageSummary, error) {
	return f.summary, nil
}
func (f *fakeUsage) UsageDailyFor(context.Context, uuid.UUID, time.Time, time.Time) ([]registry.UsageDailyBucket, error) {
	return []registry.UsageDailyBucket{{Date: "2026-06-17", Credits: 86, Tokens: 1000, Requests: 3}}, nil
}
func (f *fakeUsage) UsageByModelFor(context.Context, uuid.UUID, time.Time, time.Time, int) ([]registry.UsageModelBucket, error) {
	return []registry.UsageModelBucket{{Model: "glm-5.1", Requests: 3, InputTokens: 900, OutputTokens: 100, Credits: 86}}, nil
}
func (f *fakeUsage) ListByUserMonth(context.Context, uuid.UUID, time.Time, time.Time, int, int) ([]registry.UsageLog, int64, error) {
	return []registry.UsageLog{{
		ModelCode: "glm-5.1", InputTokens: 900, OutputTokens: 100,
		LatencyMs: 1000, CreditsCharged: 86, Status: registry.UsageOK,
		CreatedAt: time.Date(2026, 6, 17, 10, 0, 0, 0, time.UTC),
	}}, f.total, nil
}

func TestUsageHandler(t *testing.T) {
	h := &UsageHandler{Usage: &fakeUsage{
		summary: registry.UsageSummary{TodayCredits: 0, MonthCredits: 86, MonthRequests: 643, ActiveModels: 2},
		total:   643,
	}}

	// No claims → 401.
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/me/usage", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("no claims: got %d want 401", w.Code)
	}

	// With claims → 200 + shaped body.
	uid := uuid.New()
	ctx := bauth.WithClaims(context.Background(), &bauth.Claims{UserID: uid.String()})
	r := httptest.NewRequest(http.MethodGet, "/v1/me/usage?mo=2026-06&page=1&page_size=5", nil).WithContext(ctx)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("with claims: got %d want 200 (%s)", w.Code, w.Body.String())
	}
	var body struct {
		Month   string `json:"month"`
		Summary registry.UsageSummary `json:"summary"`
		Calls   []struct {
			Model   string  `json:"model"`
			TPS     float64 `json:"tps"`
			Credits int64   `json:"credits"`
		} `json:"calls"`
		Total int64 `json:"total"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Month != "2026-06" || body.Summary.MonthCredits != 86 || body.Total != 643 {
		t.Errorf("unexpected summary: %+v", body)
	}
	if len(body.Calls) != 1 || body.Calls[0].Model != "glm-5.1" || body.Calls[0].TPS != 100.0 || body.Calls[0].Credits != 86 {
		t.Errorf("unexpected calls: %+v", body.Calls)
	}
}
