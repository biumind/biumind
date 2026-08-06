// Marketplace: a manifest listing many plugins, fetched from a
// single URL / git repo / local path.
//
// Plugins themselves are still individual directories with their own
// plugin.json — a marketplace is just a router that maps logical
// names (`code-review`) to concrete sources (a git tag, a tarball
// URL, a local path). One marketplace can list dozens of plugins;
// users add a marketplace once and then install any plugin from it
// by `biu plugin install <plugin>@<marketplace>`.
//
// Wire format mirrors the upstream marketplace.json so the same file
// works across ecosystems. Three source types in this PR:
//
//	local: { "type": "local", "path": "/abs/or/relative/path" }
//	git:   { "type": "git", "repo": "https://github.com/x/y", "ref": "v1" }
//	url:   { "type": "url", "url": "https://example.com/plugin.tar.gz" }
//
// Signature support: a marketplace.json may ship with a sibling
// .sig file (base64 ed25519). Trust is bring-your-own-key — biu
// has no central root of trust. Users opt in by recording the
// public-key fingerprint when adding the marketplace; subsequent
// fetches verify against that pinned key. Without a pinned key,
// fetches succeed but a warning surfaces.
package plugins

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/biumind/biumind/apps/cli/biu/internal/skillpack"
)

// Marketplace is the parsed marketplace.json. Same JSON shape as the
// per-plugin manifest at the metadata level (name / version /
// description / author) so users can read both with a single mental
// model — but with a `plugins` array on top.
type Marketplace struct {
	// Name is the local handle users type after `@` to install:
	//   biu plugin install code-review@biumind-official
	// Lowercase letters / digits / hyphens / underscores. We keep the
	// rules slightly looser than plugin names because marketplace
	// names are user-facing handles, not URL components.
	Name string `json:"name"`

	Version     string `json:"version,omitempty"`
	Description string `json:"description,omitempty"`
	Owner       Author `json:"owner,omitempty"`

	Plugins []MarketplacePlugin `json:"plugins"`
}

// MarketplacePlugin is one entry in a marketplace's `plugins` array:
// a plugin name plus where to fetch it.
type MarketplacePlugin struct {
	Name        string       `json:"name"`
	Description string       `json:"description,omitempty"`
	Version     string       `json:"version,omitempty"`
	Source      PluginSource `json:"source"`
	// PinnedKey is an optional ed25519 public-key fingerprint the
	// marketplace publisher recommends for verifying this plugin's
	// signed payload. Format: "ed25519:<base64-spki-sha256>". biu
	// does not currently auto-trust based on this field — it surfaces
	// for the user to copy into their pinned-key store.
	PinnedKey string `json:"pinnedKey,omitempty"`
}

// PluginSource is a discriminated union over the supported delivery
// mechanisms. Type is the discriminator; every other field is set
// only for the matching type and ignored otherwise. JSON serialises
// as a flat object so the wire format stays human-editable.
type PluginSource struct {
	Type string `json:"type"` // "local" | "git" | "url"

	// local
	Path string `json:"path,omitempty"`

	// git — clone URL + optional branch / tag / sha
	Repo string `json:"repo,omitempty"`
	Ref  string `json:"ref,omitempty"` // branch, tag, or sha; empty → default branch

	// url — direct fetch of an unpacked plugin tree (today: only
	// supports a directory served as a tar.gz; PR-bounded scope.
	// Plain HTTPS file fetches are PP7's deferred work)
	URL string `json:"url,omitempty"`
}

var (
	// ErrMarketplaceInvalid wraps schema failures so callers can
	// errors.Is-check.
	ErrMarketplaceInvalid = errors.New("marketplace manifest invalid")
	// ErrMarketplacePluginNotFound — the `plugin@marketplace`
	// reference resolved but the marketplace doesn't list that
	// plugin.
	ErrMarketplacePluginNotFound = errors.New("plugin not found in marketplace")
	// ErrMarketplaceSourceUnsupported — the marketplace listed a
	// source.type biu doesn't yet handle. PR-scoped: today only
	// "local" and "git" are guaranteed; "url" lands as a fail-safe
	// stub when first encountered.
	ErrMarketplaceSourceUnsupported = errors.New("plugin source type not supported")
	// ErrMarketplaceSignature — the marketplace signature didn't
	// match the pinned key.
	ErrMarketplaceSignature = errors.New("marketplace signature verification failed")
)

var (
	marketplaceNameRE = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9_-]{0,62}[a-z0-9])?$`)
	// pluginAtMarketplaceRE accepts `<plugin>@<marketplace>` where
	// each side independently matches its naming rule. The ^/$
	// anchors prevent partial matches from creeping through.
	pluginAtMarketplaceRE = regexp.MustCompile(`^([a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?)@([a-z0-9](?:[a-z0-9_-]{0,62}[a-z0-9])?)$`)
)

// ParseMarketplaceBytes decodes + validates a marketplace.json blob.
// Same posture as ParseManifestBytes — multiple validation issues
// batch into one error so authors fix everything in one pass.
func ParseMarketplaceBytes(data []byte) (*Marketplace, error) {
	var mp Marketplace
	if err := json.Unmarshal(data, &mp); err != nil {
		return nil, fmt.Errorf("parse marketplace.json: %w", err)
	}
	if err := mp.Validate(); err != nil {
		return nil, err
	}
	return &mp, nil
}

// Validate checks a marketplace's schema. Three classes of error:
// missing required fields, malformed names, and unsupported source
// types per plugin. Per-plugin errors are flat-listed (not nested)
// for readability.
func (m *Marketplace) Validate() error {
	v := &ValidationError{}
	if m.Name == "" {
		v.add("name", "required")
	} else if !marketplaceNameRE.MatchString(m.Name) {
		v.add("name", "must match ^[a-z0-9](?:[a-z0-9_-]{0,62}[a-z0-9])?$")
	}
	if len(m.Plugins) == 0 {
		v.add("plugins", "must list at least one plugin")
	}
	seen := map[string]bool{}
	for i, p := range m.Plugins {
		prefix := fmt.Sprintf("plugins[%d]", i)
		if p.Name == "" {
			v.add(prefix+".name", "required")
		} else if !nameRE.MatchString(p.Name) {
			v.add(prefix+".name", fmt.Sprintf("invalid plugin name %q", p.Name))
		} else if seen[p.Name] {
			v.add(prefix+".name", fmt.Sprintf("duplicate plugin %q", p.Name))
		} else {
			seen[p.Name] = true
		}
		switch p.Source.Type {
		case "":
			v.add(prefix+".source.type", "required (one of: local, git, url)")
		case "local":
			if p.Source.Path == "" {
				v.add(prefix+".source.path", "required for local source")
			}
		case "git":
			if p.Source.Repo == "" {
				v.add(prefix+".source.repo", "required for git source")
			}
		case "url":
			if p.Source.URL == "" {
				v.add(prefix+".source.url", "required for url source")
			}
		default:
			v.add(prefix+".source.type", fmt.Sprintf("unknown type %q", p.Source.Type))
		}
	}
	if len(v.Fields) > 0 {
		return v
	}
	return nil
}

// Lookup returns the plugin entry by name.
func (m *Marketplace) Lookup(name string) (*MarketplacePlugin, bool) {
	if m == nil {
		return nil, false
	}
	for i := range m.Plugins {
		if m.Plugins[i].Name == name {
			return &m.Plugins[i], true
		}
	}
	return nil, false
}

// SplitPluginRef parses `plugin@marketplace` into its two halves.
// Returns ok=false when the input lacks `@` or either half is
// malformed; the caller falls back to direct-path install in that
// case.
func SplitPluginRef(ref string) (plugin, marketplace string, ok bool) {
	matches := pluginAtMarketplaceRE.FindStringSubmatch(ref)
	if matches == nil {
		return "", "", false
	}
	return matches[1], matches[2], true
}

// ─── fetch ─────────────────────────────────────────────────────

// FetchMarketplace retrieves a marketplace.json from one of three
// sources, dispatched by the URL scheme:
//
//	file://    — read a local marketplace.json
//	git+https://, git+ssh:// — git clone the repo and read
//	            <repo>/.claude-plugin/marketplace.json
//	            (or top-level marketplace.json as fallback)
//	https://, http://      — HTTP GET the URL directly
//	(absolute path / relative path)  — same as file://
//
// On success returns the parsed Marketplace and the directory it
// was fetched into (for git/url sources, a cache dir under
// ~/.biumind/plugins/marketplaces/<name>/). The caller can then
// resolve per-plugin sources of type "local" relative to that dir.
//
// pinnedKey, when non-empty, requires the marketplace.json to be
// accompanied by a .sig file that verifies against it. nil =>
// unsigned fetch (the user takes the trust risk).
func FetchMarketplace(src string, pinnedKey ed25519.PublicKey) (*Marketplace, string, error) {
	switch {
	case strings.HasPrefix(src, "git+"):
		return fetchGitMarketplace(strings.TrimPrefix(src, "git+"), pinnedKey)
	case strings.HasPrefix(src, "https://") && strings.HasSuffix(src, ".git"):
		// Convenience: GitHub URLs ending in .git are obviously git
		// repos. Treat as git+https without forcing the prefix.
		return fetchGitMarketplace(src, pinnedKey)
	case strings.HasPrefix(src, "https://"), strings.HasPrefix(src, "http://"):
		return fetchHTTPMarketplace(src, pinnedKey)
	case strings.HasPrefix(src, "file://"):
		return fetchLocalMarketplace(strings.TrimPrefix(src, "file://"), pinnedKey)
	default:
		// Treat as a local path (absolute or relative).
		return fetchLocalMarketplace(src, pinnedKey)
	}
}

// fetchLocalMarketplace reads a marketplace.json from disk. Two
// shapes accepted:
//
//	<dir>/.claude-plugin/marketplace.json
//	<dir>/marketplace.json
//	<file>.json   — a marketplace.json passed directly
//
// Signature: when pinnedKey is non-nil, looks for <path>.sig
// alongside the chosen file and verifies before parsing.
func fetchLocalMarketplace(path string, pinnedKey ed25519.PublicKey) (*Marketplace, string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, "", err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return nil, "", err
	}
	var manifestPath string
	// baseDir is the marketplace ROOT — the directory plugin-source
	// `path` fields resolve against. When the manifest lives under
	// .claude-plugin/, the root is the parent (above .claude-plugin)
	// so a sibling plugin directory `demo/` is reachable as
	// "demo" in the manifest, matching the conventional layout.
	var baseDir string
	if st.IsDir() {
		preferred := filepath.Join(abs, ".claude-plugin", "marketplace.json")
		fallback := filepath.Join(abs, "marketplace.json")
		if _, err := os.Stat(preferred); err == nil {
			manifestPath = preferred
			baseDir = abs
		} else if _, err := os.Stat(fallback); err == nil {
			manifestPath = fallback
			baseDir = abs
		} else {
			return nil, "", fmt.Errorf("no marketplace.json under %s", abs)
		}
	} else {
		manifestPath = abs
		baseDir = filepath.Dir(abs)
		// If the file lives under a `.claude-plugin` subdir, climb
		// one level so relative paths resolve from the marketplace
		// root (not from inside .claude-plugin/).
		if filepath.Base(baseDir) == ".claude-plugin" {
			baseDir = filepath.Dir(baseDir)
		}
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, "", err
	}
	if pinnedKey != nil {
		if err := verifyManifestSig(data, manifestPath+".sig", pinnedKey); err != nil {
			return nil, "", err
		}
	}
	mp, err := ParseMarketplaceBytes(data)
	if err != nil {
		return nil, "", err
	}
	return mp, baseDir, nil
}

// fetchHTTPMarketplace GETs the URL and validates the body. Sig
// fetched from <url>.sig only when pinnedKey is non-nil.
func fetchHTTPMarketplace(rawURL string, pinnedKey ed25519.PublicKey) (*Marketplace, string, error) {
	if _, err := url.Parse(rawURL); err != nil {
		return nil, "", fmt.Errorf("invalid URL: %w", err)
	}
	data, err := httpGet(rawURL)
	if err != nil {
		return nil, "", err
	}
	if pinnedKey != nil {
		sigData, err := httpGet(rawURL + ".sig")
		if err != nil {
			return nil, "", fmt.Errorf("fetch signature: %w", err)
		}
		if err := skillpack.Verify(data, strings.TrimSpace(string(sigData)), pinnedKey); err != nil {
			return nil, "", fmt.Errorf("%w: %v", ErrMarketplaceSignature, err)
		}
	}
	mp, err := ParseMarketplaceBytes(data)
	if err != nil {
		return nil, "", err
	}
	return mp, "", nil
}

// fetchGitMarketplace shallow-clones the repo into the plugin
// marketplace cache and reads the manifest. If a previous clone
// exists, runs `git pull` instead of re-cloning so updates are
// fast. The fetched directory is returned alongside the parsed
// marketplace so per-plugin local sources can resolve against it.
func fetchGitMarketplace(repo string, pinnedKey ed25519.PublicKey) (*Marketplace, string, error) {
	cacheDir, err := MarketplaceCacheDir()
	if err != nil {
		return nil, "", err
	}
	// Derive a stable cache subdir from the repo URL — replace any
	// non-alphanumeric run with a single hyphen so a path like
	// "github.com-org-repo" is a valid directory name.
	dir := filepath.Join(cacheDir, sanitiseForFS(repo))
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return nil, "", err
	}
	if _, err := os.Stat(dir); err == nil {
		// Existing clone — pull. Best-effort; offline use should still
		// work against the cached state.
		_ = runGit(dir, "pull", "--ff-only")
	} else {
		if err := runGit("", "clone", "--depth", "1", repo, dir); err != nil {
			return nil, "", fmt.Errorf("git clone %s: %w", repo, err)
		}
	}
	return fetchLocalMarketplace(dir, pinnedKey)
}

// httpGet is a small wrapper around http.Get with a sane timeout
// and a body-size cap to keep a malicious / pathological response
// from blowing up memory.
func httpGet(rawURL string) ([]byte, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(rawURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d for %s", resp.StatusCode, rawURL)
	}
	const maxSize = 4 * 1024 * 1024 // 4 MiB; marketplace.json is ~few KB in practice
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSize))
	if err != nil {
		return nil, err
	}
	return body, nil
}

// runGit invokes git with the given args. cwd may be empty for
// commands that don't operate on a working tree (e.g. clone).
// Stderr is propagated into the error message — git's own errors
// are usually clearer than any wrapper we'd write.
func runGit(cwd string, args ...string) error {
	cmd := exec.Command("git", args...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s: %w (%s)", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// MarketplaceCacheDir returns ~/.biumind/plugins/marketplaces/,
// creating the parent if missing. Used by both git fetches and
// the known-marketplaces store (separate file in the same dir).
func MarketplaceCacheDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".biumind", "plugins", "marketplaces"), nil
}

// sanitiseForFS turns an arbitrary string into a path-safe slug.
// Replaces every run of non-alnum chars with a single hyphen and
// trims leading/trailing hyphens.
func sanitiseForFS(s string) string {
	var b strings.Builder
	prevHyphen := true
	for _, r := range s {
		switch {
		case (r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') ||
			r == '_':
			b.WriteRune(r)
			prevHyphen = false
		default:
			if !prevHyphen {
				b.WriteByte('-')
				prevHyphen = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// verifyManifestSig reads <sigPath> and verifies it against the
// raw manifest bytes using the pinned public key. The .sig file
// has the same shape skillpack uses: one line of base64 ed25519.
func verifyManifestSig(manifest []byte, sigPath string, pub ed25519.PublicKey) error {
	sigBytes, err := os.ReadFile(sigPath)
	if err != nil {
		return fmt.Errorf("read signature %s: %w", sigPath, err)
	}
	if err := skillpack.Verify(manifest, strings.TrimSpace(string(sigBytes)), pub); err != nil {
		return fmt.Errorf("%w: %v", ErrMarketplaceSignature, err)
	}
	return nil
}

// ─── install resolution ────────────────────────────────────────

// ResolveInstall takes a marketplace plugin entry and produces a
// local directory ready to hand off to plugins.Load. For local
// sources, the path is resolved relative to baseDir (the directory
// the marketplace.json lived in); for git sources, a fresh clone
// lands in the plugin install cache; url sources are stub-rejected
// in this PR so the failure surfaces clearly.
//
// Returns the resolved plugin source directory; caller is
// responsible for copying it to ~/.biumind/plugins/<name>/.
func ResolveInstall(entry *MarketplacePlugin, baseDir string) (string, error) {
	if entry == nil {
		return "", fmt.Errorf("nil marketplace entry")
	}
	switch entry.Source.Type {
	case "local":
		path := entry.Source.Path
		if !filepath.IsAbs(path) {
			path = filepath.Join(baseDir, path)
		}
		st, err := os.Stat(path)
		if err != nil {
			return "", fmt.Errorf("local source %s: %w", path, err)
		}
		if !st.IsDir() {
			return "", fmt.Errorf("local source %s is not a directory", path)
		}
		return path, nil

	case "git":
		cacheDir, err := MarketplaceCacheDir()
		if err != nil {
			return "", err
		}
		dst := filepath.Join(cacheDir, "plugins", sanitiseForFS(entry.Source.Repo))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return "", err
		}
		if _, err := os.Stat(dst); err == nil {
			_ = runGit(dst, "pull", "--ff-only")
		} else {
			args := []string{"clone", "--depth", "1"}
			if entry.Source.Ref != "" {
				args = append(args, "--branch", entry.Source.Ref)
			}
			args = append(args, entry.Source.Repo, dst)
			if err := runGit("", args...); err != nil {
				return "", err
			}
		}
		return dst, nil

	case "url":
		// Not supported in PP7. Return a clear error so the user
		// knows to wait for a future release rather than guess at a
		// missing flag.
		return "", fmt.Errorf("%w: url source ('%s') — git or local source recommended for now",
			ErrMarketplaceSourceUnsupported, entry.Source.URL)

	default:
		return "", fmt.Errorf("%w: %s", ErrMarketplaceSourceUnsupported, entry.Source.Type)
	}
}
