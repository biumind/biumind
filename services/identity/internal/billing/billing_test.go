package billing

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeStore captures SetUserPlan calls so tests can assert dispatch.
type fakeStore struct {
	mu    sync.Mutex
	calls []fakeCall
	err   error // injected error
}

type fakeCall struct {
	UserID, CustomerID, SubID string
	Plan                      Plan
}

func (f *fakeStore) SetUserPlan(_ Context, uid, cust, sub string, p Plan) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.calls = append(f.calls, fakeCall{uid, cust, sub, p})
	return nil
}

// ─── Signature verification ─────────────────────────────

const testSecret = "whsec_test_0123456789abcdef"

func sign(t *testing.T, secret string, body []byte, ts time.Time) string {
	t.Helper()
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "%d.%s", ts.Unix(), body)
	return fmt.Sprintf("t=%d,v1=%s", ts.Unix(), hex.EncodeToString(mac.Sum(nil)))
}

func TestVerifyWebhookSignature_HappyPath(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	body := []byte(`{"id":"evt_x"}`)
	header := sign(t, testSecret, body, now)
	if err := VerifyWebhookSignature(testSecret, body, header, now); err != nil {
		t.Errorf("happy path: %v", err)
	}
}

func TestVerifyWebhookSignature_StaleTimestamp(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	body := []byte(`x`)
	stale := now.Add(-10 * time.Minute)
	header := sign(t, testSecret, body, stale)
	err := VerifyWebhookSignature(testSecret, body, header, now)
	if err == nil || !strings.Contains(err.Error(), "tolerance") {
		t.Errorf("expected stale rejection, got %v", err)
	}
}

func TestVerifyWebhookSignature_BadSignature(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	body := []byte(`x`)
	header := fmt.Sprintf("t=%d,v1=deadbeef", now.Unix())
	err := VerifyWebhookSignature(testSecret, body, header, now)
	if err == nil || !strings.Contains(err.Error(), "matching") {
		t.Errorf("expected mismatch rejection, got %v", err)
	}
}

func TestVerifyWebhookSignature_MissingHeader(t *testing.T) {
	if err := VerifyWebhookSignature(testSecret, []byte("x"), "",
		time.Now()); err == nil {
		t.Error("missing header must reject")
	}
}

func TestVerifyWebhookSignature_MissingSecret(t *testing.T) {
	if err := VerifyWebhookSignature("", []byte("x"), "t=1,v1=x",
		time.Now()); err == nil {
		t.Error("missing secret must reject")
	}
}

func TestVerifyWebhookSignature_MultipleV1KeyRotation(t *testing.T) {
	// During Stripe key rotation the header carries TWO v1 entries.
	// Verifier must accept if either matches.
	now := time.Unix(1_700_000_000, 0)
	body := []byte("x")
	mac := hmac.New(sha256.New, []byte(testSecret))
	fmt.Fprintf(mac, "%d.%s", now.Unix(), body)
	good := hex.EncodeToString(mac.Sum(nil))
	header := fmt.Sprintf("t=%d,v1=deadbeef,v1=%s", now.Unix(), good)
	if err := VerifyWebhookSignature(testSecret, body, header, now); err != nil {
		t.Errorf("rotation: %v", err)
	}
}

// ─── End-to-end webhook handler ─────────────────────────

func newTestServer(t *testing.T, store *fakeStore) (*Server, *httptest.Server) {
	t.Helper()
	srv := New(testSecret,
		map[string]Plan{
			"price_pro":  PlanPro,
			"price_team": PlanTeam,
		},
		store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv.Now = func() time.Time { return time.Unix(1_700_000_000, 0) }
	mux := http.NewServeMux()
	srv.Mount(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return srv, ts
}

func postWebhook(t *testing.T, ts *httptest.Server, body any) *http.Response {
	t.Helper()
	raw, _ := json.Marshal(body)
	header := sign(t, testSecret, raw, time.Unix(1_700_000_000, 0))
	req, _ := http.NewRequest(http.MethodPost,
		ts.URL+"/v1/billing/webhook", bytes.NewReader(raw))
	req.Header.Set("Stripe-Signature", header)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	return resp
}

func TestWebhook_SubscriptionCreated_MapsToPro(t *testing.T) {
	store := &fakeStore{}
	_, ts := newTestServer(t, store)
	resp := postWebhook(t, ts, map[string]any{
		"id":   "evt_1",
		"type": "customer.subscription.created",
		"data": map[string]any{
			"object": map[string]any{
				"id":       "sub_1",
				"customer": "cus_1",
				"status":   "active",
				"items": map[string]any{
					"data": []map[string]any{
						{"price": map[string]string{"id": "price_pro"}},
					},
				},
				"metadata": map[string]string{"biumind_user_id": "user_42"},
			},
		},
	})
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	if len(store.calls) != 1 {
		t.Fatalf("calls: %d", len(store.calls))
	}
	c := store.calls[0]
	if c.UserID != "user_42" || c.CustomerID != "cus_1" ||
		c.SubID != "sub_1" || c.Plan != PlanPro {
		t.Errorf("unexpected call: %+v", c)
	}
}

func TestWebhook_SubscriptionDeleted_DropsToFree(t *testing.T) {
	store := &fakeStore{}
	_, ts := newTestServer(t, store)
	resp := postWebhook(t, ts, map[string]any{
		"id":   "evt_2",
		"type": "customer.subscription.deleted",
		"data": map[string]any{
			"object": map[string]any{
				"id":       "sub_x",
				"customer": "cus_x",
				"items": map[string]any{
					"data": []map[string]any{
						{"price": map[string]string{"id": "price_pro"}},
					},
				},
				"metadata": map[string]string{"biumind_user_id": "u1"},
			},
		},
	})
	defer resp.Body.Close()
	if store.calls[0].Plan != PlanFree {
		t.Errorf("deleted should drop to free, got %s", store.calls[0].Plan)
	}
}

func TestWebhook_UnknownPriceFallsBackToFree(t *testing.T) {
	store := &fakeStore{}
	_, ts := newTestServer(t, store)
	resp := postWebhook(t, ts, map[string]any{
		"id":   "evt_3",
		"type": "customer.subscription.created",
		"data": map[string]any{
			"object": map[string]any{
				"id": "sub", "customer": "cus", "status": "active",
				"items": map[string]any{
					"data": []map[string]any{
						{"price": map[string]string{"id": "price_unknown"}},
					},
				},
				"metadata": map[string]string{"biumind_user_id": "u1"},
			},
		},
	})
	defer resp.Body.Close()
	if store.calls[0].Plan != PlanFree {
		t.Errorf("unknown price should fall back to free, got %s",
			store.calls[0].Plan)
	}
}

func TestWebhook_BadSignatureRejected(t *testing.T) {
	store := &fakeStore{}
	_, ts := newTestServer(t, store)
	body := []byte(`{"id":"x","type":"customer.subscription.created"}`)
	req, _ := http.NewRequest(http.MethodPost,
		ts.URL+"/v1/billing/webhook", bytes.NewReader(body))
	req.Header.Set("Stripe-Signature",
		fmt.Sprintf("t=%d,v1=deadbeef", time.Unix(1_700_000_000, 0).Unix()))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Errorf("bad sig should 400, got %d", resp.StatusCode)
	}
	if len(store.calls) != 0 {
		t.Errorf("store should not have been called: %+v", store.calls)
	}
}

func TestWebhook_UnknownEventIsHarmless(t *testing.T) {
	store := &fakeStore{}
	_, ts := newTestServer(t, store)
	resp := postWebhook(t, ts, map[string]any{
		"id":   "evt_x",
		"type": "charge.succeeded",  // we don't handle this
		"data": map[string]any{"object": map[string]any{}},
	})
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("unhandled event should still 200, got %d",
			resp.StatusCode)
	}
	if len(store.calls) != 0 {
		t.Errorf("unrelated event must not touch store")
	}
}

func TestLimitsFor_FallsBackOnUnknownPlan(t *testing.T) {
	limits := LimitsFor(Plan("garbage"))
	want := DefaultLimits[PlanFree]
	if limits != want {
		t.Errorf("unknown plan should map to free: got %+v want %+v",
			limits, want)
	}
}

func TestLimitsFor_KnownPlansHaveDistinctCeilings(t *testing.T) {
	free := LimitsFor(PlanFree)
	pro := LimitsFor(PlanPro)
	team := LimitsFor(PlanTeam)
	if !(free.HubRPM < pro.HubRPM && pro.HubRPM < team.HubRPM) {
		t.Errorf("RPM should monotonically grow: free=%d pro=%d team=%d",
			free.HubRPM, pro.HubRPM, team.HubRPM)
	}
}

// ─── Customer portal ────────────────────────────────────

type fakeLinker struct {
	url string
	err error
	got struct{ customerID, returnURL string }
}

func (f *fakeLinker) PortalSessionURL(cust, ret string) (string, error) {
	f.got.customerID = cust
	f.got.returnURL = ret
	return f.url, f.err
}

func portalReq(t *testing.T, ts *httptest.Server, body string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/portal",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	return resp
}

func mountPortal(linker PortalLinker, lookup CustomerLookup,
	uid string,
) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /portal", HandlePortalSession(linker, lookup,
		func(_ *http.Request) string { return uid }))
	return httptest.NewServer(mux)
}

func TestHandlePortalSession_HappyPath(t *testing.T) {
	linker := &fakeLinker{url: "https://billing.stripe.com/p/session/abc"}
	lookup := func(uid string) (string, error) {
		if uid != "user_42" {
			t.Fatalf("uid: %s", uid)
		}
		return "cus_42", nil
	}
	ts := mountPortal(linker, lookup, "user_42")
	defer ts.Close()

	resp := portalReq(t, ts, `{"return_url":"https://app.biumind.com/account/return"}`)
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var out struct{ URL string }
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if out.URL != linker.url {
		t.Errorf("url: got %q want %q", out.URL, linker.url)
	}
	if linker.got.customerID != "cus_42" {
		t.Errorf("customer: %s", linker.got.customerID)
	}
	if linker.got.returnURL != "https://app.biumind.com/account/return" {
		t.Errorf("return url: %s", linker.got.returnURL)
	}
}

func TestHandlePortalSession_NoUser_401(t *testing.T) {
	ts := mountPortal(&fakeLinker{}, func(string) (string, error) {
		return "", nil
	}, "")
	defer ts.Close()
	resp := portalReq(t, ts, `{}`)
	defer resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
}

func TestHandlePortalSession_NoCustomer_400(t *testing.T) {
	ts := mountPortal(&fakeLinker{},
		func(string) (string, error) { return "", nil }, "user_x")
	defer ts.Close()
	resp := portalReq(t, ts, `{}`)
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Errorf("expected 400 when no Stripe customer, got %d", resp.StatusCode)
	}
}

func TestHandlePortalSession_LinkerError_502(t *testing.T) {
	linker := &fakeLinker{err: fmt.Errorf("stripe down")}
	ts := mountPortal(linker,
		func(string) (string, error) { return "cus_1", nil }, "user_1")
	defer ts.Close()
	resp := portalReq(t, ts, `{}`)
	defer resp.Body.Close()
	if resp.StatusCode != 502 {
		t.Errorf("expected 502 on linker error, got %d", resp.StatusCode)
	}
}

func TestHandlePortalSession_DefaultsReturnURL(t *testing.T) {
	linker := &fakeLinker{url: "https://billing.stripe.com/p/session/y"}
	ts := mountPortal(linker,
		func(string) (string, error) { return "cus_1", nil }, "u1")
	defer ts.Close()
	resp := portalReq(t, ts, ``) // empty body
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	if linker.got.returnURL != "https://app.biumind.com/account" {
		t.Errorf("expected default return url, got %s", linker.got.returnURL)
	}
}
