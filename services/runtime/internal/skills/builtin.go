// Builtin (skills-stdlib) loader.
//
// On runtime start, walks a directory of SKILL.md files (defaults
// to /etc/biumind/skills-stdlib in container; configurable via env)
// and upserts each into runtime.skills as source='bundled',
// owner_id=NULL, status='active'. Org_id is the special bundled
// sentinel (the all-zeros UUID) so org-scoped queries naturally
// find them via the existing IncludeOrgShared loader path.
//
// Idempotency: each row keys on (org_id, identifier). Re-running
// the loader on an unchanged tree is a no-op (the upsert short-
// circuits when content_hash matches). Editing a SKILL.md and
// restarting picks up the new body.

package skills

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

// BundledOrgID is the sentinel org for skills shipped with the
// platform binary. Every real org's LoadForAgent(IncludeOrgShared=
// true) UNION-merges with this org's bundled set. We use the
// all-zero UUID so it's recognisable in logs / SQL dumps.
var BundledOrgID = uuid.UUID{}

// LoadBundled walks dir for <name>/SKILL.md files and upserts each
// into runtime.skills. Missing dir is not fatal — the runtime daemon
// can opt out by setting an empty path. Returns the count of skills
// upserted (created or updated) for startup logging.
func (r *Registry) LoadBundled(ctx context.Context, dir string) (int, error) {
	if dir == "" {
		return 0, nil
	}
	if _, err := os.Stat(dir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, fmt.Errorf("stat %s: %w", dir, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	var n int
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(dir, e.Name(), "SKILL.md")
		raw, err := os.ReadFile(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return n, fmt.Errorf("read %s: %w", path, err)
		}
		ident := e.Name()
		if err := r.upsertBundled(ctx, ident, raw); err != nil {
			return n, fmt.Errorf("upsert %s: %w", ident, err)
		}
		n++
	}
	return n, nil
}

// upsertBundled writes one bundled skill row. When the same
// (BundledOrgID, identifier) already exists with the same
// content_hash, it's a no-op — restarts after unchanged code don't
// churn updated_at.
func (r *Registry) upsertBundled(ctx context.Context, identifier string, raw []byte) error {
	front, body := splitFrontmatter(string(raw))
	// display_name wins when present (Chinese / pretty names);
	// otherwise fall back to the slug-shaped `name:` field, then to
	// the directory identifier. Mirrors installer.parseSkillMD.
	name := strings.TrimSpace(front["display_name"])
	if name == "" {
		name = strings.TrimSpace(front["name"])
	}
	if name == "" {
		name = identifier
	}
	desc := strings.TrimSpace(front["description"])
	contentHash := sha256Hex(string(raw))

	existing, err := r.GetByIdentifier(ctx, BundledOrgID, identifier)
	if err == nil && existing.ContentHash == contentHash {
		// Already up-to-date.
		return nil
	}

	manifest := Manifest{
		Version:    strings.TrimSpace(front["version"]),
		License:    strings.TrimSpace(front["license"]),
		Repository: strings.TrimSpace(front["repository"]),
		SourceURL:  strings.TrimSpace(front["source_url"]),
		Icon:       strings.TrimSpace(front["icon"]),
		Author:     ManifestAuthor{Name: strings.TrimSpace(front["author"])},
	}

	if err == nil {
		// Update existing row.
		_, err := r.Update(ctx, UpdateInput{
			ID:             existing.ID,
			Name:           name,
			SetName:        true,
			Description:    desc,
			SetDescription: true,
			Content:        strings.TrimSpace(body),
			SetContent:     true,
			Manifest:       manifest,
			SetManifest:    true,
			Paths:          parseListField(front["paths"]),
			SetPaths:       true,
			Permissions:    parseListField(front["permissions"]),
			SetPermissions: true,
		})
		return err
	}

	if !errors.Is(err, ErrNotFound) {
		return err
	}

	// Create fresh row. Bundled skills are NOT org-owned — owner_id
	// stays NULL so they show up via the LoadForAgent
	// IncludeOrgShared path for every org.
	_, err = r.Create(ctx, CreateInput{
		ID:          newBundledSkillID(identifier),
		OrgID:       BundledOrgID,
		Identifier:  identifier,
		Name:        name,
		Description: desc,
		Source:      SourceBundled,
		Manifest:    manifest,
		Content:     strings.TrimSpace(body),
		Paths:       parseListField(front["paths"]),
		Permissions: parseListField(front["permissions"]),
		Status:      StatusActive,
	})
	return err
}

// splitFrontmatter — lightweight YAML-ish parser (same shape as the
// CLI loader at apps/cli/biu/internal/skills.splitFrontmatter and the
// installer's helper). Re-implemented locally so the builtin loader
// has no cross-package dep on the CLI.
func splitFrontmatter(src string) (map[string]string, string) {
	out := map[string]string{}
	if !strings.HasPrefix(src, "---") {
		return out, src
	}
	rest := src[3:]
	idx := strings.Index(rest, "\n---")
	if idx == -1 {
		return out, src
	}
	frontText := strings.TrimSpace(rest[:idx])
	body := rest[idx+len("\n---"):]
	body = strings.TrimPrefix(body, "\n")
	for _, line := range strings.Split(frontText, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		colon := strings.Index(line, ":")
		if colon == -1 {
			continue
		}
		key := strings.TrimSpace(line[:colon])
		val := strings.TrimSpace(line[colon+1:])
		val = strings.Trim(val, `"'`)
		out[key] = val
	}
	return out, body
}

func parseListField(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	s = strings.TrimPrefix(s, "[")
	s = strings.TrimSuffix(s, "]")
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.Trim(strings.TrimSpace(p), `"'`)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func newBundledSkillID(identifier string) string {
	// Deterministic hash so re-running the loader doesn't churn
	// foreign-key references. v5 UUID over (BundledOrgID, identifier).
	sum := sha256.Sum256([]byte("bundled:" + identifier))
	return "skill_" + hex.EncodeToString(sum[:16])
}
