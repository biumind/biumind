package skillsync

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeServer scripts a sequence of (method, path, status, body)
// responses. Tests assert against the recorded requests so the wire
// format pins to what services/runtime/internal/api expects.

type recordedReq struct {
	Method, Path, Body, Auth string
	Query                    string
}

type fakeServer struct {
	t        *testing.T
	srv      *httptest.Server
	received []recordedReq
}

type scriptedResponse struct {
	status int
	body   string
}

func newFakeServer(t *testing.T, scripted []scriptedResponse) *fakeServer {
	t.Helper()
	f := &fakeServer{t: t}
	idx := 0
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.received = append(f.received, recordedReq{
			Method: r.Method, Path: r.URL.Path,
			Query: r.URL.RawQuery,
			Body:  string(body),
			Auth:  r.Header.Get("Authorization"),
		})
		if idx >= len(scripted) {
			http.Error(w, "out of script", http.StatusInternalServerError)
			return
		}
		s := scripted[idx]
		idx++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(s.status)
		_, _ = io.WriteString(w, s.body)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func TestList_HappyPath(t *testing.T) {
	f := newFakeServer(t, []scriptedResponse{{
		status: 200,
		body: `{"skills":[
			{"id":"skill_a","identifier":"code-review","name":"Code Review","content_hash":"h1","status":"active"},
			{"id":"skill_b","identifier":"weekly-report","name":"Weekly Report","content_hash":"h2","status":"active"}
		]}`,
	}})
	c := New(f.srv.URL, "tok")
	got, err := c.List(context.Background(), ListOptions{Status: "active"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("want 2 skills, got %d", len(got))
	}
	if got[0].Identifier != "code-review" {
		t.Errorf("first identifier = %q", got[0].Identifier)
	}
	if f.received[0].Auth != "Bearer tok" {
		t.Errorf("auth header = %q", f.received[0].Auth)
	}
	if !strings.Contains(f.received[0].Query, "status=active") {
		t.Errorf("status query missing: %q", f.received[0].Query)
	}
}

func TestInstallInline_RoundTrip(t *testing.T) {
	f := newFakeServer(t, []scriptedResponse{{
		status: 200,
		body:   `{"id":"skill_x","identifier":"hello","name":"Hello","status":"active","content_hash":"abcd"}`,
	}})
	c := New(f.srv.URL, "tok")
	s, err := c.InstallInline(context.Background(), InstallInlineRequest{
		Identifier: "hello", Name: "Hello", Body: "Hi $ARGS",
	})
	if err != nil {
		t.Fatal(err)
	}
	if s.ID != "skill_x" {
		t.Errorf("id = %q", s.ID)
	}
	// Body shape — JSON keys match the server's installSkillReq.
	var sent installShapeAssert
	if err := json.Unmarshal([]byte(f.received[0].Body), &sent); err != nil {
		t.Fatal(err)
	}
	if sent.Identifier != "hello" || sent.Body != "Hi $ARGS" {
		t.Errorf("sent body wrong: %+v", sent)
	}
}

type installShapeAssert struct {
	Identifier string `json:"identifier"`
	Name       string `json:"name"`
	Body       string `json:"body"`
}

func TestUpdate_PartialBody(t *testing.T) {
	f := newFakeServer(t, []scriptedResponse{{
		status: 200,
		body:   `{"id":"skill_x","identifier":"hello","status":"active","content_hash":"after"}`,
	}})
	c := New(f.srv.URL, "tok")
	desc := "new desc"
	if _, err := c.Update(context.Background(), "skill_x", UpdateRequest{
		Description: &desc,
	}); err != nil {
		t.Fatal(err)
	}
	if f.received[0].Method != http.MethodPatch {
		t.Errorf("method = %s", f.received[0].Method)
	}
	if !strings.Contains(f.received[0].Body, `"description":"new desc"`) {
		t.Errorf("body should carry description: %s", f.received[0].Body)
	}
	// SetBody was nil so it must not appear on the wire.
	if strings.Contains(f.received[0].Body, `"body"`) {
		t.Errorf("body field should be omitted: %s", f.received[0].Body)
	}
}

func TestErrors_MapHTTPStatuses(t *testing.T) {
	cases := []struct {
		status int
		want   error
	}{
		{401, ErrUnauthorized},
		{404, ErrNotFound},
		{409, ErrConflict},
	}
	for _, c := range cases {
		f := newFakeServer(t, []scriptedResponse{{status: c.status, body: `{"error":{"code":"x","message":"y"}}`}})
		client := New(f.srv.URL, "tok")
		_, err := client.Get(context.Background(), "skill_x")
		if !errors.Is(err, c.want) {
			t.Errorf("status %d → err %v, want %v", c.status, err, c.want)
		}
	}
}

func TestDelete_ReturnsNilOnSuccess(t *testing.T) {
	f := newFakeServer(t, []scriptedResponse{{status: 200, body: `{"id":"skill_x"}`}})
	c := New(f.srv.URL, "tok")
	if err := c.Delete(context.Background(), "skill_x"); err != nil {
		t.Errorf("delete: %v", err)
	}
	if f.received[0].Method != http.MethodDelete {
		t.Errorf("method = %s", f.received[0].Method)
	}
}

func TestToggle_ShapeMatchesServer(t *testing.T) {
	f := newFakeServer(t, []scriptedResponse{{
		status: 200,
		body:   `{"agent_id":"00000000-0000-0000-0000-000000000001","skill_id":"skill_x","is_enabled":true,"pinned":true}`,
	}})
	c := New(f.srv.URL, "tok")
	as, err := c.Toggle(context.Background(), "skill_x", ToggleRequest{
		AgentID:   "00000000-0000-0000-0000-000000000001",
		IsEnabled: true,
		Pinned:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !as.IsEnabled || !as.Pinned {
		t.Errorf("toggle response wrong: %+v", as)
	}
	if !strings.Contains(f.received[0].Body, `"is_enabled":true`) {
		t.Errorf("toggle body missing is_enabled: %s", f.received[0].Body)
	}
}

func TestNoBaseURL_FriendlyError(t *testing.T) {
	c := New("", "tok")
	_, err := c.List(context.Background(), ListOptions{})
	if err == nil || !strings.Contains(err.Error(), "BIUMIND_RUNTIME_URL") {
		t.Errorf("want hint about runtime URL; got %v", err)
	}
}

func TestTimeoutPropagates(t *testing.T) {
	// Server hangs forever; client ctx timeout must surface.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
	}))
	defer srv.Close()
	c := New(srv.URL, "tok")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := c.List(ctx, ListOptions{})
	if err == nil {
		t.Error("expected timeout error")
	}
}
