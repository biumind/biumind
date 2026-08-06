// Package skillpack builds and inspects deterministic .biuskill
// archives. The format is a ZIP because the runtime installer
// (services/runtime/internal/skills/installer.FromZip) already
// understands one — we don't introduce a second packaging shape.
//
// Why deterministic: PS4.4 will sign archive bytes with ed25519, and
// signatures only hold if Pack(dir) yields the exact same bytes for
// the same source tree. Three rules pin determinism:
//
//   1. Entries sorted lexicographically by archive path.
//   2. Modification time fixed to the Unix epoch.
//   3. Permission bits fixed to 0o644 for files (and 0o755 for dirs,
//      though we don't emit dir entries — every file is path-prefixed).
//
// Layout (mirrors installer.FromZip's accepted shape):
//
//   SKILL.md            (required, at archive root)
//   scripts/<file>      (optional; bash/python helpers)
//   references/<file>   (optional; supporting docs)
//   assets/<file>       (optional; templates / icons)
//
// Anything outside those three top-level dirs (and the SKILL.md root)
// is ignored at pack time, with a warning. Same rule as the installer
// — keeps "what the user packed" and "what the server accepts"
// identical.
package skillpack

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// MaxArchiveBytes mirrors installer.MaxZipBytes — the runtime would
// reject anything larger anyway, so fail at pack time with a clearer
// message than "server returned 413".
const MaxArchiveBytes = 8 * 1024 * 1024 // 8 MB

// epoch is the deterministic mtime stamped on every entry. The Go
// archive/zip writer rejects pre-1980 dates with an opaque error;
// 1980-01-01 UTC is the floor.
var epoch = time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)

// PackResult captures byte-level metadata so callers (CLI / tests /
// the future signing path) can render a human-readable summary
// without rehashing the archive.
type PackResult struct {
	Bytes      []byte // archive contents
	Sha256     string // hex digest of Bytes
	EntryCount int
	Skipped    []string // archive paths dropped by the layout filter
}

// Pack walks src, collects the supported entries, and produces a
// .biuskill archive. Returns ErrMissingSkillMD if SKILL.md isn't
// present at src root — caller should surface that as a packaging
// error, not a generic I/O failure.
func Pack(src string) (*PackResult, error) {
	st, err := os.Stat(src)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", src, err)
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", src)
	}

	type entry struct {
		archivePath string
		fsPath      string
	}
	var entries []entry
	var skipped []string

	walkErr := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		ap := filepath.ToSlash(rel)
		if !accept(ap) {
			skipped = append(skipped, ap)
			return nil
		}
		entries = append(entries, entry{archivePath: ap, fsPath: p})
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}

	// Required: SKILL.md at archive root.
	hasSkillMD := false
	for _, e := range entries {
		if e.archivePath == "SKILL.md" {
			hasSkillMD = true
			break
		}
	}
	if !hasSkillMD {
		return nil, ErrMissingSkillMD
	}

	// Determinism rule 1 — lexicographic order.
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].archivePath < entries[j].archivePath
	})

	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, e := range entries {
		body, err := os.ReadFile(e.fsPath)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", e.archivePath, err)
		}
		hdr := &zip.FileHeader{
			Name:     e.archivePath,
			Method:   zip.Deflate,
			Modified: epoch, // determinism rule 2
		}
		hdr.SetMode(0o644) // determinism rule 3
		fw, err := w.CreateHeader(hdr)
		if err != nil {
			return nil, fmt.Errorf("zip create %s: %w", e.archivePath, err)
		}
		if _, err := fw.Write(body); err != nil {
			return nil, fmt.Errorf("zip write %s: %w", e.archivePath, err)
		}
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("zip close: %w", err)
	}
	if buf.Len() > MaxArchiveBytes {
		return nil, fmt.Errorf("archive %d bytes exceeds %d-byte cap",
			buf.Len(), MaxArchiveBytes)
	}
	sum := sha256.Sum256(buf.Bytes())
	return &PackResult{
		Bytes:      buf.Bytes(),
		Sha256:     hex.EncodeToString(sum[:]),
		EntryCount: len(entries),
		Skipped:    skipped,
	}, nil
}

// Unpack reads a .biuskill archive and writes its contents to dst.
// Used by the CLI's biu skill unpack command for inspection / hand
// edits. Path traversal and absolute paths are rejected up front
// (same posture as the runtime installer).
func Unpack(raw []byte, dst string) (*UnpackResult, error) {
	if len(raw) > MaxArchiveBytes {
		return nil, fmt.Errorf("archive too large (%d > %d)",
			len(raw), MaxArchiveBytes)
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil, fmt.Errorf("open archive: %w", err)
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return nil, err
	}
	var written []string
	for _, f := range zr.File {
		if strings.Contains(f.Name, "..") || strings.HasPrefix(f.Name, "/") {
			return nil, fmt.Errorf("entry %q rejected (path traversal)", f.Name)
		}
		if f.FileInfo().IsDir() {
			continue
		}
		ap := filepath.ToSlash(filepath.Clean(f.Name))
		if !accept(ap) {
			// Same filter as Pack — stay symmetric so round-trip is
			// idempotent.
			continue
		}
		out := filepath.Join(dst, ap)
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return nil, err
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("open %s: %w", ap, err)
		}
		body, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(out, body, 0o644); err != nil {
			return nil, err
		}
		written = append(written, ap)
	}
	sort.Strings(written)
	sum := sha256.Sum256(raw)
	return &UnpackResult{
		Sha256:  hex.EncodeToString(sum[:]),
		Written: written,
	}, nil
}

// UnpackResult mirrors PackResult — Sha256 lets callers verify a
// previously signed archive was unmodified.
type UnpackResult struct {
	Sha256  string
	Written []string
}

// accept gates the archive layout — the runtime installer ignores
// anything outside SKILL.md + scripts/ + references/ + assets/, so we
// match here to avoid silently shipping bytes the server will drop.
func accept(archivePath string) bool {
	if archivePath == "SKILL.md" {
		return true
	}
	for _, prefix := range []string{"scripts/", "references/", "assets/"} {
		if strings.HasPrefix(archivePath, prefix) && archivePath != prefix {
			return true
		}
	}
	return false
}

// ErrMissingSkillMD — caller should print a friendly hint
// ("create SKILL.md at the directory root") rather than the raw error.
var ErrMissingSkillMD = errors.New("SKILL.md missing at directory root")
