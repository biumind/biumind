package skillsync

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// End-to-end sync exercises against an httptest.Server scripting the
// runtime API. Pull / Push / Diff each get one happy path + the
// conflict path so the CLI command flow is pinned.

// scriptHandler accepts a router function and returns a server +
// the request log — same shape as client_test.go but shaped for the
// sync flow which makes multiple round-trips.
type scriptHandler struct {
	t        *testing.T
	skills   map[string]Skill // identifier → skill on the "cloud"
	received []recordedReq
	srv      *httptest.Server
}

func newScriptedRuntime(t *testing.T, initial []Skill) *scriptHandler {
	t.Helper()
	h := &scriptHandler{t: t, skills: map[string]Skill{}}
	for _, s := range initial {
		h.skills[s.Identifier] = s
	}
	h.srv = httptest.NewServer(http.HandlerFunc(h.handle))
	t.Cleanup(h.srv.Close)
	return h
}

func (h *scriptHandler) handle(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	h.received = append(h.received, recordedReq{
		Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery,
		Body: string(body), Auth: r.Header.Get("Authorization"),
	})
	w.Header().Set("Content-Type", "application/json")

	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/v1/skills":
		out := struct {
			Skills []Skill `json:"skills"`
		}{}
		for _, s := range h.skills {
			out.Skills = append(out.Skills, s)
		}
		_ = json.NewEncoder(w).Encode(out)

	case r.Method == http.MethodPost && r.URL.Path == "/v1/skills":
		var req InstallInlineRequest
		_ = json.Unmarshal(body, &req)
		s := Skill{
			ID: "skill_" + req.Identifier, Identifier: req.Identifier,
			Name: req.Name, Description: req.Description,
			Content: req.Body, Status: "active", Source: "user",
			ContentHash: hashFromInline(req),
		}
		h.skills[req.Identifier] = s
		_ = json.NewEncoder(w).Encode(s)

	case r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, "/v1/skills/"):
		id := strings.TrimPrefix(r.URL.Path, "/v1/skills/")
		var req UpdateRequest
		_ = json.Unmarshal(body, &req)
		// Find by ID.
		var found *Skill
		for k, v := range h.skills {
			if v.ID == id {
				cp := v
				found = &cp
				_ = k
				break
			}
		}
		if found == nil {
			http.NotFound(w, r)
			return
		}
		if req.Description != nil {
			found.Description = *req.Description
		}
		if req.Body != nil {
			found.Content = *req.Body
		}
		if req.Paths != nil {
			found.Paths = *req.Paths
		}
		if req.Permissions != nil {
			found.Permissions = *req.Permissions
		}
		// Recompute hash from the synthesised SKILL.md.
		found.ContentHash = sha256Hex(assembleMarkdown(*found))
		h.skills[found.Identifier] = *found
		_ = json.NewEncoder(w).Encode(found)

	default:
		http.Error(w, "no route: "+r.Method+" "+r.URL.Path, http.StatusNotFound)
	}
}

// hashFromInline mirrors what the real server does: hash the
// reassembled SKILL.md (frontmatter + body).
func hashFromInline(req InstallInlineRequest) string {
	loc := localSkill{
		Identifier: req.Identifier, Name: req.Name,
		Description: req.Description, Body: req.Body,
		Paths: req.Paths, Permissions: req.Permissions,
	}
	return sha256Hex(assembleMarkdownFromLocal(&loc))
}

// ─── Pull ────────────────────────────────────────────────────

func TestPull_CreatesLocalFiles(t *testing.T) {
	h := newScriptedRuntime(t, []Skill{
		{ID: "skill_a", Identifier: "alpha", Name: "Alpha",
			Description: "test", Content: "Body A", Status: "active"},
		{ID: "skill_b", Identifier: "beta", Name: "Beta",
			Description: "test", Content: "Body B", Status: "active"},
	})
	root := t.TempDir()
	c := New(h.srv.URL, "tok")
	res, err := Pull(context.Background(), c, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 {
		t.Fatalf("want 2 results, got %d", len(res))
	}
	for _, r := range res {
		if r.Action != PullCreated {
			t.Errorf("%s should be created, was %s", r.Identifier, r.Action)
		}
		if _, err := os.Stat(r.LocalPath); err != nil {
			t.Errorf("%s should exist on disk: %v", r.LocalPath, err)
		}
	}
}

func TestPull_UnchangedSkipsRewrite(t *testing.T) {
	h := newScriptedRuntime(t, []Skill{
		{ID: "skill_a", Identifier: "alpha", Name: "Alpha",
			Description: "x", Content: "Body", Status: "active"},
	})
	root := t.TempDir()
	c := New(h.srv.URL, "tok")
	if _, err := Pull(context.Background(), c, root); err != nil {
		t.Fatal(err)
	}
	res, err := Pull(context.Background(), c, root)
	if err != nil {
		t.Fatal(err)
	}
	if res[0].Action != PullUnchanged {
		t.Errorf("second pull should be unchanged; got %s", res[0].Action)
	}
}

func TestPull_DivergedSurfacesConflict(t *testing.T) {
	h := newScriptedRuntime(t, []Skill{
		{ID: "skill_a", Identifier: "alpha", Name: "Alpha",
			Description: "cloud", Content: "Cloud body", Status: "active"},
	})
	root := t.TempDir()
	dir := filepath.Join(root, ".biumind", "skills", "alpha")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Plant a divergent local copy.
	_ = os.WriteFile(filepath.Join(dir, "SKILL.md"),
		[]byte("---\nname: Alpha\ndescription: edited locally\n---\n\nLocal body\n"), 0o644)

	c := New(h.srv.URL, "tok")
	res, err := Pull(context.Background(), c, root)
	if err != nil {
		t.Fatal(err)
	}
	if res[0].Action != PullConflict {
		t.Errorf("expected conflict; got %s", res[0].Action)
	}
	// Local file should NOT have been clobbered.
	got, _ := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if !strings.Contains(string(got), "Local body") {
		t.Error("local body was overwritten despite conflict path")
	}
}

// ─── Push ────────────────────────────────────────────────────

func TestPush_NetNewCallsInstallInline(t *testing.T) {
	h := newScriptedRuntime(t, nil)
	root := t.TempDir()
	planLocal(t, root, "alpha", "alpha", "first push", "Body A", nil)

	c := New(h.srv.URL, "tok")
	res, err := Push(context.Background(), c, root, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if res.Action != PushCreated {
		t.Errorf("action = %s, want created", res.Action)
	}
	if _, ok := h.skills["alpha"]; !ok {
		t.Error("server didn't record the new skill")
	}
}

func TestPush_UnchangedSkipsUpdate(t *testing.T) {
	h := newScriptedRuntime(t, nil)
	root := t.TempDir()
	planLocal(t, root, "alpha", "alpha", "desc", "Body", nil)

	c := New(h.srv.URL, "tok")
	if _, err := Push(context.Background(), c, root, "alpha"); err != nil {
		t.Fatal(err)
	}
	res, err := Push(context.Background(), c, root, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if res.Action != PushUnchanged {
		t.Errorf("second push action = %s, want unchanged", res.Action)
	}
}

func TestPush_DivergedCallsUpdate(t *testing.T) {
	h := newScriptedRuntime(t, nil)
	root := t.TempDir()
	planLocal(t, root, "alpha", "alpha", "v1 desc", "v1 body", nil)

	c := New(h.srv.URL, "tok")
	if _, err := Push(context.Background(), c, root, "alpha"); err != nil {
		t.Fatal(err)
	}
	// Mutate local body — push should now Update.
	planLocal(t, root, "alpha", "alpha", "v1 desc", "v2 body — changed", nil)
	res, err := Push(context.Background(), c, root, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if res.Action != PushUpdated {
		t.Errorf("action = %s, want updated", res.Action)
	}
	if !strings.Contains(h.skills["alpha"].Content, "v2 body") {
		t.Error("server should hold v2 body after update push")
	}
}

// ─── Diff ────────────────────────────────────────────────────

func TestDiff_LocalOnly(t *testing.T) {
	h := newScriptedRuntime(t, nil)
	root := t.TempDir()
	planLocal(t, root, "alpha", "alpha", "x", "y", nil)
	c := New(h.srv.URL, "tok")
	d, err := Diff(context.Background(), c, root, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if d.Action != "local-only" {
		t.Errorf("action = %s, want local-only", d.Action)
	}
}

func TestDiff_CloudOnly(t *testing.T) {
	h := newScriptedRuntime(t, []Skill{
		{ID: "skill_a", Identifier: "alpha", Name: "Alpha",
			Description: "x", Content: "Body", Status: "active",
			ContentHash: "stub-hash"},
	})
	root := t.TempDir()
	c := New(h.srv.URL, "tok")
	d, err := Diff(context.Background(), c, root, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if d.Action != "cloud-only" {
		t.Errorf("action = %s, want cloud-only", d.Action)
	}
}

// ─── helper ──────────────────────────────────────────────────

func planLocal(t *testing.T, root, identifier, name, description, body string, paths []string) {
	t.Helper()
	dir := filepath.Join(root, ".biumind", "skills", identifier)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	loc := localSkill{
		Identifier: identifier, Name: name, Description: description,
		Body: body, Paths: paths,
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"),
		[]byte(assembleMarkdownFromLocal(&loc)), 0o644); err != nil {
		t.Fatal(err)
	}
}
