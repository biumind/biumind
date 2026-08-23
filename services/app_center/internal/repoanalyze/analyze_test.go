package repoanalyze

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/biumind/biumind/packages/go-sdk/biu/biuapp"
)

// ─── scripted GitHub stub ────────────────────────────────────────────

type ghStub struct {
	owner, repo string

	repoStatus int    // default 200
	repoBody   string // raw JSON for /repos/{o}/{r}
	releaseTag string // "" → 404 on releases/latest
	tagsBody   string // raw JSON array for /tags; "" → "[]"
	headSHA    string // sha returned by /commits/{ref}
	files      map[string]string
}

func (s *ghStub) server(t *testing.T) *httptest.Server {
	t.Helper()
	prefix := "/repos/" + s.owner + "/" + s.repo
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == prefix:
			if s.repoStatus != 0 && s.repoStatus != 200 {
				w.WriteHeader(s.repoStatus)
				return
			}
			fmt.Fprint(w, s.repoBody)
		case r.URL.Path == prefix+"/releases/latest":
			if s.releaseTag == "" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			fmt.Fprintf(w, `{"tag_name":%q}`, s.releaseTag)
		case r.URL.Path == prefix+"/tags":
			if s.tagsBody != "" {
				fmt.Fprint(w, s.tagsBody)
				return
			}
			fmt.Fprint(w, `[]`)
		case strings.HasPrefix(r.URL.Path, prefix+"/commits/"):
			fmt.Fprintf(w, `{"sha":%q}`, s.headSHA)
		case strings.HasPrefix(r.URL.Path, prefix+"/contents/"):
			path := strings.TrimPrefix(r.URL.Path, prefix+"/contents/")
			content, ok := s.files[path]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				fmt.Fprint(w, `{"message":"Not Found"}`)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{
				"type":     "file",
				"encoding": "base64",
				"content":  base64.StdEncoding.EncodeToString([]byte(content)),
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func (s *ghStub) client(t *testing.T) (*Client, *httptest.Server) {
	t.Helper()
	srv := s.server(t)
	t.Cleanup(srv.Close)
	return NewClient(srv.URL, ""), srv
}

func repoJSON(desc, branch, license string, stars int, topics ...string) string {
	topicsJSON, _ := json.Marshal(topics)
	if license == "" {
		return fmt.Sprintf(`{"full_name":"o/r","description":%q,"default_branch":%q,`+
			`"license":null,"stargazers_count":%d,"topics":%s,"html_url":"https://github.com/o/r"}`,
			desc, branch, stars, topicsJSON)
	}
	return fmt.Sprintf(`{"full_name":"o/r","description":%q,"default_branch":%q,`+
		`"license":{"spdx_id":%q},"stargazers_count":%d,"topics":%s,"html_url":"https://github.com/o/r"}`,
		desc, branch, license, stars, topicsJSON)
}

// ─── URL parsing ─────────────────────────────────────────────────────

func TestParseRepoURL(t *testing.T) {
	valid := map[string][2]string{
		"https://github.com/gin-gonic/gin":     {"gin-gonic", "gin"},
		"https://github.com/gin-gonic/gin/":    {"gin-gonic", "gin"},
		"https://github.com/gin-gonic/gin.git": {"gin-gonic", "gin"},
		"http://www.github.com/o/r":            {"o", "r"},
		"gin-gonic/gin":                        {"gin-gonic", "gin"},
		"Foo_Bar/My.App":                       {"Foo_Bar", "My.App"},
		"https://github.com/o/r.git/":          {"o", "r"},
	}
	for in, want := range valid {
		owner, repo, err := ParseRepoURL(in)
		if err != nil {
			t.Errorf("ParseRepoURL(%q) err: %v", in, err)
			continue
		}
		if owner != want[0] || repo != want[1] {
			t.Errorf("ParseRepoURL(%q) = %s/%s, want %s/%s", in, owner, repo, want[0], want[1])
		}
	}

	invalid := []string{
		"", "https://gitlab.com/o/r", "https://github.com/onlyowner",
		"owner", "owner/repo/extra", "https://github.com/",
		"not a url",
	}
	for _, in := range invalid {
		if _, _, err := ParseRepoURL(in); !errors.Is(err, ErrInvalidRepoURL) {
			t.Errorf("ParseRepoURL(%q) err = %v, want ErrInvalidRepoURL", in, err)
		}
	}
}

// ─── version normalisation ───────────────────────────────────────────

func TestNormalizeVersion(t *testing.T) {
	cases := map[string]string{
		"v1.2":        "1.2.0",
		"1.2.3":       "1.2.3",
		"V2":          "2.0.0",
		"v1.2.3-rc.1": "1.2.3-rc.1",
		"1.2.3+build": "1.2.3",
		"v1.2.3.4":    "1.2.3",
		"release-x":   "0.1.0",
		"nightly":     "0.1.0",
	}
	for in, want := range cases {
		if got := normalizeVersion(in); got != want {
			t.Errorf("normalizeVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

// ─── env schema ──────────────────────────────────────────────────────

func TestParseEnvSchema(t *testing.T) {
	content := `
# 必填配置
OPENAI_API_KEY=   # OpenAI 密钥
PORT=8000
DATABASE_URL="postgres://localhost/db" # 数据库连接
DEBUG=false
export SESSION_SECRET='abc # not a comment'
EMPTY_LINE_ABOVE=

not-an-assignment
`
	fields := ParseEnvSchema(content)
	byName := map[string]EnvField{}
	for _, f := range fields {
		byName[f.Name] = f
	}

	key := byName["OPENAI_API_KEY"]
	if !key.Secret {
		t.Error("OPENAI_API_KEY should be Secret")
	}
	if key.Optional || key.Default != "" {
		t.Errorf("OPENAI_API_KEY should be required with no default, got %+v", key)
	}
	if key.Label != "OpenAI 密钥" {
		t.Errorf("OPENAI_API_KEY label = %q", key.Label)
	}

	port := byName["PORT"]
	if !port.System {
		t.Error("PORT should be System")
	}
	if port.Default != "8000" || !port.Optional {
		t.Errorf("PORT default/optional wrong: %+v", port)
	}

	db := byName["DATABASE_URL"]
	if db.Default != "postgres://localhost/db" || db.Label != "数据库连接" {
		t.Errorf("DATABASE_URL = %+v", db)
	}
	if db.Secret {
		t.Error("DATABASE_URL should not be Secret")
	}

	secret := byName["SESSION_SECRET"]
	if !secret.Secret || secret.Default != "abc # not a comment" {
		t.Errorf("SESSION_SECRET = %+v (quoted value must keep the #)", secret)
	}

	if _, ok := byName["not-an-assignment"]; ok {
		t.Error("non-assignment line should be skipped")
	}
	if _, ok := byName["EMPTY_LINE_ABOVE"]; !ok {
		t.Error("EMPTY_LINE_ABOVE should be parsed as required field")
	}
}

// ─── full analyze pipeline ───────────────────────────────────────────

func TestAnalyze_PythonMakeSetup(t *testing.T) {
	stub := &ghStub{
		owner: "o", repo: "r",
		repoBody: repoJSON("A python tool", "main", "AGPL-3.0", 42, "python", "productivity"),
		// no release, no tags → default branch HEAD
		headSHA: "deadbeefcafe",
		files: map[string]string{
			"requirements.txt": "flask==3.0.0\n",
			"Makefile":         "setup:\n\tpip install -e .\nrun:\n\tflask run\n",
			".env.example":     "API_KEY=\nPORT=8000\n",
		},
	}
	gh, _ := stub.client(t)

	res, err := Analyze(context.Background(), gh, "https://github.com/o/r")
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}

	if res.Stack.Kind != StackPython {
		t.Fatalf("stack kind = %q, want python", res.Stack.Kind)
	}
	if res.Stack.InstallCmd != "make setup" {
		t.Errorf("install cmd = %q, want make setup", res.Stack.InstallCmd)
	}
	if res.RepoMeta.LatestRef != "main" || res.RepoMeta.LatestSHA != "deadbeefcafe" {
		t.Errorf("repo meta ref/sha = %q/%q", res.RepoMeta.LatestRef, res.RepoMeta.LatestSHA)
	}
	if res.ManifestDraft.Version != "0.1.0" {
		t.Errorf("version = %q, want 0.1.0 (no release)", res.ManifestDraft.Version)
	}
	if len(res.Warnings) == 0 || !strings.Contains(res.Warnings[0], "AGPL") {
		t.Errorf("expected AGPL warning, got %v", res.Warnings)
	}
	if res.ManifestDraft.Category != "productivity" {
		t.Errorf("category = %q, want productivity", res.ManifestDraft.Category)
	}

	var apiKey *EnvField
	for i := range res.EnvSchema {
		if res.EnvSchema[i].Name == "API_KEY" {
			apiKey = &res.EnvSchema[i]
		}
	}
	if apiKey == nil || !apiKey.Secret {
		t.Errorf("env schema missing secret API_KEY: %+v", res.EnvSchema)
	}

	if err := biuapp.Validate(&res.ManifestDraft); err != nil {
		t.Errorf("manifest draft failed biuapp.Validate: %v", err)
	}
}

func TestAnalyze_NodeFrontend(t *testing.T) {
	stub := &ghStub{
		owner: "o", repo: "r",
		repoBody:   repoJSON("A web UI", "main", "MIT", 7, "dashboard"),
		releaseTag: "v1.2",
		tagsBody:   `[{"name":"v1.2","commit":{"sha":"abc123"}}]`,
		files: map[string]string{
			"package.json": `{
				"scripts": {"dev": "vite", "build": "vite build"},
				"engines": {"node": ">=20"},
				"packageManager": "pnpm@9.1.0"
			}`,
		},
	}
	gh, _ := stub.client(t)

	res, err := Analyze(context.Background(), gh, "o/r")
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}

	if res.Stack.Kind != StackNodeFrontend {
		t.Fatalf("stack kind = %q, want node_frontend", res.Stack.Kind)
	}
	if res.Stack.InstallCmd != "pnpm install" || res.Stack.StartCmd != "pnpm run build" {
		t.Errorf("cmds = %q / %q", res.Stack.InstallCmd, res.Stack.StartCmd)
	}
	if len(res.Stack.RuntimeReqs) != 1 || res.Stack.RuntimeReqs[0].Version != ">=20" {
		t.Errorf("runtime reqs = %+v", res.Stack.RuntimeReqs)
	}
	if res.ManifestDraft.Version != "1.2.0" {
		t.Errorf("version = %q, want 1.2.0", res.ManifestDraft.Version)
	}
	if res.RepoMeta.LatestRef != "v1.2" || res.RepoMeta.LatestSHA != "abc123" {
		t.Errorf("ref/sha = %q/%q", res.RepoMeta.LatestRef, res.RepoMeta.LatestSHA)
	}
	if len(res.Warnings) != 0 {
		t.Errorf("MIT repo should have no warnings, got %v", res.Warnings)
	}
	if res.ManifestDraft.Category != "data" {
		t.Errorf("category = %q, want data (dashboard topic)", res.ManifestDraft.Category)
	}
	if err := biuapp.Validate(&res.ManifestDraft); err != nil {
		t.Errorf("manifest draft failed biuapp.Validate: %v", err)
	}
}

func TestAnalyze_ComposeRejected(t *testing.T) {
	stub := &ghStub{
		owner: "o", repo: "r",
		repoBody:   repoJSON("multi-container app", "main", "", 3),
		releaseTag: "v2.0.0",
		tagsBody:   `[{"name":"v2.0.0","commit":{"sha":"fff"}}]`,
		files: map[string]string{
			"docker-compose.yml": "services:\n  web:\n    image: x\n",
			"package.json":       `{"scripts":{"start":"node index.js"}}`,
		},
	}
	gh, _ := stub.client(t)

	res, err := Analyze(context.Background(), gh, "o/r")
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if res.Stack.Kind != StackUnsupported {
		t.Fatalf("stack kind = %q, want unsupported", res.Stack.Kind)
	}
	if !strings.Contains(res.Stack.Reason, "compose") {
		t.Errorf("reason should mention compose: %q", res.Stack.Reason)
	}
	// No license declared → legal-risk warning.
	if len(res.Warnings) == 0 || !strings.Contains(res.Warnings[0], "许可证") {
		t.Errorf("expected no-license warning, got %v", res.Warnings)
	}
}

func TestAnalyze_RepoNotFound(t *testing.T) {
	stub := &ghStub{owner: "o", repo: "r", repoStatus: http.StatusNotFound}
	gh, _ := stub.client(t)

	_, err := Analyze(context.Background(), gh, "o/r")
	if !errors.Is(err, ErrRepoNotFound) {
		t.Fatalf("err = %v, want ErrRepoNotFound", err)
	}
}

func TestAnalyze_IdentifierSanitised(t *testing.T) {
	stub := &ghStub{
		owner: "Foo_Bar", repo: "My.App",
		repoBody: strings.Replace(
			repoJSON("weird name repo", "main", "MIT", 0),
			`"full_name":"o/r"`, `"full_name":"Foo_Bar/My.App"`, 1),
		releaseTag: "v0.3",
		tagsBody:   `[{"name":"v0.3","commit":{"sha":"99"}}]`,
		files:      map[string]string{"index.html": "<html></html>"},
	}
	gh, _ := stub.client(t)

	res, err := Analyze(context.Background(), gh, "Foo_Bar/My.App")
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	id := res.ManifestDraft.Identifier
	if !strings.HasPrefix(id, "gh-foo-bar-my-app-") {
		t.Errorf("identifier = %q, want gh-foo-bar-my-app-<hash8>", id)
	}
	if err := biuapp.Validate(&res.ManifestDraft); err != nil {
		t.Errorf("draft failed biuapp.Validate: %v", err)
	}
}

// ─── ETag conditional GET ────────────────────────────────────────────

func TestClient_NotModified(t *testing.T) {
	const etag = `"v1"`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("If-None-Match"); got != etag {
			t.Errorf("If-None-Match = %q, want %q", got, etag)
		}
		if got := r.Header.Get("User-Agent"); got != "BiuMind/1.0 (+repoanalyze)" {
			t.Errorf("User-Agent = %q", got)
		}
		w.WriteHeader(http.StatusNotModified)
	}))
	t.Cleanup(srv.Close)

	gh := NewClient(srv.URL, "")
	_, _, err := gh.Repo(context.Background(), "o", "r", etag)
	if !errors.Is(err, ErrNotModified) {
		t.Fatalf("err = %v, want ErrNotModified", err)
	}
}

func TestClient_TokenHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			t.Errorf("Authorization = %q", got)
		}
		w.Header().Set("ETag", `"e1"`)
		fmt.Fprint(w, `{"full_name":"o/r","default_branch":"main","license":null,"topics":[]}`)
	}))
	t.Cleanup(srv.Close)

	gh := NewClient(srv.URL, "tok")
	info, etag, err := gh.Repo(context.Background(), "o", "r", "")
	if err != nil {
		t.Fatalf("Repo: %v", err)
	}
	if etag != `"e1"` {
		t.Errorf("etag = %q", etag)
	}
	if info.DefaultBranch != "main" {
		t.Errorf("default branch = %q", info.DefaultBranch)
	}
}
