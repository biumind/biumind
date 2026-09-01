package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/biumind/biumind/services/model-relay/internal/billing"
)

type stubChargeBiller struct {
	entry       *billing.PricingEntry
	pricingErr  error
	holdErr     error
	settleErr   error
	holdCalls   int
	settleCalls int
	released    bool
}

func (s *stubChargeBiller) LookupPrice(_ context.Context, _, _ string) (*billing.PricingEntry, error) {
	return s.entry, s.pricingErr
}

func (s *stubChargeBiller) Hold(_ context.Context, _ billing.HoldArgs) (*billing.Hold, error) {
	s.holdCalls++
	if s.holdErr != nil {
		return nil, s.holdErr
	}
	return &billing.Hold{ID: "hold-1"}, nil
}

func (s *stubChargeBiller) Settle(_ context.Context, _ string, _ int64, _ string) error {
	s.settleCalls++
	return s.settleErr
}

func (s *stubChargeBiller) Release(_ context.Context, _ string) error {
	s.released = true
	return nil
}

func chargeRequest(h http.Handler, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/internal/usage/charge",
		strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestUsageCharge_MissingFields(t *testing.T) {
	h := &UsageChargeHandler{Billing: &stubChargeBiller{}}
	rec := chargeRequest(h, `{"user_id":"u1"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestUsageCharge_PricingMissingZeroCharge(t *testing.T) {
	stub := &stubChargeBiller{pricingErr: billing.ErrPricingNotFound}
	h := &UsageChargeHandler{Billing: stub}
	rec := chargeRequest(h, `{"user_id":"u1","model":"wiki-parse-text","ref_type":"parse_page","quantity":10,"idempotency_key":"k1"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out["pricing_missing"] != true || out["charged_amount"] != float64(0) {
		t.Fatalf("want zero-charge+pricing_missing, got %v", out)
	}
	if stub.holdCalls != 0 {
		t.Fatalf("pricing missing must not hold (got %d holds)", stub.holdCalls)
	}
}

func TestUsageCharge_DryRunQuotesOnly(t *testing.T) {
	stub := &stubChargeBiller{entry: &billing.PricingEntry{
		CostInputPerUnit: 100, MarkupRatio: 2.0, MinCharge: 1,
	}}
	h := &UsageChargeHandler{Billing: stub}
	rec := chargeRequest(h, `{"user_id":"u1","model":"wiki-parse-text","ref_type":"parse_page","quantity":10,"idempotency_key":"k1","dry_run":true}`)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	// 100 × 10 页 × markup 2.0 = 2000
	if out["charged_amount"] != float64(2000) || out["dry_run"] != true {
		t.Fatalf("want quote 2000 dry_run, got %v", out)
	}
	if stub.holdCalls != 0 || stub.settleCalls != 0 {
		t.Fatalf("dry_run must not touch identity")
	}
}

func TestUsageCharge_HoldSettleSuccess(t *testing.T) {
	stub := &stubChargeBiller{entry: &billing.PricingEntry{
		CostInputPerUnit: 100, MarkupRatio: 1.0, MinCharge: 1,
	}}
	h := &UsageChargeHandler{Billing: stub}
	rec := chargeRequest(h, `{"user_id":"u1","model":"wiki-parse-text","ref_type":"parse_page","quantity":3,"idempotency_key":"k1"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out["charged_amount"] != float64(300) || out["hold_id"] != "hold-1" {
		t.Fatalf("want charged 300 hold-1, got %v", out)
	}
	if stub.holdCalls != 1 || stub.settleCalls != 1 || stub.released {
		t.Fatalf("want 1 hold + 1 settle + no release, got %d/%d/%v",
			stub.holdCalls, stub.settleCalls, stub.released)
	}
}

func TestUsageCharge_Insufficient402(t *testing.T) {
	stub := &stubChargeBiller{
		entry:   &billing.PricingEntry{CostInputPerUnit: 100, MarkupRatio: 1.0, MinCharge: 1},
		holdErr: billing.ErrInsufficient,
	}
	h := &UsageChargeHandler{Billing: stub}
	rec := chargeRequest(h, `{"user_id":"u1","model":"wiki-parse-text","ref_type":"parse_page","quantity":3,"idempotency_key":"k1"}`)
	if rec.Code != http.StatusPaymentRequired {
		t.Fatalf("want 402, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestUsageCharge_SettleFailureReleases(t *testing.T) {
	stub := &stubChargeBiller{
		entry:     &billing.PricingEntry{CostInputPerUnit: 100, MarkupRatio: 1.0, MinCharge: 1},
		settleErr: errors.New("identity down"),
	}
	h := &UsageChargeHandler{Billing: stub}
	rec := chargeRequest(h, `{"user_id":"u1","model":"wiki-parse-text","ref_type":"parse_page","quantity":3,"idempotency_key":"k1"}`)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("want 502, got %d", rec.Code)
	}
	if !stub.released {
		t.Fatal("settle failure must release the hold")
	}
}
