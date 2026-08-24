package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/biumind/biumind/apps/cli/biu/internal/repoapp"
)

func TestRepoAppUpdateHasReportFlags(t *testing.T) {
	f := &rootFlags{}
	cmd := newRepoAppUpdateCmd(f)
	for _, name := range []string{"install-id", "build-id", "report-url"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("update missing --%s flag", name)
		}
	}
}

func TestResolveRepoAppReporter_DisabledWithoutIDs(t *testing.T) {
	f := &rootFlags{}
	r, on, err := resolveRepoAppReporter(context.Background(), f, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if on || r != nil {
		t.Errorf("plain local update must not report (on=%v r=%v)", on, r)
	}
}

func TestResolveRepoAppReporter_RequiresBothIDs(t *testing.T) {
	f := &rootFlags{}
	for _, pair := range [][2]string{{"inst_1", ""}, {"", "bld_2"}} {
		_, _, err := resolveRepoAppReporter(context.Background(), f, pair[0], pair[1], "http://example.com")
		if err == nil || !strings.Contains(err.Error(), "together") {
			t.Errorf("ids %v: want 'given together' error; got %v", pair, err)
		}
	}
}

func TestResolveRepoAppReporter_EndToEnd(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody repoapp.BuildResult
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	t.Setenv("BIUMIND_TOKEN", "tok-e2e")
	f := &rootFlags{}
	r, on, err := resolveRepoAppReporter(context.Background(), f, "inst_1", "bld_2", srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if !on {
		t.Fatal("reporting should be on when both ids are given")
	}
	if err := r.CompleteBuild(context.Background(), "inst_1", "bld_2", repoapp.BuildResult{
		Status: repoapp.BuildStatusLive, SHA: "abc123",
	}); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/v1/apps/installs/inst_1/builds/bld_2/complete" {
		t.Errorf("path = %q", gotPath)
	}
	if gotAuth != "Bearer tok-e2e" {
		t.Errorf("auth = %q", gotAuth)
	}
	if gotBody.Status != "live" || gotBody.SHA != "abc123" {
		t.Errorf("body = %+v", gotBody)
	}
}

// fakeReporter captures CompleteBuild calls; failErr makes them fail.
type fakeReporter struct {
	calls   []repoapp.BuildResult
	failErr error
}

func (f *fakeReporter) CompleteBuild(_ context.Context, installID, buildID string, result repoapp.BuildResult) error {
	f.calls = append(f.calls, result)
	return f.failErr
}

func TestReportRepoAppBuild_LiveOnSuccess(t *testing.T) {
	fr := &fakeReporter{}
	reportRepoAppBuild(context.Background(), fr, "inst_1", "bld_2", true, "newsha")
	if len(fr.calls) != 1 {
		t.Fatalf("want 1 call, got %d", len(fr.calls))
	}
	if fr.calls[0].Status != repoapp.BuildStatusLive || fr.calls[0].SHA != "newsha" {
		t.Errorf("result = %+v", fr.calls[0])
	}
}

func TestReportRepoAppBuild_FailedOnError(t *testing.T) {
	fr := &fakeReporter{}
	reportRepoAppBuild(context.Background(), fr, "inst_1", "bld_2", false, "")
	if len(fr.calls) != 1 || fr.calls[0].Status != repoapp.BuildStatusFailed {
		t.Errorf("calls = %+v", fr.calls)
	}
}

func TestReportRepoAppBuild_ReporterErrorIsBestEffort(t *testing.T) {
	fr := &fakeReporter{failErr: errors.New("server down")}
	// Must not panic / propagate — the update's exit code is unaffected.
	reportRepoAppBuild(context.Background(), fr, "inst_1", "bld_2", true, "sha")
}
