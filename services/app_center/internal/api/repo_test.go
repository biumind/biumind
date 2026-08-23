package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/biumind/biumind/services/app_center/internal/repoanalyze"
)

// stubGitHub serves the minimum GitHub API surface repoanalyze.Analyze
// consumes: repo meta, no releases, one tag, and 404 for every contents
// probe (no feature files → StackUnsupported, which is a nil-error
// result).
func stubGitHub(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/octocat/hello", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"full_name": "octocat/hello",
			"description": "greetings",
			"default_branch": "main",
			"license": {"spdx_id": "MIT"},
			"stargazers_count": 42,
			"topics": ["cli"],
			"html_url": "https://github.com/octocat/hello"
		}`))
	})
	mux.HandleFunc("/repos/octocat/hello/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	mux.HandleFunc("/repos/octocat/hello/tags", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"name":"v1.2.3","commit":{"sha":"deadbeef"}}]`))
	})
	mux.HandleFunc("/repos/octocat/hello/contents/", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func repoAuthReq(t *testing.T, method, path, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader([]byte(body)))
	req.Header.Set("Authorization", "Bearer "+mintToken(t))
	return req
}

func TestRepoAnalyze_HappyPath(t *testing.T) {
	srv := newSrv(t).WithRepoAnalyzer(repoanalyze.NewClient(stubGitHub(t).URL, ""))
	mux := http.NewServeMux()
	srv.Mount(mux)

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, repoAuthReq(t, "POST", "/v1/apps/repo/analyze",
		`{"repo_url":"https://github.com/octocat/hello"}`))
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d %s", rr.Code, rr.Body.String())
	}
	// The wire contract is snake_case throughout — pin the keys the
	// client parses.
	body := rr.Body.String()
	for _, key := range []string{`"manifest_draft"`, `"stack"`, `"repo_meta"`, `"latest_ref"`, `"latest_sha"`} {
		if !strings.Contains(body, key) {
			t.Errorf("response missing %s: %s", key, body)
		}
	}
	var out struct {
		RepoMeta struct {
			LatestRef string `json:"latest_ref"`
			LatestSHA string `json:"latest_sha"`
		} `json:"repo_meta"`
		ManifestDraft struct {
			Kind string `json:"kind"`
		} `json:"manifest_draft"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.RepoMeta.LatestRef != "v1.2.3" || out.RepoMeta.LatestSHA != "deadbeef" {
		t.Errorf("repo_meta = %+v", out.RepoMeta)
	}
	if out.ManifestDraft.Kind != "webview" {
		t.Errorf("manifest kind = %q, want webview", out.ManifestDraft.Kind)
	}
}

func TestRepoAnalyze_InvalidURL(t *testing.T) {
	srv := newSrv(t).WithRepoAnalyzer(repoanalyze.NewClient(stubGitHub(t).URL, ""))
	mux := http.NewServeMux()
	srv.Mount(mux)

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, repoAuthReq(t, "POST", "/v1/apps/repo/analyze",
		`{"repo_url":"https://gitlab.com/octocat/hello"}`))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "invalid_repo_url") {
		t.Errorf("want code=invalid_repo_url, got %s", rr.Body.String())
	}
}

func TestRepoAnalyze_NotFound(t *testing.T) {
	srv := newSrv(t).WithRepoAnalyzer(repoanalyze.NewClient(stubGitHub(t).URL, ""))
	mux := http.NewServeMux()
	srv.Mount(mux)

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, repoAuthReq(t, "POST", "/v1/apps/repo/analyze",
		`{"repo_url":"octocat/nope"}`))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "repo_not_found") {
		t.Errorf("want code=repo_not_found, got %s", rr.Body.String())
	}
}

func TestRepoAnalyze_DisabledWithoutClient(t *testing.T) {
	srv := newSrv(t) // no WithRepoAnalyzer
	mux := http.NewServeMux()
	srv.Mount(mux)

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, repoAuthReq(t, "POST", "/v1/apps/repo/analyze",
		`{"repo_url":"octocat/hello"}`))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "repo_disabled") {
		t.Errorf("want code=repo_disabled, got %s", rr.Body.String())
	}
}

// Stateless mode (no Installer / pool) must 503 the DB-backed repo
// endpoints; analyze is the only exemption.
func TestRepoEndpoints_Stateless503(t *testing.T) {
	srv := newSrv(t).WithRepoAnalyzer(repoanalyze.NewClient(stubGitHub(t).URL, ""))
	mux := http.NewServeMux()
	srv.Mount(mux)

	cases := []struct {
		method, path, body string
	}{
		{"POST", "/v1/apps/repo/installs", `{"repo_url":"octocat/hello","ref_type":"release"}`},
		{"GET", "/v1/apps/installs/some-id/runtime", ""},
		{"GET", "/v1/apps/installs/some-id/builds", ""},
		{"POST", "/v1/apps/installs/some-id/redeploy", ""},
	}
	for _, c := range cases {
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, repoAuthReq(t, c.method, c.path, c.body))
		if rr.Code != http.StatusServiceUnavailable {
			t.Errorf("%s %s: want 503, got %d %s", c.method, c.path, rr.Code, rr.Body.String())
		}
		if !strings.Contains(rr.Body.String(), "repo_disabled") {
			t.Errorf("%s %s: want code=repo_disabled, got %s", c.method, c.path, rr.Body.String())
		}
	}
}
