// .biuapp.yaml include / exclude glob expansion.
//
// Match semantics:
//   "manifest.yaml"   exact file
//   "skills/**"       recursive directory descent (zero or more
//                     path segments, including just "skills/foo.md")
//   "**/*_test.go"    any-depth match for the trailing pattern
//
// We use Go's filepath.Match for individual segments and add the
// `**` semantics ourselves — stdlib doesn't support it natively.

package apppack

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// IncludeSpec is the parsed form of .biuapp.yaml's include/exclude
// blocks. Caller reads the YAML; we just expand to a concrete
// relative-path list.
type IncludeSpec struct {
	Include []string `yaml:"include"`
	Exclude []string `yaml:"exclude"`
}

// Resolve walks sourceDir and returns the relative paths matched by
// `include` minus `exclude`. Result is sorted + deduped.
func Resolve(sourceDir string, spec IncludeSpec) ([]string, error) {
	if sourceDir == "" {
		return nil, errors.New("apppack: sourceDir required")
	}
	all := map[string]struct{}{}

	for _, pat := range spec.Include {
		matches, err := walkMatch(sourceDir, pat)
		if err != nil {
			return nil, fmt.Errorf("apppack: include pattern %q: %w", pat, err)
		}
		for _, m := range matches {
			all[m] = struct{}{}
		}
	}
	for _, pat := range spec.Exclude {
		matches, err := walkMatch(sourceDir, pat)
		if err != nil {
			// Bad exclude pattern is non-fatal — log via return shape
			// would over-engineer this. We simply skip the pattern.
			continue
		}
		for _, m := range matches {
			delete(all, m)
		}
	}
	out := make([]string, 0, len(all))
	for k := range all {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}

// walkMatch returns every regular file under sourceDir whose
// rel-path matches pattern. Handles `**` recursive globbing.
func walkMatch(sourceDir, pattern string) ([]string, error) {
	pattern = filepath.ToSlash(pattern)
	var out []string
	err := filepath.WalkDir(sourceDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Skip dot dirs (.git/.idea/etc.) automatically — saves
			// users from spamming exclude entries.
			if path != sourceDir && strings.HasPrefix(d.Name(), ".") {
				return fs.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(sourceDir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if matchGlob(pattern, rel) {
			out = append(out, rel)
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return out, nil
}

// matchGlob handles ** semantics on top of filepath.Match.
//
// Algorithm: if pattern doesn't contain "**", delegate to
// filepath.Match directly. Otherwise split on "**" and check that
// each chunk appears IN ORDER as a substring match — same approach
// .gitignore implementations use, and good enough for the file
// patterns Apps actually need (no `a/**/b/**/c` weirdness).
func matchGlob(pattern, name string) bool {
	if !strings.Contains(pattern, "**") {
		// "skills/foo" → exact-segment match via filepath.Match.
		ok, _ := filepath.Match(pattern, name)
		return ok
	}
	parts := strings.Split(pattern, "**")
	pos := 0
	for i, p := range parts {
		p = strings.TrimPrefix(p, "/")
		p = strings.TrimSuffix(p, "/")
		if p == "" {
			continue
		}
		// Find next match of p (with filepath.Match semantics) in
		// name starting at pos. We slide segment by segment.
		found := -1
		for j := pos; j <= len(name); j++ {
			// Try to match starting at every "/" boundary.
			if j == 0 || j == len(name) || name[j-1] == '/' {
				rest := name[j:]
				idx := strings.Index(rest, "/")
				var segment string
				if idx < 0 {
					segment = rest
				} else {
					segment = rest[:idx]
				}
				ok, _ := filepath.Match(p, segment)
				if ok {
					if i == len(parts)-1 && idx >= 0 {
						// Pattern ends here but more path segments follow:
						// that's only OK when this is the trailing **
						// (i.e. parts[len(parts)-1] == "").
						continue
					}
					found = j + len(segment)
					break
				}
			}
		}
		if found < 0 {
			return false
		}
		pos = found
	}
	return true
}
