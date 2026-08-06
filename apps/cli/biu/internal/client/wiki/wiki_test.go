package wiki

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeWiki simulates the Brain.Wiki API for end-to-end tests.
type fakeWiki struct {
	projects []Project
	pages    []Page
	blocks   []Block
	authOK   string
}

func newFakeWiki(token string) *fakeWiki {
	return &fakeWiki{
		authOK:   token,
		projects: []Project{{ID: "11111111-1111-1111-1111-111111111111", Name: "Notes"}},
	}
}

func (f *fakeWiki) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/wiki/projects", func(w http.ResponseWriter, r *http.Request) {
		if !f.authed(r) {
			http.Error(w, "no auth", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"projects": f.projects})
	})
	mux.HandleFunc("POST /v1/wiki/projects", func(w http.ResponseWriter, r *http.Request) {
		if !f.authed(r) {
			http.Error(w, "no auth", http.StatusUnauthorized)
			return
		}
		var body struct {
			Name string `json:"name"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		p := Project{ID: "22222222-2222-2222-2222-222222222222", Name: body.Name}
		f.projects = append(f.projects, p)
		_ = json.NewEncoder(w).Encode(p)
	})
	mux.HandleFunc("POST /v1/wiki/projects/{pid}/pages", func(w http.ResponseWriter, r *http.Request) {
		if !f.authed(r) {
			http.Error(w, "no auth", http.StatusUnauthorized)
			return
		}
		pid := r.PathValue("pid")
		var body struct {
			Title    string `json:"title"`
			ParentID string `json:"parent_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		p := Page{ID: "page-" + body.Title, ProjectID: pid, Title: body.Title, Version: 1}
		f.pages = append(f.pages, p)
		_ = json.NewEncoder(w).Encode(p)
	})
	mux.HandleFunc("POST /v1/wiki/projects/{pid}/pages/{id}/blocks", func(w http.ResponseWriter, r *http.Request) {
		if !f.authed(r) {
			http.Error(w, "no auth", http.StatusUnauthorized)
			return
		}
		pageID := r.PathValue("id")
		var body struct {
			Type     string         `json:"type"`
			Position float64        `json:"position"`
			Content  map[string]any `json:"content"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		b := Block{
			ID: "block-" + r.PathValue("id"), PageID: pageID,
			Position: body.Position, Type: body.Type, Content: body.Content, Version: 1,
		}
		f.blocks = append(f.blocks, b)
		_ = json.NewEncoder(w).Encode(b)
	})
	return mux
}

func (f *fakeWiki) authed(r *http.Request) bool {
	if f.authOK == "" {
		return true
	}
	return r.Header.Get("Authorization") == "Bearer "+f.authOK
}

// ─── tests ───

func TestListProjects(t *testing.T) {
	f := newFakeWiki("tok")
	ts := httptest.NewServer(f.handler())
	defer ts.Close()
	c := New(ts.URL, "tok")
	ps, err := c.ListProjects(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 1 || ps[0].Name != "Notes" {
		t.Errorf("got %+v", ps)
	}
}

func TestCreateProjectPageBlock(t *testing.T) {
	f := newFakeWiki("tok")
	ts := httptest.NewServer(f.handler())
	defer ts.Close()
	c := New(ts.URL, "tok")
	ctx := context.Background()

	proj, err := c.CreateProject(ctx, "Research")
	if err != nil {
		t.Fatal(err)
	}
	page, err := c.CreatePage(ctx, proj.ID, CreatePageInput{Title: "Quantum"})
	if err != nil {
		t.Fatal(err)
	}
	for i, b := range []CreateBlockInput{
		{Type: "heading", Position: 1, Content: map[string]any{"text": "Overview"}},
		{Type: "text", Position: 2, Content: map[string]any{"text": "Photons are particles."}},
	} {
		got, err := c.CreateBlock(ctx, proj.ID, page.ID, b)
		if err != nil {
			t.Fatalf("block %d: %v", i, err)
		}
		if got.Type != b.Type {
			t.Errorf("block %d type = %q", i, got.Type)
		}
	}
	if len(f.blocks) != 2 {
		t.Errorf("server saw %d blocks", len(f.blocks))
	}
}

func TestUnauthorized(t *testing.T) {
	f := newFakeWiki("right-token")
	ts := httptest.NewServer(f.handler())
	defer ts.Close()
	c := New(ts.URL, "wrong-token")
	if _, err := c.ListProjects(context.Background()); err == nil {
		t.Fatal("expected error on bad token")
	} else if !strings.Contains(err.Error(), "401") {
		t.Errorf("err = %v", err)
	}
}

func TestResolveProjectByName(t *testing.T) {
	f := newFakeWiki("tok")
	f.projects = append(f.projects, Project{ID: "33333333-3333-3333-3333-333333333333", Name: "Research"})
	ts := httptest.NewServer(f.handler())
	defer ts.Close()
	c := New(ts.URL, "tok")
	p, err := c.ResolveProject(context.Background(), "research") // case-insensitive
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "Research" {
		t.Errorf("got %+v", p)
	}
}

func TestResolveProjectByID(t *testing.T) {
	f := newFakeWiki("tok")
	ts := httptest.NewServer(f.handler())
	defer ts.Close()
	c := New(ts.URL, "tok")
	p, err := c.ResolveProject(context.Background(), "11111111-1111-1111-1111-111111111111")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "Notes" {
		t.Errorf("got %+v", p)
	}
}

func TestResolveProjectMissing(t *testing.T) {
	f := newFakeWiki("tok")
	ts := httptest.NewServer(f.handler())
	defer ts.Close()
	c := New(ts.URL, "tok")
	if _, err := c.ResolveProject(context.Background(), "nope"); err == nil {
		t.Fatal("expected not found")
	}
}
