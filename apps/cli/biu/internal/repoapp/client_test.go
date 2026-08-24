package repoapp

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// scriptedReportServer records the requests it receives and answers
// from a script of (status, body) responses, mirroring the skillsync
// client tests so the wire format pins to the app_center contract.

type recordedReportReq struct {
	Method, Path, Body, Auth string
}

type reportServer struct {
	srv      *httptest.Server
	received []recordedReportReq
}

type scriptedResp struct {
	status int
	body   string
}

func newReportServer(t *testing.T, scripted ...scriptedResp) *reportServer {
	t.Helper()
	r := &reportServer{}
	idx := 0
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		r.received = append(r.received, recordedReportReq{
			Method: req.Method, Path: req.URL.Path,
			Body: string(body),
			Auth: req.Header.Get("Authorization"),
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
	t.Cleanup(r.srv.Close)
	return r
}

func TestCompleteBuild_HappyPath(t *testing.T) {
	f := newReportServer(t, scriptedResp{status: 200, body: `{"ok":true}`})

	c := NewReportClient(f.srv.URL+"/", "tok")
	err := c.CompleteBuild(context.Background(), "inst_1", "bld_2", BuildResult{
		Status: BuildStatusLive,
		SHA:    "0123456789abcdef",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.received) != 1 {
		t.Fatalf("want 1 request, got %d", len(f.received))
	}
	got := f.received[0]
	if got.Method != http.MethodPost {
		t.Errorf("method = %s", got.Method)
	}
	wantPath := "/v1/apps/installs/inst_1/builds/bld_2/complete"
	if got.Path != wantPath {
		t.Errorf("path = %q want %q", got.Path, wantPath)
	}
	if got.Auth != "Bearer tok" {
		t.Errorf("auth header = %q", got.Auth)
	}
	for _, frag := range []string{`"status":"live"`, `"sha":"0123456789abcdef"`, `"log_ref":""`} {
		if !strings.Contains(got.Body, frag) {
			t.Errorf("body missing %s: %s", frag, got.Body)
		}
	}
}

func TestCompleteBuild_FailedStatus(t *testing.T) {
	f := newReportServer(t, scriptedResp{status: 200, body: `{}`})
	c := NewReportClient(f.srv.URL, "tok")
	if err := c.CompleteBuild(context.Background(), "inst_1", "bld_2", BuildResult{
		Status: BuildStatusFailed,
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.received[0].Body, `"status":"failed"`) {
		t.Errorf("body should carry failed: %s", f.received[0].Body)
	}
}

func TestCompleteBuild_ErrorsMapHTTPStatuses(t *testing.T) {
	cases := []struct {
		status int
		want   error
	}{
		{401, ErrUnauthorized},
		{404, ErrNotFound},
		{409, ErrConflict},
	}
	for _, c := range cases {
		f := newReportServer(t, scriptedResp{status: c.status, body: `{"error":{"code":"x","message":"y"}}`})
		client := NewReportClient(f.srv.URL, "tok")
		err := client.CompleteBuild(context.Background(), "inst_1", "bld_2", BuildResult{Status: BuildStatusLive})
		if !errors.Is(err, c.want) {
			t.Errorf("status %d → err %v, want %v", c.status, err, c.want)
		}
	}
}

func TestCompleteBuild_5xxIsGenericError(t *testing.T) {
	f := newReportServer(t, scriptedResp{status: 500, body: `{"error":{"code":"internal","message":"boom"}}`})
	c := NewReportClient(f.srv.URL, "tok")
	err := c.CompleteBuild(context.Background(), "inst_1", "bld_2", BuildResult{Status: BuildStatusLive})
	if err == nil || !strings.Contains(err.Error(), "HTTP 500") {
		t.Errorf("want HTTP 500 error; got %v", err)
	}
}

func TestCompleteBuild_NoBaseURL(t *testing.T) {
	c := NewReportClient("", "tok")
	err := c.CompleteBuild(context.Background(), "inst_1", "bld_2", BuildResult{Status: BuildStatusLive})
	if err == nil || !strings.Contains(err.Error(), "--report-url") {
		t.Errorf("want hint about report URL; got %v", err)
	}
}
