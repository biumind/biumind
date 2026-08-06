// Package plans manages on-disk plan files written by ExitPlanMode.
//
// Files live at ~/.biu/plans/<session-id>.md (or whatever path
// interactive.NewDiskPlanStore was constructed with). This package
// adds the read-side surface the CLI / REPL need:
//
//   * ListPlans  — newest-first directory walk + size + mtime
//   * FindByID   — resolve "latest" / partial-id / full-id
//   * Read       — load body + strip the auto-generated header
//   * Remove     — single id or bulk by mtime
//   * RemoveOlderThan — bulk cleanup
//
// Plans are keyed by session-id basename rather than a word slug so
// each plan collates next to its session log.

package plans

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Plan is the metadata + body returned by ListPlans / Read.
type Plan struct {
	ID        string    // basename without `.md`
	Path      string    // absolute path on disk
	Bytes     int64     // file size
	ModTime   time.Time // last write
	FirstLine string    // first non-blank, non-comment line — for table previews
}

// Dir resolves the standard plans directory (~/.biu/plans). Honours
// the BIU_PLANS_DIR env override so tests / CI can isolate state.
func Dir() (string, error) {
	if env := os.Getenv("BIU_PLANS_DIR"); env != "" {
		return env, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".biu", "plans"), nil
}

// ListPlans returns every .md file in dir, sorted newest-mtime first.
// Missing directory yields an empty slice without error so the caller
// can show a friendly "no plans yet" message.
func ListPlans(dir string) ([]Plan, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]Plan, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		full := filepath.Join(dir, e.Name())
		p := Plan{
			ID:      strings.TrimSuffix(e.Name(), ".md"),
			Path:    full,
			Bytes:   info.Size(),
			ModTime: info.ModTime(),
		}
		p.FirstLine = readFirstSignificantLine(full)
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ModTime.After(out[j].ModTime)
	})
	return out, nil
}

// FindByID resolves a plan reference, supporting:
//
//   - `latest` (alias for the newest plan)
//   - full id (`20260301-120000-abcd1234`)
//   - unambiguous prefix (`20260301`)
//
// Returns (Plan, true) on a unique hit; (zero, false) when missing
// or ambiguous. Callers handle the ambiguous case by listing.
func FindByID(dir, ref string) (Plan, bool) {
	rows, _ := ListPlans(dir)
	if len(rows) == 0 {
		return Plan{}, false
	}
	if ref == "" || ref == "latest" {
		return rows[0], true
	}
	// Exact match wins outright.
	for _, p := range rows {
		if p.ID == ref {
			return p, true
		}
	}
	// Otherwise look for a single prefix match.
	var matches []Plan
	for _, p := range rows {
		if strings.HasPrefix(p.ID, ref) {
			matches = append(matches, p)
		}
	}
	if len(matches) == 1 {
		return matches[0], true
	}
	return Plan{}, false
}

// Read returns the file body as a string, with the auto-generated
// `<!-- biu plan, written … -->` header stripped so callers see the
// plan content the model wrote.
func Read(p Plan) (string, error) {
	raw, err := os.ReadFile(p.Path)
	if err != nil {
		return "", err
	}
	body := string(raw)
	if idx := strings.Index(body, "-->"); idx > 0 && strings.HasPrefix(strings.TrimSpace(body), "<!--") {
		body = body[idx+3:]
	}
	return strings.TrimSpace(body) + "\n", nil
}

// Remove deletes one plan file.
func Remove(p Plan) error {
	return os.Remove(p.Path)
}

// RemoveOlderThan deletes every plan whose mtime is before
// time.Now() - cutoff. Returns the number of files removed +
// the first error encountered (best-effort: errors don't stop the
// loop, the caller decides).
func RemoveOlderThan(dir string, cutoff time.Duration) (int, error) {
	rows, err := ListPlans(dir)
	if err != nil {
		return 0, err
	}
	deadline := time.Now().Add(-cutoff)
	var firstErr error
	removed := 0
	for _, p := range rows {
		if !p.ModTime.Before(deadline) {
			continue
		}
		if rmErr := Remove(p); rmErr != nil {
			if firstErr == nil {
				firstErr = rmErr
			}
			continue
		}
		removed++
	}
	return removed, firstErr
}

// readFirstSignificantLine reads the first non-empty, non-comment
// line so ListPlans can show a one-line preview without slurping the
// whole file. Hard-cap at 4 KB read.
func readFirstSignificantLine(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	buf := make([]byte, 4096)
	n, _ := f.Read(buf)
	body := string(buf[:n])
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "<!--") {
			continue
		}
		// Strip leading markdown markers so the table looks clean.
		trimmed = strings.TrimLeft(trimmed, "#- *")
		trimmed = strings.TrimSpace(trimmed)
		if trimmed == "" {
			continue
		}
		// Cap to keep the table tidy.
		if len(trimmed) > 80 {
			trimmed = trimmed[:79] + "…"
		}
		return trimmed
	}
	return ""
}

// ParseDuration accepts the user-friendly spellings `30d`, `2w`, `4h`
// in addition to Go's stdlib formats. Used by `biu plan rm
// --older-than 30d`.
func ParseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("duration is required")
	}
	last := s[len(s)-1]
	switch last {
	case 'd':
		var n int
		_, err := fmt.Sscanf(s[:len(s)-1], "%d", &n)
		if err != nil || n < 0 {
			return 0, fmt.Errorf("bad duration %q", s)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	case 'w':
		var n int
		_, err := fmt.Sscanf(s[:len(s)-1], "%d", &n)
		if err != nil || n < 0 {
			return 0, fmt.Errorf("bad duration %q", s)
		}
		return time.Duration(n) * 7 * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}
