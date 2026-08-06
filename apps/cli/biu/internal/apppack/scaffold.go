// Scaffolds for `biu app new`.
//
// Three templates mirror docs/samples/app_center/:
//   - minimal      backend-only, single action, no UI
//   - view_only    UI surface that wraps platform calls
//   - hybrid_full  full-featured (the rss / weather-tracker shape)
//
// Each template ships a manifest.yaml + (where applicable) a Go
// package skeleton. We embed at compile time so the CLI binary is
// self-contained and works offline.

package apppack

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

//go:embed templates/*
var templates embed.FS

// Templates lists the names usable with NewProject. Stable for CLI
// `--from <name>` flag; new entries land alongside the embedded
// fixture files under templates/.
func Templates() []string {
	return []string{"minimal", "view_only", "hybrid_full"}
}

// NewProject scaffolds a new App at destDir using the named template.
// Replaces every occurrence of "{{slug}}" inside text files (manifest,
// README, .biuapp.yaml) with the user-provided slug.
//
// Refuses to overwrite a non-empty destDir — better surface as a
// clear error than silently merge into someone's existing project.
func NewProject(destDir, slug, template string) error {
	if slug == "" {
		return errors.New("apppack: slug required")
	}
	if !validSlug(slug) {
		return fmt.Errorf("apppack: slug %q must be kebab-case (a-z 0-9 -)", slug)
	}
	tplDir := "templates/" + template
	if _, err := templates.ReadDir(tplDir); err != nil {
		return fmt.Errorf("apppack: unknown template %q (try: %s)",
			template, strings.Join(Templates(), ", "))
	}

	// destDir must be empty / non-existent.
	if entries, err := os.ReadDir(destDir); err == nil && len(entries) > 0 {
		return fmt.Errorf("apppack: %s is not empty", destDir)
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return err
	}

	return fs.WalkDir(templates, tplDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == tplDir {
			return nil
		}
		rel := strings.TrimPrefix(path, tplDir+"/")
		out := filepath.Join(destDir, rel)
		if d.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		raw, err := templates.ReadFile(path)
		if err != nil {
			return err
		}
		body := strings.ReplaceAll(string(raw), "{{slug}}", slug)
		// Embed YAML / Go templates as text — binary fixtures don't
		// need substitution. We swallow the substitution call for
		// binary types implicitly because they don't contain "{{slug}}".
		return os.WriteFile(out, []byte(body), 0o644)
	})
}

// validSlug mirrors biuapp.Validate's slug pattern: lower + digit
// + dash; first char must be letter. We intentionally don't accept
// the marketplace-scoped "<author>/<slug>" form here — `biu app new`
// creates a fresh project, the publish flow rewrites the identifier
// when the developer uploads.
func validSlug(s string) bool {
	if len(s) == 0 || s[0] < 'a' || s[0] > 'z' {
		return false
	}
	for i := 1; i < len(s); i++ {
		c := s[i]
		ok := (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-'
		if !ok {
			return false
		}
	}
	return true
}
