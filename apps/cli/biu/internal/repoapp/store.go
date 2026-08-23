// Package repoapp is the local runner behind `biu repo-app` — it clones
// a GitHub project onto this machine, installs its dependencies, and
// runs it as a local web service bound to 127.0.0.1.
//
// Instance layout (one directory per installed repo):
//
//	~/.biumind/repo-apps/<slug>/
//	├─ repo/         git clone --depth 1
//	├─ data/         user data (survives updates)
//	├─ .env          config + secrets (plaintext, mode 0600)
//	├─ runtime.json  port / start_cmd / installed ref+sha / health_path
//	├─ logs/run.log  child stdout+stderr (truncated >10MB on start)
//	└─ runner.pid    PID file (procmgmt semantics)
//
// The design authority is docs/BiuMind-AppCenter-GitHub-Repo-Apps-
// TechPlan.md §3. macOS/Linux only in M1; Windows commands fail fast
// with a clear message (see Supported()).
package repoapp

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// envRootOverride lets tests and power users relocate the instance root
// without touching the config file.
const envRootOverride = "BIU_REPOAPP_ROOT"

// Supported reports whether repo-app can run on this platform. M1 ships
// macOS/Linux only — the process primitives (Setsid detach, signal-based
// liveness) are Unix assumptions, matching the precedent documented in
// cmd/biu/serve_cmd.go.
func Supported() bool {
	return supportedPlatform
}

// DefaultRoot returns the instance registry root: $BIU_REPOAPP_ROOT when
// set, else ~/.biumind/repo-apps/.
func DefaultRoot() (string, error) {
	if root := os.Getenv(envRootOverride); root != "" {
		return root, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".biumind", "repo-apps"), nil
}

// RuntimesDir returns ~/.biumind/runtimes/ where the bootstrapper
// installs user-local toolchains (uv/mise binaries, uv pythons, mise
// nodes). Nothing here touches the system PATH.
func RuntimesDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".biumind", "runtimes"), nil
}

// Store is the on-disk registry of installed repo apps.
type Store struct {
	Root string
}

// NewStore returns a Store rooted at root; empty root resolves to
// DefaultRoot().
func NewStore(root string) (*Store, error) {
	if root == "" {
		var err error
		root, err = DefaultRoot()
		if err != nil {
			return nil, err
		}
	}
	return &Store{Root: root}, nil
}

// ownerRepoRe matches the shorthand form "owner/repo" (optionally with a
// .git suffix).
var ownerRepoRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+(\.git)?$`)

// ParseRepoArg normalises an install argument into a clone URL and a
// filesystem-safe slug. Accepts:
//
//	owner/repo
//	https://github.com/owner/repo(.git)(/tree/<ref>)...
//	git@github.com:owner/repo(.git)
//
// The slug is "<owner>-<repo>" sanitised via sanitiseForFS so it is a
// valid directory name on disk.
func ParseRepoArg(arg string) (slug, cloneURL string, err error) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return "", "", fmt.Errorf("empty repo argument")
	}
	if ownerRepoRe.MatchString(arg) {
		arg = "https://github.com/" + strings.TrimSuffix(arg, ".git")
	}
	if strings.HasPrefix(arg, "git@github.com:") {
		arg = "https://github.com/" + strings.TrimPrefix(arg, "git@github.com:")
	}
	u, err := url.Parse(arg)
	if err != nil || u.Host == "" {
		return "", "", fmt.Errorf("unrecognised repo %q — expected a GitHub URL or owner/repo", arg)
	}
	if u.Host != "github.com" && u.Host != "www.github.com" {
		return "", "", fmt.Errorf("only github.com repos are supported (got host %q)", u.Host)
	}
	parts := strings.Split(strings.Trim(strings.TrimSuffix(u.Path, ".git"), "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("cannot derive owner/repo from %q", arg)
	}
	cloneURL = "https://github.com/" + parts[0] + "/" + parts[1] + ".git"
	slug = sanitiseForFS(parts[0] + "/" + parts[1])
	if slug == "" {
		return "", "", fmt.Errorf("cannot derive a filesystem-safe slug from %q", arg)
	}
	return slug, cloneURL, nil
}

// sanitiseForFS turns an arbitrary string into a path-safe slug — same
// rule as plugins.sanitiseForFS (marketplace.go): every run of
// non-alphanumeric chars collapses to a single hyphen, hyphens trimmed
// at both ends.
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

// Instance is one installed repo app on disk.
type Instance struct {
	Slug string
	Dir  string
}

func (i Instance) RepoDir() string     { return filepath.Join(i.Dir, "repo") }
func (i Instance) DataDir() string     { return filepath.Join(i.Dir, "data") }
func (i Instance) EnvPath() string     { return filepath.Join(i.Dir, ".env") }
func (i Instance) RuntimePath() string { return filepath.Join(i.Dir, "runtime.json") }
func (i Instance) LogPath() string     { return filepath.Join(i.Dir, "logs", "run.log") }
func (i Instance) PIDPath() string     { return filepath.Join(i.Dir, "runner.pid") }

// Instance returns the handle for slug; it may not exist on disk yet.
func (s *Store) Instance(slug string) Instance {
	return Instance{Slug: slug, Dir: filepath.Join(s.Root, slug)}
}

// Create materialises the instance directory layout (repo/ is created by
// the clone step, not here).
func (s *Store) Create(slug string) (Instance, error) {
	inst := s.Instance(slug)
	for _, dir := range []string{inst.Dir, inst.DataDir(), filepath.Dir(inst.LogPath())} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return inst, err
		}
	}
	// Create an empty .env up front so the mode-0600 invariant holds even
	// before the user writes any config into it.
	if _, err := os.Stat(inst.EnvPath()); os.IsNotExist(err) {
		if err := os.WriteFile(inst.EnvPath(), nil, 0o600); err != nil {
			return inst, err
		}
	}
	return inst, nil
}

// Exists reports whether the instance directory is present.
func (s *Store) Exists(slug string) bool {
	_, err := os.Stat(s.Instance(slug).RuntimePath())
	return err == nil
}

// List returns every installed instance (directories under the root that
// contain a runtime.json), sorted by slug.
func (s *Store) List() ([]Instance, error) {
	entries, err := os.ReadDir(s.Root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Instance
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		inst := s.Instance(e.Name())
		if _, err := os.Stat(inst.RuntimePath()); err == nil {
			out = append(out, inst)
		}
	}
	return out, nil
}

// Remove deletes the whole instance directory. Callers must stop the
// runner first (Runner.Stop) — Remove itself does no process management.
func (s *Store) Remove(slug string) error {
	return os.RemoveAll(s.Instance(slug).Dir)
}

// RuntimeInfo is the runtime.json contract: everything the runner needs
// to (re)start the app without re-detecting anything.
type RuntimeInfo struct {
	RepoURL      string    `json:"repo_url"`
	Ref          string    `json:"ref"`
	InstalledSHA string    `json:"installed_sha"`
	Stack        string    `json:"stack"` // node | python | docker
	PackageManager string  `json:"package_manager,omitempty"`
	StartCmd     string    `json:"start_cmd"` // executed via `sh -c`
	Port         int       `json:"port"`
	HealthPath   string    `json:"health_path"` // empty = "/"
	// PathExtra entries are prepended to the child's PATH (managed
	// toolchains, the project venv, ...). Absolute paths only.
	PathExtra []string  `json:"path_extra,omitempty"`
	UpdatedAt time.Time   `json:"updated_at"`
}

// EffectiveHealthPath returns the configured health path, defaulting to
// "/".
func (r *RuntimeInfo) EffectiveHealthPath() string {
	if r.HealthPath == "" {
		return "/"
	}
	return r.HealthPath
}

// LoadRuntime reads runtime.json for an instance directory.
func LoadRuntime(dir string) (*RuntimeInfo, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "runtime.json"))
	if err != nil {
		return nil, err
	}
	var ri RuntimeInfo
	if err := json.Unmarshal(raw, &ri); err != nil {
		return nil, fmt.Errorf("parse runtime.json: %w", err)
	}
	return &ri, nil
}

// SaveRuntime writes runtime.json atomically (write-then-rename) so a
// crash mid-update can't leave a half-written file the runner chokes on.
func SaveRuntime(dir string, ri *RuntimeInfo) error {
	raw, err := json.MarshalIndent(ri, "", "  ")
	if err != nil {
		return err
	}
	final := filepath.Join(dir, "runtime.json")
	tmp := final + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, final)
}
