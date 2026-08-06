package admin

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	bauth "github.com/biumind/biumind/packages/go-sdk/biu/auth"
	"github.com/biumind/biumind/services/identity/internal/billing"
	"github.com/golang-jwt/jwt/v5"
)

const testJWTSecret = "admin-test-secret-32-chars-aaaaaaaa"

// fakeStore — minimal in-memory User registry.
type fakeStore struct {
	users map[string]*User
}

func newFakeStore() *fakeStore {
	return &fakeStore{users: map[string]*User{
		"u1": {ID: "u1", Email: "alice@example.com", Plan: "free",
			CreatedAt: time.Unix(1, 0)},
		"u2": {ID: "u2", Email: "bob@example.com", Plan: "pro",
			CreatedAt: time.Unix(2, 0)},
	}}
}

func (s *fakeStore) ListUsers(q string, limit, offset int) ([]User, int, error) {
	out := []User{}
	for _, u := range s.users {
		if q == "" || strings.Contains(u.Email, q) {
			out = append(out, *u)
		}
	}
	total := len(out)
	if offset > len(out) {
		return []User{}, total, nil
	}
	end := offset + limit
	if end > len(out) {
		end = len(out)
	}
	return out[offset:end], total, nil
}

func (s *fakeStore) GetUser(id string) (*User, error) {
	u, ok := s.users[id]
	if !ok {
		return nil, errNotFound
	}
	return u, nil
}

func (s *fakeStore) SetUserPlan(id string, plan billing.Plan, _ string) error {
	u, ok := s.users[id]
	if !ok {
		return errNotFound
	}
	u.Plan = string(plan)
	return nil
}

func (s *fakeStore) SetUserRole(id, role, _, _ string) error {
	u, ok := s.users[id]
	if !ok {
		return errNotFound
	}
	u.Role = role
	return nil
}

// 假撤 session: 总返 0 (fake 不维护 session 状态)
func (s *fakeStore) RevokeAllSessions(_ string) (int64, error) { return 0, nil }

func (s *fakeStore) CountUsersByRole(role string) (int, error) {
	n := 0
	for _, u := range s.users {
		if u.Role == role {
			n++
		}
	}
	return n, nil
}

var errNotFound = &fakeNotFound{}

type fakeNotFound struct{}

func (e *fakeNotFound) Error() string { return "not found" }

// mintJWT writes a token with the given roles. scopes 参数保留兼容旧调用,
// 内部翻译: 如果 scopes 含 "admin" 字符串, 当 admin role 处理.
func mintJWT(t *testing.T, uid string, scopes []string) string {
	t.Helper()
	roles := []string{}
	for _, s := range scopes {
		if s == "admin" {
			roles = append(roles, "admin")
		}
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": uid, "uid": uid,
		"iss":   "https://identity.biumind.local",
		"aud":   "biumind-api",
		"roles": roles,
		"iat":   time.Now().Unix(),
		"exp":   time.Now().Add(time.Hour).Unix(),
	})
	s, _ := tok.SignedString([]byte(testJWTSecret))
	return s
}

func newTestServer(t *testing.T) (*Server, *fakeStore, *httptest.Server) {
	t.Helper()
	store := newFakeStore()
	v := bauth.NewVerifier(testJWTSecret,
		"https://identity.biumind.local", "biumind-api")
	srv := New(store, NewAuditLog(64), v,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	mux := http.NewServeMux()
	srv.Mount(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return srv, store, ts
}

func get(t *testing.T, ts *httptest.Server, token, path string) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, ts.URL+path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp, body
}

// ─── tests ──────────────────────────────────────────────

func TestAdmin_RequiresBearerToken(t *testing.T) {
	_, _, ts := newTestServer(t)
	resp, _ := get(t, ts, "", "/v1/admin/users")
	if resp.StatusCode != 401 {
		t.Errorf("status: %d, want 401", resp.StatusCode)
	}
}

func TestAdmin_RejectsNonAdminToken(t *testing.T) {
	_, _, ts := newTestServer(t)
	tok := mintJWT(t, "regular-user", []string{"user"})
	resp, _ := get(t, ts, tok, "/v1/admin/users")
	if resp.StatusCode != 403 {
		t.Errorf("non-admin status: %d, want 403", resp.StatusCode)
	}
}

func TestAdmin_AcceptsAdminToken(t *testing.T) {
	_, _, ts := newTestServer(t)
	tok := mintJWT(t, "admin-1", []string{"admin"})
	resp, body := get(t, ts, tok, "/v1/admin/users")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body=%s", resp.StatusCode, body)
	}
	var out struct {
		Users []User `json:"users"`
		Total int    `json:"total"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if out.Total != 2 || len(out.Users) != 2 {
		t.Errorf("expected 2 users, got total=%d len=%d",
			out.Total, len(out.Users))
	}
}

func TestAdmin_ListUsersFiltersByQuery(t *testing.T) {
	_, _, ts := newTestServer(t)
	tok := mintJWT(t, "admin-1", []string{"admin"})
	resp, body := get(t, ts, tok, "/v1/admin/users?q=alice")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var out struct {
		Users []User `json:"users"`
	}
	_ = json.Unmarshal(body, &out)
	if len(out.Users) != 1 || out.Users[0].Email != "alice@example.com" {
		t.Errorf("filter: %+v", out.Users)
	}
}

func TestAdmin_GetUser_IncludesLimits(t *testing.T) {
	_, _, ts := newTestServer(t)
	tok := mintJWT(t, "admin-1", []string{"admin"})
	resp, body := get(t, ts, tok, "/v1/admin/users/u2")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body=%s", resp.StatusCode, body)
	}
	var out struct {
		User   User               `json:"user"`
		Limits billing.PlanLimits `json:"limits"`
	}
	_ = json.Unmarshal(body, &out)
	if out.User.Plan != "pro" {
		t.Errorf("plan: %s", out.User.Plan)
	}
	if out.Limits.HubRPM == 0 {
		t.Errorf("limits not populated: %+v", out.Limits)
	}
}

func TestAdmin_SetPlan_OverridesAndAudits(t *testing.T) {
	srv, store, ts := newTestServer(t)
	tok := mintJWT(t, "admin-7", []string{"admin"})

	body := []byte(`{"plan":"team","reason":"customer requested upgrade"}`)
	req, _ := http.NewRequest(http.MethodPost,
		ts.URL+"/v1/admin/users/u1/plan", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}

	if store.users["u1"].Plan != "team" {
		t.Errorf("store not mutated: %+v", store.users["u1"])
	}
	events := srv.Audit.Recent(10)
	if len(events) != 1 {
		t.Fatalf("audit count: %d", len(events))
	}
	if events[0].ActorID != "admin-7" || events[0].Action != "user.plan.override" ||
		events[0].Target != "u1" || !strings.Contains(events[0].Detail, "team") {
		t.Errorf("audit event: %+v", events[0])
	}
}

func TestAdmin_SetPlan_RejectsInvalidPlan(t *testing.T) {
	_, _, ts := newTestServer(t)
	tok := mintJWT(t, "admin", []string{"admin"})
	body := []byte(`{"plan":"deluxe-platinum","reason":"x"}`)
	req, _ := http.NewRequest(http.MethodPost,
		ts.URL+"/v1/admin/users/u1/plan", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Errorf("status: %d, want 400", resp.StatusCode)
	}
}

func TestAdmin_AuditLog_RingBufferRotation(t *testing.T) {
	a := NewAuditLog(3)
	for i := 0; i < 7; i++ {
		a.Append(AuditEvent{
			ActorID: "test", Action: "x",
			Detail: string(rune('a' + i)),
		})
	}
	got := a.Recent(0)
	if len(got) != 3 {
		t.Errorf("ring size: %d, want 3", len(got))
	}
	// Newest-first ordering.
	if got[0].Detail != "g" || got[1].Detail != "f" || got[2].Detail != "e" {
		t.Errorf("ordering / rotation: %+v", got)
	}
}

func TestAdmin_AuditEndpoint_ReturnsRecent(t *testing.T) {
	srv, _, ts := newTestServer(t)
	srv.Audit.Append(AuditEvent{
		ActorID: "boot", Action: "service.start"})
	tok := mintJWT(t, "admin", []string{"admin"})
	resp, body := get(t, ts, tok, "/v1/admin/audit")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var out struct {
		Events []AuditEvent `json:"events"`
	}
	_ = json.Unmarshal(body, &out)
	if len(out.Events) == 0 ||
		out.Events[0].Action != "service.start" {
		t.Errorf("events: %+v", out.Events)
	}
}
