package internalapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newTestServer(t *testing.T, token string, lookup PlanLookup) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	New(token, lookup).Mount(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func get(t *testing.T, ts *httptest.Server, path, token string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, ts.URL+path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	return resp
}

func TestPlan_HappyPath(t *testing.T) {
	ts := newTestServer(t, "secret-token", func(uid string) (string, error) {
		if uid != "user_42" {
			t.Fatalf("uid: %s", uid)
		}
		return "pro", nil
	})
	resp := get(t, ts, "/v1/internal/users/user_42/plan", "secret-token")
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var body struct{ Plan string }
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body.Plan != "pro" {
		t.Errorf("plan: %s", body.Plan)
	}
}

func TestPlan_NotFoundFallsBackToFree(t *testing.T) {
	ts := newTestServer(t, "tok", func(string) (string, error) {
		return "", ErrNotFound
	})
	resp := get(t, ts, "/v1/internal/users/ghost/plan", "tok")
	defer resp.Body.Close()
	var body struct{ Plan string }
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body.Plan != "free" {
		t.Errorf("expected free for missing user, got %s", body.Plan)
	}
}

func TestPlan_MissingToken_401(t *testing.T) {
	ts := newTestServer(t, "tok",
		func(string) (string, error) { return "pro", nil })
	resp := get(t, ts, "/v1/internal/users/u/plan", "")
	defer resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
}

func TestPlan_BadToken_401(t *testing.T) {
	ts := newTestServer(t, "tok",
		func(string) (string, error) { return "pro", nil })
	resp := get(t, ts, "/v1/internal/users/u/plan", "wrong")
	defer resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
}

func TestPlan_EmptyTokenSkipsAuth(t *testing.T) {
	ts := newTestServer(t, "",
		func(string) (string, error) { return "team", nil })
	resp := get(t, ts, "/v1/internal/users/u/plan", "")
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("expected 200 in dev mode, got %d", resp.StatusCode)
	}
}

func TestPlan_LookupErrorIs500(t *testing.T) {
	ts := newTestServer(t, "tok", func(string) (string, error) {
		return "", &customErr{}
	})
	resp := get(t, ts, "/v1/internal/users/u/plan", "tok")
	defer resp.Body.Close()
	if resp.StatusCode != 500 {
		t.Errorf("expected 500 on opaque error, got %d", resp.StatusCode)
	}
}

type customErr struct{}

func (*customErr) Error() string { return "boom" }
