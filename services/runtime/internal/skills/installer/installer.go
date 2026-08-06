// Package installer parses external skill packaging into the
// skills.CreateInput shape the runtime registry consumes. Two
// front-ends today, a third reserved for a Marketplace CAS path:
//
//	URL    — HTTPS fetch of a single SKILL.md. The simplest external
//	         install: copy a public skill from GitHub raw,
//	         skills.sh, or any plain HTTP source. No bundle / no
//	         resources — body only, captured into the row's content
//	         column verbatim.
//	Zip    — `.biuskill` bundle (zip with SKILL.md at the root +
//	         optional scripts/ references/ assets/ subdirs). Small
//	         text resources inline directly into the row's resources
//	         JSONB; binary or large (>4KB) resources are rejected
//	         in v1.5 — Files CAS upload lands in PS3.6.
//	CAS    — caller already uploaded a bundle to Files; pass its
//	         sha256. Reserved; not implemented here.
//
// Why a separate package: services/runtime/internal/api couldn't own
// the parsing without growing way past its HTTP-handler scope, and
// services/runtime/internal/skills shouldn't take a hard dep on
// archive/zip + net/http (it'd force every test that uses the
// registry to build with TLS roots wired). The installer package
// sits in between: depends on skills for types, depends on no I/O
// at the test boundary (its tests use httptest + in-memory zip
// buffers, no disk).
package installer

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"time"

	skillsreg "github.com/biumind/biumind/services/runtime/internal/skills"
)

const (
	// MaxSkillMDBytes caps SKILL.md body. Larger files almost
	// always belong in references/.
	MaxSkillMDBytes = 256 * 1024 // 256 KB
	// MaxResourceInlineBytes — anything past this rejects with
	// ErrTooLarge. The cap was 4 KB during PS3.6 because we expected
	// CAS upload to land soon; profiling real skills (skill-creator,
	// wiki, sandbox examples) showed 95% of resources are CSVs /
	// prompts / manifests under 64 KB, so we promote to 64 KB and
	// leave true-large bundles for the eventual CAS path. JSONB column
	// (postgres) handles megabyte-class blobs without trouble; the
	// real cost is sandbox argv when we materialise via shell prep,
	// and 64 KB base64 (~88 KB argv) is well under typical 2 MB
	// ARG_MAX budgets.
	MaxResourceInlineBytes = 64 * 1024 // 64 KB
	// MaxZipBytes is the absolute upper bound on a .biuskill
	// upload. Anything past this is almost certainly a packaging
	// mistake (or an attack); reject early so we don't unzip-bomb
	// ourselves.
	MaxZipBytes = 8 * 1024 * 1024 // 8 MB
	// urlFetchTimeout — short by design; an upstream SKILL.md
	// taking longer than this is broken or being slow on purpose.
	urlFetchTimeout = 15 * time.Second
)

// ErrTooLarge is returned for any size violation. The caller (HTTP
// handler) maps it to 413 Payload Too Large so the message reaches
// the user verbatim.
var ErrTooLarge = errors.New("input too large")

// ParsedSkill is the intermediate shape the installers produce. The
// API layer turns it into a skills.CreateInput by filling in IDs +
// org context (which the installer can't know).
type ParsedSkill struct {
	Identifier  string
	Name        string
	Description string
	Body        string
	Manifest    skillsreg.Manifest
	Paths       []string
	Permissions []string
	Resources   map[string]skillsreg.ResourceMeta

	// Provenance — populated only by URL/Zip flows; informational.
	SourceURL string
}

// ─── URL ────────────────────────────────────────────────────

// FromURL fetches a single SKILL.md over HTTPS and parses its
// frontmatter. The URL must scheme to https (http demoted to a
// hard error to keep prod traffic encrypted); the returned
// ParsedSkill carries SourceURL so the caller can populate
// manifest.source_url + Source=imported.
func FromURL(ctx context.Context, raw string) (*ParsedSkill, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse url: %w", err)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return nil, fmt.Errorf("unsupported url scheme %q (only http/https)", u.Scheme)
	}
	cli := &http.Client{Timeout: urlFetchTimeout}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/markdown, text/plain, */*")
	resp, err := cli.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", raw, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("fetch %s: HTTP %d", raw, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxSkillMDBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > MaxSkillMDBytes {
		return nil, fmt.Errorf("%w: SKILL.md > %d bytes", ErrTooLarge, MaxSkillMDBytes)
	}
	parsed, err := parseSkillMD(body)
	if err != nil {
		return nil, err
	}
	parsed.SourceURL = raw
	if parsed.Manifest.SourceURL == "" {
		parsed.Manifest.SourceURL = raw
	}
	return parsed, nil
}

// ─── Zip ────────────────────────────────────────────────────

// FromZip parses a .biuskill bundle from raw zip bytes. Layout:
//
//	SKILL.md                       (required, at zip root)
//	scripts/<file>                 (optional)
//	references/<file>              (optional)
//	assets/<file>                  (optional)
//
// Any file deeper than two segments is ignored (skill bundles are
// flat by convention; a deeper tree is almost always a packaging
// accident). Files exceeding MaxResourceInlineBytes are rejected
// in v1.5 — CAS upload lives in PS3.6.
func FromZip(raw []byte) (*ParsedSkill, error) {
	if len(raw) > MaxZipBytes {
		return nil, fmt.Errorf("%w: zip > %d bytes", ErrTooLarge, MaxZipBytes)
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil, fmt.Errorf("open zip: %w", err)
	}

	var (
		skillMD  []byte
		entries  = map[string]*zip.File{}
		zipNames = []string{}
	)
	for _, f := range zr.File {
		// Reject path traversal / absolute paths up front.
		if strings.Contains(f.Name, "..") || strings.HasPrefix(f.Name, "/") {
			return nil, fmt.Errorf("zip entry %q rejected (path traversal)", f.Name)
		}
		if f.FileInfo().IsDir() {
			continue
		}
		clean := filepath.ToSlash(filepath.Clean(f.Name))
		zipNames = append(zipNames, clean)
		switch {
		case clean == "SKILL.md" || strings.EqualFold(clean, "skill.md"):
			rc, err := f.Open()
			if err != nil {
				return nil, fmt.Errorf("open SKILL.md: %w", err)
			}
			body, err := io.ReadAll(io.LimitReader(rc, MaxSkillMDBytes+1))
			rc.Close()
			if err != nil {
				return nil, err
			}
			if len(body) > MaxSkillMDBytes {
				return nil, fmt.Errorf("%w: SKILL.md > %d bytes",
					ErrTooLarge, MaxSkillMDBytes)
			}
			skillMD = body
		case isAllowedResourcePath(clean):
			entries[clean] = f
		default:
			// Drop silently — flat layout convention. Caller can
			// resubmit with a stricter package if they care.
		}
	}
	if len(skillMD) == 0 {
		sort.Strings(zipNames)
		return nil, fmt.Errorf("zip contains no SKILL.md (entries: %v)", zipNames)
	}

	parsed, err := parseSkillMD(skillMD)
	if err != nil {
		return nil, err
	}
	res, err := readResources(entries)
	if err != nil {
		return nil, err
	}
	if parsed.Resources == nil {
		parsed.Resources = res
	} else {
		for k, v := range res {
			parsed.Resources[k] = v
		}
	}
	return parsed, nil
}

// isAllowedResourcePath gates the bundle layout: only top-level
// scripts/, references/, assets/ entries make it into resources.
func isAllowedResourcePath(p string) bool {
	for _, prefix := range []string{"scripts/", "references/", "assets/"} {
		if strings.HasPrefix(p, prefix) && p != prefix {
			return true
		}
	}
	return false
}

func readResources(entries map[string]*zip.File) (map[string]skillsreg.ResourceMeta, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	out := make(map[string]skillsreg.ResourceMeta, len(entries))
	for path, f := range entries {
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("open %s: %w", path, err)
		}
		body, err := io.ReadAll(io.LimitReader(rc, MaxResourceInlineBytes+1))
		rc.Close()
		if err != nil {
			return nil, err
		}
		if len(body) > MaxResourceInlineBytes {
			return nil, fmt.Errorf("%w: resource %q > %d bytes (true-large "+
				"resources need the Files CAS path; not yet wired)",
				ErrTooLarge, path, MaxResourceInlineBytes)
		}
		sum := sha256.Sum256(body)
		out[path] = skillsreg.ResourceMeta{
			Sha256:    hex.EncodeToString(sum[:]),
			SizeBytes: int64(len(body)),
			MimeType:  guessMime(path),
			Inline:    string(body),
		}
	}
	return out, nil
}

// ─── shared SKILL.md parser ─────────────────────────────────

// parseSkillMD turns a raw SKILL.md byte stream into a ParsedSkill.
// Frontmatter format mirrors apps/cli/biu/internal/skills.splitFrontmatter
// — same tolerance for missing-fence (returns empty map + full body
// rather than failing). Required fields surface as errors here so
// the runtime catalogue doesn't end up with anonymous rows; the CLI
// loader is laxer (falls back to dir name) because its consumers
// can recover.
func parseSkillMD(raw []byte) (*ParsedSkill, error) {
	front, body := splitFrontmatter(string(raw))
	identifier := strings.TrimSpace(front["name"])
	if identifier == "" {
		return nil, errors.New("SKILL.md missing required `name:` frontmatter")
	}
	desc := strings.TrimSpace(front["description"])
	if desc == "" {
		return nil, errors.New("SKILL.md missing required `description:` frontmatter")
	}
	parsed := &ParsedSkill{
		Identifier:  identifier,
		Name:        firstNonEmpty(front["display_name"], identifier),
		Description: desc,
		Body:        strings.TrimSpace(body),
		Paths:       parseList(front["paths"]),
		Permissions: parseList(front["permissions"]),
		Manifest: skillsreg.Manifest{
			Version:    strings.TrimSpace(front["version"]),
			License:    strings.TrimSpace(front["license"]),
			Repository: strings.TrimSpace(front["repository"]),
			SourceURL:  strings.TrimSpace(front["source_url"]),
			Icon:       strings.TrimSpace(front["icon"]),
			Author: skillsreg.ManifestAuthor{
				Name: strings.TrimSpace(front["author"]),
			},
		},
	}
	return parsed, nil
}

// splitFrontmatter is the local copy of the CLI loader's parser.
// We don't import bskills.splitFrontmatter because that function is
// lowercase / unexported and a public re-export would couple two
// otherwise-independent modules forever. It's small enough to dual-
// own; tests pin both copies.
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

func parseList(s string) []string {
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

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return ""
}

func guessMime(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".markdown":
		return "text/markdown"
	case ".json":
		return "application/json"
	case ".yml", ".yaml":
		return "application/yaml"
	case ".sh", ".bash":
		return "text/x-shellscript"
	case ".py":
		return "text/x-python"
	case ".txt":
		return "text/plain"
	}
	return "application/octet-stream"
}
