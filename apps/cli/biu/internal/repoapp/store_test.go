package repoapp

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseRepoArg(t *testing.T) {
	cases := []struct {
		arg      string
		wantSlug string
		wantURL  string
		wantErr  bool
	}{
		{"owner/repo", "owner-repo", "https://github.com/owner/repo.git", false},
		{"https://github.com/owner/repo", "owner-repo", "https://github.com/owner/repo.git", false},
		{"https://github.com/owner/repo.git", "owner-repo", "https://github.com/owner/repo.git", false},
		{"git@github.com:owner/repo.git", "owner-repo", "https://github.com/owner/repo.git", false},
		{"https://github.com/my-org/my.repo", "my-org-my-repo", "https://github.com/my-org/my.repo.git", false},
		{"https://gitlab.com/owner/repo", "", "", true},           // non-github host
		{"https://github.com/owner/repo/tree/main", "", "", true}, // deep link, not a repo root
		{"not-a-repo", "", "", true},
		{"", "", "", true},
	}
	for _, c := range cases {
		slug, cloneURL, err := ParseRepoArg(c.arg)
		if c.wantErr {
			if err == nil {
				t.Errorf("ParseRepoArg(%q) expected error, got slug=%q url=%q", c.arg, slug, cloneURL)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseRepoArg(%q) unexpected error: %v", c.arg, err)
			continue
		}
		if slug != c.wantSlug || cloneURL != c.wantURL {
			t.Errorf("ParseRepoArg(%q) = (%q,%q) want (%q,%q)", c.arg, slug, cloneURL, c.wantSlug, c.wantURL)
		}
	}
}

func TestSanitiseForFS(t *testing.T) {
	cases := map[string]string{
		"owner/repo":      "owner-repo",
		"a//b///c":        "a-b-c",
		"--lead-trail--":  "lead-trail",
		"under_score-ok":  "under_score-ok",
		"spaces and.dots": "spaces-and-dots",
	}
	for in, want := range cases {
		if got := sanitiseForFS(in); got != want {
			t.Errorf("sanitiseForFS(%q) = %q want %q", in, got, want)
		}
	}
}

func TestStoreCreateLayoutAndEnvPerms(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	inst, err := store.Create("owner-repo")
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{inst.Dir, inst.DataDir(), filepath.Dir(inst.LogPath())} {
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			t.Errorf("expected dir %s: %v", dir, err)
		}
	}
	// .env must exist and be 0600 from the start (plaintext-secrets
	// contract, TechPlan §3.5 D9).
	info, err := os.Stat(inst.EnvPath())
	if err != nil {
		t.Fatalf(".env missing: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf(".env mode = %o want 600", info.Mode().Perm())
	}
}

func TestRuntimeRoundtripAndList(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	inst, err := store.Create("a-b")
	if err != nil {
		t.Fatal(err)
	}
	want := &RuntimeInfo{
		RepoURL:      "https://github.com/a/b.git",
		Ref:          "v1.2.3",
		InstalledSHA: "deadbeef",
		Stack:        "python",
		StartCmd:     "python app.py",
		Port:         51234,
		HealthPath:   "/health",
		PathExtra:    []string{"/x/bin"},
		UpdatedAt:    time.Now().Truncate(time.Second),
	}
	if err := SaveRuntime(inst.Dir, want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadRuntime(inst.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.RepoURL != want.RepoURL || got.Ref != want.Ref || got.InstalledSHA != want.InstalledSHA ||
		got.Stack != want.Stack || got.StartCmd != want.StartCmd || got.Port != want.Port ||
		got.HealthPath != want.HealthPath || len(got.PathExtra) != 1 || got.PathExtra[0] != "/x/bin" {
		t.Errorf("roundtrip mismatch: got %+v", got)
	}
	if got.EffectiveHealthPath() != "/health" {
		t.Errorf("EffectiveHealthPath = %q", got.EffectiveHealthPath())
	}
	want.HealthPath = ""
	if want.EffectiveHealthPath() != "/" {
		t.Error("empty health_path must default to /")
	}

	if !store.Exists("a-b") {
		t.Error("Exists should be true after SaveRuntime")
	}
	instances, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(instances) != 1 || instances[0].Slug != "a-b" {
		t.Errorf("List = %+v", instances)
	}

	// A directory without runtime.json is not an instance.
	if err := os.MkdirAll(filepath.Join(store.Root, "stray"), 0o755); err != nil {
		t.Fatal(err)
	}
	instances, _ = store.List()
	if len(instances) != 1 {
		t.Errorf("stray dir must not be listed: %+v", instances)
	}
}

func TestStoreRemove(t *testing.T) {
	store, _ := NewStore(t.TempDir())
	inst, _ := store.Create("x-y")
	if err := SaveRuntime(inst.Dir, &RuntimeInfo{}); err != nil {
		t.Fatal(err)
	}
	if err := store.Remove("x-y"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(inst.Dir); !os.IsNotExist(err) {
		t.Error("instance dir should be gone")
	}
}
