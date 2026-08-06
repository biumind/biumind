// Bundled plugins shipped inside the biu binary.
//
// Layout:
//
//	internal/plugins/bundled/
//	├── bundled.go             ← this file (embed.FS + extraction)
//	├── plugins/               ← embedded tree
//	│   ├── biu-debug/
//	│   │   ├── .claude-plugin/plugin.json
//	│   │   └── commands/biu-debug.md
//	│   ├── code-review/       (PP8c)
//	│   ├── feature-dev/       (PP8c)
//	│   └── …
//	├── hookify/               ← Go-rewrite hook plugins (PP8d)
//	├── ralph_loop/            (PP8d)
//	└── …
//
// Loader flow:
//
//  1. At startup, Materialise() unpacks the embed.FS into a stable
//     cache directory keyed on biu version
//     (~/.biumind/plugins/.bundled/<version>/) so subsequent runs
//     reuse the extracted tree rather than racing on every launch.
//
//  2. Roots() returns a SearchRoot for that cache dir, which the
//     wiring layer concatenates onto plugins.DefaultRoots(). Loaded
//     plugins carry Source=SrcBundled so /plugin list shows
//     bundled vs user installs distinctly.
//
//  3. Each PP8d plugin (hookify, ralph_loop, security_guidance,
//     explanatory_style, learning_style) is its own Go sub-package
//     imported below for its init() side-effect — that's where
//     hooks.RegisterInternal claims handler names referenced by
//     the plugin's hooks.json.
//
// Why extract to disk rather than make plugins.Load fs.FS-aware:
// the existing plugin loader resolves component paths to absolute
// strings the rest of biu (hooks runner, BashTool sandbox, agents
// dispatcher) consumes as actual filesystem paths. Teaching every
// downstream consumer about fs.FS would be a much bigger surgery
// than a one-time extract on startup. The extract is idempotent
// and a few KB per plugin — cheap.

package bundled

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"github.com/biumind/biumind/apps/cli/biu/internal/plugins"
)

//go:embed all:plugins
var bundledFS embed.FS

// VersionTag identifies the embedded payload. Defaults to a hash of
// the embedded tree so a fresh build automatically invalidates the
// stale cache; callers can override (e.g. set to biu's release
// version for human-readable cache paths).
//
// Computed lazily via versionTag() — go:embed doesn't expose its
// payload at init time, and we'd rather pay the hash once than at
// every call.
var (
	overrideVersion = ""
	versionOnce     sync.Once
	cachedVersion   string
)

// SetVersion lets the wiring layer override the cache key with the
// public biu version string. Useful so `~/.biumind/plugins/.bundled/`
// has paths like `v0.20.57/` instead of opaque hashes.
func SetVersion(v string) { overrideVersion = v }

// versionTag returns the active cache key. Idempotent and cached.
func versionTag() string {
	versionOnce.Do(func() {
		if overrideVersion != "" {
			cachedVersion = sanitiseTag(overrideVersion)
			return
		}
		// Fall back: SHA-256 over every embedded file path + content.
		// This makes the cache key stable for identical embeds and
		// distinct for any change.
		h := sha256.New()
		_ = fs.WalkDir(bundledFS, ".", func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			fmt.Fprintln(h, p)
			f, err := bundledFS.Open(p)
			if err != nil {
				return err
			}
			defer f.Close()
			if _, err := io.Copy(h, f); err != nil {
				return err
			}
			return nil
		})
		cachedVersion = hex.EncodeToString(h.Sum(nil))[:16]
	})
	return cachedVersion
}

// sanitiseTag strips path-unsafe characters from version strings
// (so a tag like "v0.1.0+local" becomes "v0.1.0-local"). Conservative
// — we don't want surprises in the cache directory naming.
func sanitiseTag(v string) string {
	out := make([]byte, 0, len(v))
	for i := 0; i < len(v); i++ {
		c := v[i]
		switch {
		case (c >= 'a' && c <= 'z') ||
			(c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') ||
			c == '.' || c == '_' || c == '-':
			out = append(out, c)
		default:
			out = append(out, '-')
		}
	}
	return string(out)
}

// Materialise unpacks the embedded plugin tree into a stable cache
// directory + returns the absolute path. Idempotent: if the directory
// already exists with the current version tag, returns immediately
// without re-writing files.
//
// Implementation: every distinct biu version gets its own subdirectory
// so concurrent biu invocations of different versions don't fight.
// On a re-extract for the same version, we check a sentinel file
// (`.materialised`) to avoid re-writing unchanged content; missing or
// older sentinel triggers a fresh extract.
//
// On extract failure (disk full, permission denied), Materialise
// returns the error — the caller logs it but bundled plugins are
// non-essential, so biu continues without them. Same posture as
// "user has no ~/.biumind/plugins/" today.
func Materialise() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve $HOME: %w", err)
	}
	root := filepath.Join(home, ".biumind", "plugins", ".bundled", versionTag())
	sentinel := filepath.Join(root, ".materialised")

	if _, err := os.Stat(sentinel); err == nil {
		return root, nil
	}

	// Re-extract from scratch — tear down the version dir if any
	// stale state survived (e.g. half-written from a previous crash).
	if err := os.RemoveAll(root); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}

	err = fs.WalkDir(bundledFS, "plugins", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// Strip the leading "plugins/" so the on-disk layout matches
		// the user-installed shape (plugin dirs at the top level).
		rel := p
		if rel == "plugins" {
			return nil
		}
		const prefix = "plugins/"
		if len(rel) > len(prefix) && rel[:len(prefix)] == prefix {
			rel = rel[len(prefix):]
		}
		dst := filepath.Join(root, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		data, err := bundledFS.ReadFile(p)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		return os.WriteFile(dst, data, 0o644)
	})
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(sentinel, []byte(versionTag()), 0o644); err != nil {
		return "", err
	}
	return root, nil
}

// Roots returns a SearchRoot list for the bundled tree. Empty slice
// when Materialise fails; callers normally never invoke this directly
// (the init() below registers it with plugins.RegisterRootsProvider
// so plugins.DefaultRoots picks it up automatically).
//
// Errors from Materialise are swallowed here on purpose — bundled
// plugins are convenience features, not required for biu to run, so
// we degrade silently rather than fail every DefaultRoots call. The
// wiring layer's BundledRootsErr() exposes the underlying error for
// startup diagnostics.
func Roots() []plugins.SearchRoot {
	dir, err := Materialise()
	if err != nil {
		recordMaterialiseErr(err)
		return nil
	}
	return []plugins.SearchRoot{
		{Path: dir, Source: plugins.SrcBundled},
	}
}

// materialiseErr captures the most recent extract failure so the
// wiring layer can log it once at startup. Mutex-protected because
// Roots() may be called from concurrent slash handlers.
var (
	materialiseErr   error
	materialiseErrMu sync.Mutex
)

func recordMaterialiseErr(err error) {
	materialiseErrMu.Lock()
	defer materialiseErrMu.Unlock()
	materialiseErr = err
}

// MaterialiseErr returns the last error from Materialise (or nil if
// extraction succeeded). Wiring uses this once at startup to print a
// "[biu] bundled plugins: …" stderr line.
func MaterialiseErr() error {
	materialiseErrMu.Lock()
	defer materialiseErrMu.Unlock()
	return materialiseErr
}

// init registers the bundled tree as a SearchRoot provider so
// plugins.DefaultRoots returns it automatically. Every call site
// that already uses DefaultRoots (slash handlers, biu plugin CLI,
// wiring) gains bundled discovery for free.
func init() {
	plugins.RegisterRootsProvider(Roots)
}
