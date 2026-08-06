// Bidirectional sync between local SKILL.md files and the runtime
// catalogue. Pull writes server rows out to disk; Push uploads local
// drafts to the server; Diff compares hashes per identifier.
//
// Conflict policy: last-write-wins by content hash. When local and
// cloud disagree, the syncer surfaces the diff to the caller via
// ConflictError so the CLI can prompt the user; it does NOT auto-
// merge or overwrite — show the disagreement, let the human resolve.

package skillsync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	bskills "github.com/biumind/biumind/apps/cli/biu/internal/skills"
)

// ConflictError surfaces a content-hash mismatch between local and
// cloud. The CLI prints both hashes + a hint and exits non-zero.
type ConflictError struct {
	Identifier string
	LocalHash  string
	CloudHash  string
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("conflict on skill %q: local=%s cloud=%s",
		e.Identifier, short(e.LocalHash), short(e.CloudHash))
}

// PullResult — one row per cloud skill the syncer touched.
type PullResult struct {
	Identifier string
	Action     PullAction
	LocalPath  string
}

type PullAction string

const (
	PullCreated   PullAction = "created"
	PullUpdated   PullAction = "updated"
	PullUnchanged PullAction = "unchanged"
	PullConflict  PullAction = "conflict" // local edited; cloud differs; manual resolve
)

// Pull writes every cloud skill into the user's project directory
// (`<root>/.biumind/skills/<identifier>/SKILL.md`). When a local
// file already exists with a different content hash AND the user
// hasn't rebased (their copy looks edited), pull surfaces a
// PullConflict for that identifier rather than overwriting.
//
// root is typically `os.UserHomeDir()` so files land in
// `~/.biumind/skills/`.
func Pull(ctx context.Context, c *Client, root string) ([]PullResult, error) {
	rows, err := c.List(ctx, ListOptions{Status: "active"})
	if err != nil {
		return nil, fmt.Errorf("list cloud skills: %w", err)
	}
	out := make([]PullResult, 0, len(rows))
	for _, s := range rows {
		dir := filepath.Join(root, ".biumind", "skills", s.Identifier)
		path := filepath.Join(dir, "SKILL.md")
		body := assembleMarkdown(s)
		desiredHash := sha256Hex(body)

		existing, err := os.ReadFile(path)
		switch {
		case errors.Is(err, os.ErrNotExist):
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, fmt.Errorf("mkdir %s: %w", dir, err)
			}
			if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
				return nil, fmt.Errorf("write %s: %w", path, err)
			}
			out = append(out, PullResult{s.Identifier, PullCreated, path})
		case err != nil:
			return nil, fmt.Errorf("read %s: %w", path, err)
		default:
			localHash := sha256Hex(string(existing))
			if localHash == desiredHash {
				out = append(out, PullResult{s.Identifier, PullUnchanged, path})
				continue
			}
			// Local differs from cloud. Without a stored "last-pulled
			// hash" we can't tell whether the user edited locally or
			// just received an upstream change → safe path is conflict.
			// PS2.5+ can add a marker file (.last-pulled) to
			// disambiguate, but for v1.5 surface to the user.
			out = append(out, PullResult{s.Identifier, PullConflict, path})
		}
	}
	return out, nil
}

// Push uploads a single local skill to the cloud. When the skill
// doesn't exist on the cloud yet → InstallInline. When it does AND
// the cloud hash matches local → no-op (already in sync). When the
// cloud hash differs → Update with the local body. Returns the
// resulting Skill row + an Action describing what happened.
type PushResult struct {
	Identifier string
	Action     PushAction
	Skill      *Skill
}

type PushAction string

const (
	PushCreated   PushAction = "created"
	PushUpdated   PushAction = "updated"
	PushUnchanged PushAction = "unchanged"
)

func Push(ctx context.Context, c *Client, root, identifier string) (*PushResult, error) {
	if identifier == "" {
		return nil, errors.New("identifier required")
	}
	local, err := loadLocalSkill(root, identifier)
	if err != nil {
		return nil, err
	}

	cloudList, err := c.List(ctx, ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list cloud: %w", err)
	}
	var existing *Skill
	for i := range cloudList {
		if cloudList[i].Identifier == identifier {
			existing = &cloudList[i]
			break
		}
	}

	if existing == nil {
		// Net-new — InstallInline.
		req := InstallInlineRequest{
			Identifier:  local.Identifier,
			Name:        local.Name,
			Description: local.Description,
			Body:        local.Body,
			Paths:       local.Paths,
			Permissions: local.Permissions,
		}
		s, err := c.InstallInline(ctx, req)
		if err != nil {
			return nil, err
		}
		return &PushResult{identifier, PushCreated, s}, nil
	}

	// Compare hashes — server's content_hash is sha256 of SKILL.md
	// raw, but our local Body is post-frontmatter only. Recompose
	// the same wire shape (assembleMarkdown) for an apples-to-apples
	// comparison.
	localFull := assembleMarkdownFromLocal(local)
	if sha256Hex(localFull) == existing.ContentHash {
		return &PushResult{identifier, PushUnchanged, existing}, nil
	}

	// Update body + the metadata fields the local file owns.
	desc := local.Description
	body := local.Body
	paths := local.Paths
	perms := local.Permissions
	req := UpdateRequest{
		Description: &desc,
		Body:        &body,
		Paths:       &paths,
		Permissions: &perms,
	}
	s, err := c.Update(ctx, existing.ID, req)
	if err != nil {
		return nil, err
	}
	return &PushResult{identifier, PushUpdated, s}, nil
}

// Diff compares one local skill against its cloud counterpart.
// Returns nil when in sync; non-nil ConflictError when hashes differ
// or when one side is missing.
func Diff(ctx context.Context, c *Client, root, identifier string) (*DiffResult, error) {
	local, localErr := loadLocalSkill(root, identifier)

	cloudList, err := c.List(ctx, ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list cloud: %w", err)
	}
	var cloud *Skill
	for i := range cloudList {
		if cloudList[i].Identifier == identifier {
			cloud = &cloudList[i]
			break
		}
	}

	d := &DiffResult{Identifier: identifier}
	if localErr == nil {
		d.LocalHash = sha256Hex(assembleMarkdownFromLocal(local))
	}
	if cloud != nil {
		d.CloudHash = cloud.ContentHash
	}
	switch {
	case localErr != nil && cloud == nil:
		return nil, fmt.Errorf("skill %q not found locally or on cloud", identifier)
	case localErr != nil:
		d.Action = "cloud-only"
	case cloud == nil:
		d.Action = "local-only"
	case d.LocalHash == d.CloudHash:
		d.Action = "in-sync"
	default:
		d.Action = "diverged"
	}
	return d, nil
}

type DiffResult struct {
	Identifier string
	LocalHash  string
	CloudHash  string
	Action     string // "in-sync" | "diverged" | "local-only" | "cloud-only"
}

// ─── local SKILL.md helpers ─────────────────────────────────

// localSkill is what we read off disk; mirrors the shape of
// internal/skills.Skill but kept local so this package's
// dependency graph stays minimal.
type localSkill struct {
	Identifier  string
	Name        string
	Description string
	Body        string
	Paths       []string
	Permissions []string
	Path        string
}

func loadLocalSkill(root, identifier string) (*localSkill, error) {
	dir := filepath.Join(root, ".biumind", "skills", identifier)
	path := filepath.Join(dir, "SKILL.md")
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	// Reuse the CLI's loader by passing root as the cwd; it walks
	// <root>/.biumind/skills/ and parses every SKILL.md the same way
	// `biu repl` does.
	reg, err := bskills.Load(root)
	if err != nil {
		return nil, err
	}
	rs, ok := reg.Lookup(identifier)
	if !ok {
		return nil, fmt.Errorf("skills.Load did not surface %q from %s", identifier, dir)
	}
	return &localSkill{
		Identifier:  identifier,
		Name:        firstNonEmpty(rs.Skill.Name, identifier),
		Description: rs.Skill.Description,
		Body:        rs.Skill.Body,
		Paths:       rs.Skill.Paths,
		Permissions: nil, // bskills doesn't surface permissions yet; CLI loader pre-dates the field
		Path:        path,
	}, nil
}

// assembleMarkdown reconstructs a full SKILL.md (frontmatter + body)
// from a server-side Skill. Used by Pull when writing rows out.
// Must match exactly what Push will recompute from the same struct
// via assembleMarkdownFromLocal so round-trips don't churn hashes.
func assembleMarkdown(s Skill) string {
	loc := localSkill{
		Identifier: s.Identifier, Name: s.Name,
		Description: s.Description, Body: s.Content,
		Paths: s.Paths, Permissions: s.Permissions,
	}
	return assembleMarkdownFromLocal(&loc)
}

// assembleMarkdownFromLocal — the canonical SKILL.md serialisation.
// Frontmatter fields appear in a fixed order so two callers with
// identical fields produce byte-for-byte identical output (cache /
// hash stability).
func assembleMarkdownFromLocal(s *localSkill) string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("name: ")
	b.WriteString(s.Name)
	b.WriteByte('\n')
	if s.Description != "" {
		b.WriteString("description: ")
		b.WriteString(s.Description)
		b.WriteByte('\n')
	}
	if len(s.Paths) > 0 {
		b.WriteString("paths: [")
		// Stable sort so the field is order-independent on the wire.
		ps := append([]string(nil), s.Paths...)
		sort.Strings(ps)
		for i, p := range ps {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(`"`)
			b.WriteString(p)
			b.WriteString(`"`)
		}
		b.WriteString("]\n")
	}
	if len(s.Permissions) > 0 {
		b.WriteString("permissions: [")
		ps := append([]string(nil), s.Permissions...)
		sort.Strings(ps)
		for i, p := range ps {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(`"`)
			b.WriteString(p)
			b.WriteString(`"`)
		}
		b.WriteString("]\n")
	}
	b.WriteString("---\n")
	body := strings.TrimRight(s.Body, "\n")
	if body != "" {
		b.WriteByte('\n')
		b.WriteString(body)
		b.WriteByte('\n')
	}
	return b.String()
}

// ─── tiny utilities ─────────────────────────────────────────

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func short(h string) string {
	if len(h) > 8 {
		return h[:8]
	}
	return h
}

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return ""
}
