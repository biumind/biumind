package installer

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ─── URL ────────────────────────────────────────────────────

func TestFromURL_HappyPath(t *testing.T) {
	body := `---
name: code-review
description: PR auto-review
version: 1.0.0
license: MIT
paths: ["**/*.go"]
permissions: ["sandbox.exec"]
---

# Body
Review carefully.
`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/markdown")
		_, _ = io.WriteString(w, body)
	}))
	defer srv.Close()

	got, err := FromURL(context.Background(), srv.URL+"/code-review/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	if got.Identifier != "code-review" || got.Description != "PR auto-review" {
		t.Errorf("frontmatter parse miss: %+v", got)
	}
	if got.Manifest.Version != "1.0.0" || got.Manifest.License != "MIT" {
		t.Errorf("manifest miss: %+v", got.Manifest)
	}
	if len(got.Paths) != 1 || got.Paths[0] != "**/*.go" {
		t.Errorf("paths miss: %v", got.Paths)
	}
	if got.Manifest.SourceURL == "" {
		t.Error("Manifest.SourceURL should auto-populate from fetch URL")
	}
	if !strings.Contains(got.Body, "Review carefully") {
		t.Errorf("body missing: %q", got.Body)
	}
}

func TestFromURL_RejectsNonHTTPSchemes(t *testing.T) {
	cases := []string{"file:///etc/passwd", "ftp://x.com/skill.md", "gopher://nope"}
	for _, u := range cases {
		t.Run(u, func(t *testing.T) {
			_, err := FromURL(context.Background(), u)
			if err == nil || !strings.Contains(err.Error(), "scheme") {
				t.Errorf("want scheme error, got %v", err)
			}
		})
	}
}

func TestFromURL_HTTPErrorSurfaces(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()
	_, err := FromURL(context.Background(), srv.URL+"/missing")
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("want 404 surface, got %v", err)
	}
}

func TestFromURL_RejectsOversized(t *testing.T) {
	big := strings.Repeat("x", MaxSkillMDBytes+1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "---\nname: big\ndescription: y\n---\n"+big)
	}))
	defer srv.Close()
	_, err := FromURL(context.Background(), srv.URL+"/big")
	if !errors.Is(err, ErrTooLarge) {
		t.Errorf("want ErrTooLarge, got %v", err)
	}
}

func TestFromURL_RejectsMissingRequiredFrontmatter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "no frontmatter at all\n")
	}))
	defer srv.Close()
	_, err := FromURL(context.Background(), srv.URL)
	if err == nil || !strings.Contains(err.Error(), "name") {
		t.Errorf("missing name should error, got %v", err)
	}
}

// ─── Zip ────────────────────────────────────────────────────

// makeZip builds a small in-memory zip with the given file map.
// Used so tests don't need an on-disk fixture.
func makeZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(w, body); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestFromZip_HappyPath(t *testing.T) {
	raw := makeZip(t, map[string]string{
		"SKILL.md": `---
name: bundle-test
description: tests bundle parsing
permissions: ["sandbox.exec"]
---

Body content.
`,
		"references/checklist.md": "- item 1\n- item 2\n",
		"scripts/run.sh":          "#!/bin/sh\necho hi\n",
	})
	got, err := FromZip(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.Identifier != "bundle-test" {
		t.Errorf("identifier = %q", got.Identifier)
	}
	if len(got.Resources) != 2 {
		t.Errorf("want 2 resources, got %d: %v", len(got.Resources), got.Resources)
	}
	if got.Resources["references/checklist.md"].MimeType != "text/markdown" {
		t.Errorf("mime guess wrong: %+v", got.Resources["references/checklist.md"])
	}
	if got.Resources["scripts/run.sh"].MimeType != "text/x-shellscript" {
		t.Errorf("mime guess wrong: %+v", got.Resources["scripts/run.sh"])
	}
	for path, meta := range got.Resources {
		if meta.Inline == "" {
			t.Errorf("inline empty for %s", path)
		}
		if meta.Sha256 == "" {
			t.Errorf("sha256 empty for %s", path)
		}
	}
}

func TestFromZip_RejectsNoSkillMD(t *testing.T) {
	raw := makeZip(t, map[string]string{
		"references/foo.md": "no skill here",
	})
	_, err := FromZip(raw)
	if err == nil || !strings.Contains(err.Error(), "no SKILL.md") {
		t.Errorf("want missing-skill error, got %v", err)
	}
}

func TestFromZip_RejectsPathTraversal(t *testing.T) {
	raw := makeZip(t, map[string]string{
		"SKILL.md":      "---\nname: x\ndescription: y\n---\n\nbody",
		"../etc/passwd": "evil",
	})
	_, err := FromZip(raw)
	if err == nil || !strings.Contains(err.Error(), "traversal") {
		t.Errorf("want traversal block, got %v", err)
	}
}

func TestFromZip_DropsUnknownPaths(t *testing.T) {
	// Files not under scripts/ references/ assets/ are silently
	// dropped — bundle convention is flat.
	raw := makeZip(t, map[string]string{
		"SKILL.md":            "---\nname: x\ndescription: y\n---\n\nbody",
		"random/foo.md":       "should be dropped",
		"top-level-stray.txt": "also dropped",
	})
	got, err := FromZip(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Resources) != 0 {
		t.Errorf("unknown paths leaked into resources: %v", got.Resources)
	}
}

func TestFromZip_RejectsOversizedResource(t *testing.T) {
	big := strings.Repeat("x", MaxResourceInlineBytes+1)
	raw := makeZip(t, map[string]string{
		"SKILL.md":          "---\nname: x\ndescription: y\n---\n\nbody",
		"references/big.md": big,
	})
	_, err := FromZip(raw)
	if !errors.Is(err, ErrTooLarge) {
		t.Errorf("want ErrTooLarge, got %v", err)
	}
}

func TestFromZip_RejectsOversizedZip(t *testing.T) {
	raw := make([]byte, MaxZipBytes+1)
	_, err := FromZip(raw)
	if !errors.Is(err, ErrTooLarge) {
		t.Errorf("want ErrTooLarge, got %v", err)
	}
}

func TestFromZip_LowercaseSkillMDAccepted(t *testing.T) {
	// Some packers normalise filenames; tolerate `skill.md`.
	raw := makeZip(t, map[string]string{
		"skill.md": "---\nname: lowercase\ndescription: y\n---\n\nbody",
	})
	got, err := FromZip(raw)
	if err != nil {
		t.Fatalf("lowercase skill.md should be accepted: %v", err)
	}
	if got.Identifier != "lowercase" {
		t.Errorf("identifier = %q", got.Identifier)
	}
}

// ─── frontmatter parser ─────────────────────────────────────

func TestParseSkillMD_RejectsMissingRequired(t *testing.T) {
	cases := map[string]string{
		"missing name":        "---\ndescription: y\n---\nbody",
		"missing description": "---\nname: x\n---\nbody",
		"empty name":          "---\nname:   \ndescription: y\n---\nbody",
		"no fence at all":     "no frontmatter",
	}
	for label, src := range cases {
		t.Run(label, func(t *testing.T) {
			_, err := parseSkillMD([]byte(src))
			if err == nil {
				t.Error("expected error")
			}
		})
	}
}

func TestParseSkillMD_PathsAndPermissions(t *testing.T) {
	src := []byte(`---
name: x
description: y
paths: ["src/**", "cmd/**"]
permissions: ["sandbox.exec", "wiki.read"]
author: Alice
version: 0.2.1
---

body
`)
	got, err := parseSkillMD(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Paths) != 2 || got.Paths[0] != "src/**" {
		t.Errorf("paths: %v", got.Paths)
	}
	if len(got.Permissions) != 2 || got.Permissions[1] != "wiki.read" {
		t.Errorf("perms: %v", got.Permissions)
	}
	if got.Manifest.Author.Name != "Alice" {
		t.Errorf("author: %+v", got.Manifest.Author)
	}
	if got.Manifest.Version != "0.2.1" {
		t.Errorf("version: %s", got.Manifest.Version)
	}
}

func TestParseList_ToleratesQuoting(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{`["a", "b"]`, 2},
		{`a, b, c`, 3},
		{`"a","b"`, 2},
		{``, 0},
		{`"only-one"`, 1},
	}
	for _, c := range cases {
		got := parseList(c.in)
		if len(got) != c.want {
			t.Errorf("parseList(%q) = %v (len %d), want len %d", c.in, got, len(got), c.want)
		}
	}
}

func TestGuessMime(t *testing.T) {
	cases := map[string]string{
		"foo.md":       "text/markdown",
		"foo.MARKDOWN": "text/markdown",
		"x.json":       "application/json",
		"x.sh":         "text/x-shellscript",
		"x.py":         "text/x-python",
		"x.bin":        "application/octet-stream",
		"":             "application/octet-stream",
	}
	for in, want := range cases {
		if got := guessMime(in); got != want {
			t.Errorf("guessMime(%q) = %q, want %q", in, got, want)
		}
	}
}

// Sanity: zip handler should not crash on a corrupt buffer.
func TestFromZip_CorruptInput(t *testing.T) {
	_, err := FromZip([]byte("not a zip"))
	if err == nil {
		t.Error("expected error on garbage bytes")
	}
	// Don't require a specific message — archive/zip's wording can shift.
	fmt.Println(err) // surface in -v output for debuggability
}
