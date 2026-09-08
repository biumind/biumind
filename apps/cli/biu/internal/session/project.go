// Project-directory encoding for session logs.
//
// Sessions are bucketed per launch directory:
//
//	~/.biu/sessions/<project-dir>/<session-id>.jsonl
//
// where <project-dir> is ProjectDir(cwd) — the launch cwd after
// symlink resolution, sanitised to a single safe path component.

package session

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// maxProjectDirLen bounds the sanitised component so deep paths stay
// under the 255-byte filesystem limit even after the hash suffix.
const maxProjectDirLen = 200

// ProjectDir encodes a launch cwd into the project subdirectory name.
// The path is made absolute, symlinks are resolved (macOS maps /tmp to
// /private/tmp — without this one project would split into two
// buckets), and Unicode is NFC-normalised before every
// non-[a-zA-Z0-9] byte becomes '-':
//
//	/Users/didi/workspaces/biu → -Users-didi-workspaces-biu
//
// Names longer than maxProjectDirLen are truncated and suffixed with a
// short sha256 of the full path so distinct deep paths can't collide.
// The encoding is intentionally not reversible — the original path is
// recoverable for display by stripping the sanitisation mentally, and
// uniqueness is what the bucket actually needs.
func ProjectDir(cwd string) string {
	if abs, err := filepath.Abs(cwd); err == nil {
		cwd = abs
	}
	if real, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = real
	}
	cwd = norm.NFC.String(cwd)

	var b strings.Builder
	b.Grow(len(cwd))
	for _, r := range cwd {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteByte(byte(r))
		default:
			b.WriteByte('-')
		}
	}
	name := b.String()
	if len(name) <= maxProjectDirLen {
		return name
	}
	sum := sha256.Sum256([]byte(cwd))
	return name[:maxProjectDirLen] + "-" + hex.EncodeToString(sum[:])[:8]
}
