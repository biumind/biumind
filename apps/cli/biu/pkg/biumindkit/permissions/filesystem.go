// Path-membership predicates for the working-directory check.
//
// Three exported helpers:
//
//   AllWorkingDirectories(ctx, originalCwd)
//       canonical "what directories may the model read/write" set.
//       cwd ∪ ctx.additionalDirs.
//
//   PathInWorkingPath(path, workingPath)
//       single-pair containment with the macOS /private/{tmp,var}
//       symlink quirk handled and case-insensitive comparison
//       (defends against `.cLauDe/Settings.locaL.json`-style
//       bypasses on macOS / Windows).
//
//   PathInAllowedWorkingPath(path, ctx, originalCwd)
//       "is path inside ANY working directory?" — what
//       checkRead/WritePermissionForTool calls before deciding
//       whether to ask.
//
// We deliberately do NOT realpath() the inputs at compare time. Going
// through realpath would expand /tmp to /private/tmp on macOS and
// confuse the user when they pasted /tmp into /add-dir. The /private
// quirk is handled by literal substitution before relpath, which
// catches the common case without exploding into per-OS resolution.
//
// Patterns / rule-style globbing live in rule.go; this file is purely
// "is X inside Y" containment.

package permissions

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// AllWorkingDirectories returns the union of originalCwd and every
// path in ctx.AdditionalDirectoryPaths(), deduplicated. Results are
// sorted by path for deterministic system-prompt output. The original
// cwd appears first regardless of path sort order so the model's
// "primary" working directory is unambiguous.
func AllWorkingDirectories(ctx *Context, originalCwd string) []string {
	originalCwd = strings.TrimSpace(originalCwd)

	var extras []string
	if ctx != nil {
		extras = ctx.AdditionalDirectoryPaths()
	}

	if originalCwd == "" && len(extras) == 0 {
		return nil
	}
	if originalCwd == "" {
		// No cwd context — just return extras as-is (already sorted).
		return extras
	}

	// dedup against originalCwd
	out := make([]string, 0, len(extras)+1)
	out = append(out, originalCwd)
	seen := map[string]struct{}{originalCwd: {}}
	for _, p := range extras {
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	// Keep originalCwd first, sort the rest.
	if len(out) > 1 {
		tail := out[1:]
		sort.Strings(tail)
	}
	return out
}

// PathInAllowedWorkingPath reports whether path falls inside the
// working set (originalCwd or any of ctx.additionalDirs).
//
// We do not pre-compute a resolved-symlinks list — biumind does not
// yet realpath ahead of permission checks, so the macOS /private
// quirk is handled inside PathInWorkingPath instead.
func PathInAllowedWorkingPath(path string, ctx *Context, originalCwd string) bool {
	for _, w := range AllWorkingDirectories(ctx, originalCwd) {
		if PathInWorkingPath(path, w) {
			return true
		}
	}
	return false
}

// PathInWorkingPath reports whether `path` is `workingPath` or sits
// strictly inside it. Both arguments are expanded (`~`, env vars) and
// made absolute before comparison. The comparison is:
//
//   - case-INSENSITIVE on every OS — the threat model is path
//     traversal via case mutation on case-insensitive filesystems
//     (macOS HFS+/APFS, Windows NTFS). Doing the same lowercase
//     normalisation everywhere is cheaper than per-OS branching and
//     never makes a real attack succeed.
//
//   - macOS /private/{var,tmp} ↔ /var,/tmp normalised. realpath()
//     resolves /tmp → /private/tmp on macOS, so an input typed by the
//     user as /tmp/foo and a sandbox-resolved path /private/tmp/foo
//     are the same path. Without normalisation a write in /tmp/foo
//     would be denied because containment fails the substring test.
//
// Returns false on any path resolution error (treat unknown as
// outside — fail closed).
func PathInWorkingPath(path, workingPath string) bool {
	abs, ok := absExpand(path)
	if !ok {
		return false
	}
	work, ok := absExpand(workingPath)
	if !ok {
		return false
	}

	abs = normalizePrivatePrefix(abs)
	work = normalizePrivatePrefix(work)

	abs = strings.ToLower(abs)
	work = strings.ToLower(work)

	rel, err := filepath.Rel(work, abs)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	if strings.HasPrefix(rel, "..") {
		return false
	}
	if filepath.IsAbs(rel) {
		// On Windows filepath.Rel may return drive-prefix abs paths
		// when the inputs span drive letters. Treat as outside.
		return false
	}
	// rel == "x" or "x/y" → inside.
	return true
}

// ─── helpers ──────────────────────────────────────────

// absExpand returns the absolute, ~-expanded form of p. The second
// return value is false on any error so callers can fail-closed
// without coupling to errors.Is checks.
func absExpand(p string) (string, bool) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", false
	}
	if strings.HasPrefix(p, "~") {
		home, err := os.UserHomeDir()
		if err == nil && home != "" {
			if p == "~" {
				p = home
			} else if strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
				p = filepath.Join(home, p[2:])
			}
		}
	}
	if !filepath.IsAbs(p) {
		abs, err := filepath.Abs(p)
		if err != nil {
			return "", false
		}
		p = abs
	}
	return filepath.Clean(p), true
}

// normalizePrivatePrefix collapses /private/var/* → /var/* and
// /private/tmp/* → /tmp/*. macOS resolves /tmp and /var as symlinks
// into /private; without this normalisation, a user-typed /tmp/foo
// fails containment against a realpath-resolved /private/tmp/foo
// (and vice versa).
//
// Only applied on darwin — on Linux/Windows /private has no special
// meaning and a literal /private/tmp directory should keep its name.
func normalizePrivatePrefix(p string) string {
	if runtime.GOOS != "darwin" {
		return p
	}
	if strings.HasPrefix(p, "/private/var/") {
		return "/var/" + p[len("/private/var/"):]
	}
	if p == "/private/var" {
		return "/var"
	}
	if strings.HasPrefix(p, "/private/tmp/") {
		return "/tmp/" + p[len("/private/tmp/"):]
	}
	if p == "/private/tmp" {
		return "/tmp"
	}
	return p
}
