// Package updatecheck implements biu's startup version check.
//
// Behaviour contract (mirrors the notify-only pattern used by most
// CLIs we surveyed):
//
//   * Runs only in the interactive REPL, in the background — never
//     blocks startup, never touches headless / serve / one-shot runs.
//   * Throttled to one network check per 24h via ~/.biu/update-check.json.
//   * A version is notified at most once (lastNotifiedVersion); a
//     skipped version is never notified again.
//   * Fail-closed everywhere: unparseable versions, network errors and
//     malformed responses all degrade to "no update" — the check can
//     never nag, crash, or suggest a downgrade.
//   * Dev builds (0.1.0-dev), source builds without ldflags, and
//     client-managed builds (1.2.3+clientmanaged, installed by the
//     desktop client) are skipped entirely — the client owns that
//     install plane.
//
// Data source: the GitHub releases API for biumind/biumind. The repo's
// release stream mixes tag prefixes (bare `v*` = CLI, `client-v*` =
// desktop client, `service-v*` = images), so /releases/latest can't be
// used — it would return whichever product shipped most recently. We
// list recent releases and pick the first bare-semver non-draft
// non-prerelease tag.

package updatecheck

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// githubReleasesURL lists the repo's releases, newest first.
// per_page=100 is the API max; enough to find a bare `v*` CLI tag even
// when several client/image releases shipped in between.
const githubReleasesURL = "https://api.github.com/repos/biumind/biumind/releases?per_page=100"

// CheckInterval is how often MaybeCheck actually hits the network.
const CheckInterval = 24 * time.Hour

// httpTimeout caps the whole check; the REPL shows nothing while it
// runs, but we still don't want a zombie goroutine hanging around.
const httpTimeout = 3 * time.Second

// Result describes one completed check.
type Result struct {
	// Current is the running binary's version string.
	Current string
	// Latest is the newest CLI release, bare semver (no "v").
	Latest string
	// Newer reports whether Latest > Current.
	Newer bool
	// Source identifies where the answer came from ("github").
	Source string
	// ReleaseURL links to the release page for humans.
	ReleaseURL string
	// AssetFilename is the GoReleaser archive for this platform
	// (biu_<ver>_<Os>_<arch>.tar.gz). Empty when the source carries
	// no per-asset metadata.
	AssetFilename string
	// AssetURL downloads AssetFilename. Empty when unavailable.
	AssetURL string
	// AssetSha256 is the expected hex digest when the source carries
	// it inline (OSS manifests do; GitHub relies on ChecksumsURL).
	AssetSha256 string
	// ChecksumsURL downloads GoReleaser's checksums.txt (SHA-256 per
	// asset). GitHub source relies on it for verification.
	ChecksumsURL string
	// DownloadSource says where AssetURL points ("github" / "oss").
	// Version info can come from GitHub while the download prefers
	// OSS (China-reachable) — see CheckWithFallback.
	DownloadSource string
}

// Options parameterise the check for tests. Zero values are filled
// with production defaults.
type Options struct {
	// CurrentVersion is the running binary's version (main.version).
	CurrentVersion string

	// Now defaults to time.Now.
	Now func() time.Time

	// HTTPDo defaults to a client with a 3s timeout. Injection point
	// for tests.
	HTTPDo func(*http.Request) (*http.Response, error)

	// StatePath defaults to ~/.biu/update-check.json.
	StatePath string

	// ManifestURL is the OSS manifest fallback
	// (<endpoint>/downloads/biu.json). Empty → GitHub only.
	// See CheckWithFallback.
	ManifestURL string
}

// MaybeCheck runs the throttled startup check: consults the toggle +
// state file, hits the network only when the 24h interval expired, and
// returns a Result only when there is a newer version the user hasn't
// been notified about (or explicitly skipped). A nil Result with nil
// error is the normal "nothing to say" outcome.
func MaybeCheck(ctx context.Context, opt Options) (*Result, error) {
	if !Enabled() {
		return nil, nil
	}
	if ShouldSkipVersion(opt.CurrentVersion) {
		return nil, nil
	}
	state, err := LoadState(opt.StatePath)
	if err != nil {
		// Unreadable state: fall through with defaults rather than
		// skipping the check entirely — a corrupt file shouldn't pin
		// the user on an old version forever.
		state = State{}
	}
	now := opt.Now
	if now == nil {
		now = time.Now
	}
	if !state.LastCheckedAt.IsZero() && now().Sub(state.LastCheckedAt) < CheckInterval {
		return nil, nil
	}

	res, err := CheckWithFallback(ctx, opt)
	if err != nil {
		// Network/parse failure: record the attempt so a broken
		// network doesn't retry on every startup, then stay quiet.
		state.LastCheckedAt = now()
		state.LatestVersion = ""
		_ = SaveState(opt.StatePath, state)
		return nil, nil
	}

	state.LastCheckedAt = now()
	state.LatestVersion = res.Latest
	switch {
	case !res.Newer:
		_ = SaveState(opt.StatePath, state)
		return nil, nil
	case res.Latest == state.SkippedVersion,
		res.Latest == state.LastNotifiedVersion:
		_ = SaveState(opt.StatePath, state)
		return nil, nil
	}
	state.LastNotifiedVersion = res.Latest
	_ = SaveState(opt.StatePath, state)
	return res, nil
}

// Check performs an unthrottled network check and reports the newest
// CLI release relative to CurrentVersion. Used by MaybeCheck and by
// explicit surfaces (`/upgrade check`) that want fresh data now.
func Check(ctx context.Context, opt Options) (*Result, error) {
	current := strings.TrimSpace(opt.CurrentVersion)
	if current == "" {
		current = "0"
	}

	do := opt.HTTPDo
	if do == nil {
		client := &http.Client{Timeout: httpTimeout}
		do = client.Do
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, githubReleasesURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "biu-update-check")

	resp, err := do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, fmt.Errorf("github api: %s", resp.Status)
	}

	var releases []struct {
		TagName    string `json:"tag_name"`
		HTMLURL    string `json:"html_url"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
		Assets     []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&releases); err != nil {
		return nil, fmt.Errorf("github api: decode: %w", err)
	}
	for _, rel := range releases {
		if rel.Draft || rel.Prerelease {
			continue
		}
		v, ok := ParseCLITag(rel.TagName)
		if !ok {
			continue
		}
		res := &Result{
			Current:    opt.CurrentVersion,
			Latest:     v,
			Newer:      CompareVersions(v, current) > 0,
			Source:     "github",
			ReleaseURL: rel.HTMLURL,
		}
		// Surface this platform's archive + checksums.txt so
		// self-update can download and verify without a second API
		// round-trip.
		want := PlatformFilename(v)
		for _, a := range rel.Assets {
			switch a.Name {
			case want:
				res.AssetFilename, res.AssetURL = a.Name, a.BrowserDownloadURL
			case "checksums.txt":
				res.ChecksumsURL = a.BrowserDownloadURL
			}
		}
		return res, nil
	}
	return nil, fmt.Errorf("github api: no bare `v*` CLI release in the latest %d releases", len(releases))
}

// PlatformFilename returns the GoReleaser archive name for the running
// platform, e.g. "biu_0.3.0_Darwin_arm64.tar.gz". Mirrors the
// name_template in apps/cli/biu/.goreleaser.yml (title-cased OS,
// amd64 → x86_64).
func PlatformFilename(version string) string {
	osName := strings.ToUpper(runtime.GOOS[:1]) + runtime.GOOS[1:]
	arch := runtime.GOARCH
	if arch == "amd64" {
		arch = "x86_64"
	}
	return fmt.Sprintf("biu_%s_%s_%s.tar.gz", version, osName, arch)
}

// CheckWithFallback resolves the newest release with a dual-source
// strategy:
//
//  1. GitHub API for version info (authoritative, but unreachable
//     from some networks).
//  2. OSS manifest (<endpoint>/downloads/biu.json) as download
//     preference when GitHub answered, and as full fallback when it
//     didn't.
//
// The OSS asset is preferred for downloads whenever it serves the
// same version — the archive is ~15 MB and OSS is reachable from
// networks where GitHub release downloads stall.
func CheckWithFallback(ctx context.Context, opt Options) (*Result, error) {
	res, gerr := Check(ctx, opt)
	if opt.ManifestURL == "" {
		return res, gerr
	}
	if res != nil {
		// GitHub answered: prefer the OSS asset for the download when
		// it serves the same version. Best-effort — an OSS hiccup must
		// not fail the check.
		if oss, err := checkOSS(ctx, opt); err == nil &&
			oss.Latest == res.Latest && oss.AssetURL != "" {
			res.AssetURL = oss.AssetURL
			res.AssetFilename = oss.AssetFilename
			res.AssetSha256 = oss.AssetSha256
			res.DownloadSource = "oss"
		}
		return res, nil
	}
	// GitHub failed (offline for the user's network) → OSS as the
	// source of truth.
	oss, oerr := checkOSS(ctx, opt)
	if oerr != nil {
		return nil, fmt.Errorf("github: %v; oss: %w", gerr, oerr)
	}
	return oss, nil
}

// ossManifest is the subset of schema/release/v1 consumed here.
type ossManifest struct {
	Version string `json:"version"`
	Assets  []struct {
		Platform string `json:"platform"`
		URL      string `json:"url"`
		Filename string `json:"filename"`
		Sha256   string `json:"sha256"`
	} `json:"assets"`
}

// ossPlatform is this runtime's platform key in the manifest enum
// (biu-macos-arm64, biu-linux-x64, …).
func ossPlatform() string {
	arch := runtime.GOARCH
	if arch == "amd64" {
		arch = "x64"
	}
	return "biu-" + runtime.GOOS + "-" + arch
}

// ManifestURLForEndpoint derives the OSS manifest URL from the user's
// server endpoint (single-origin addressing): <endpoint>/downloads/biu.json.
func ManifestURLForEndpoint(endpoint string) string {
	if endpoint = strings.TrimSpace(endpoint); endpoint == "" {
		return ""
	}
	return strings.TrimRight(endpoint, "/") + "/downloads/biu.json"
}

// checkOSS fetches the OSS manifest and resolves this platform's asset.
func checkOSS(ctx context.Context, opt Options) (*Result, error) {
	if opt.ManifestURL == "" {
		return nil, fmt.Errorf("oss: no manifest url configured")
	}
	do := opt.HTTPDo
	if do == nil {
		client := &http.Client{Timeout: httpTimeout}
		do = client.Do
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, opt.ManifestURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "biu-update-check")
	resp, err := do(req)
	if err != nil {
		return nil, fmt.Errorf("oss: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, fmt.Errorf("oss: %s", resp.Status)
	}
	var m ossManifest
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&m); err != nil {
		return nil, fmt.Errorf("oss: decode: %w", err)
	}
	if _, ok := parseSemver(m.Version); !ok {
		return nil, fmt.Errorf("oss: manifest version %q is not bare semver", m.Version)
	}
	current := strings.TrimSpace(opt.CurrentVersion)
	if current == "" {
		current = "0"
	}
	res := &Result{
		Current:        opt.CurrentVersion,
		Latest:         m.Version,
		Newer:          CompareVersions(m.Version, current) > 0,
		Source:         "oss",
		DownloadSource: "oss",
	}
	for _, a := range m.Assets {
		if a.Platform == ossPlatform() {
			res.AssetFilename = a.Filename
			res.AssetURL = a.URL
			res.AssetSha256 = a.Sha256
			break
		}
	}
	return res, nil
}

// cliTagPattern matches the CLI's release tags: bare semver with a `v`
// prefix, no prerelease/build suffix (those land as GitHub
// prereleases and are skipped above).
var cliTagPattern = regexp.MustCompile(`^v(\d+\.\d+\.\d+)$`)

// ParseCLITag extracts the bare version from a CLI release tag
// ("v0.3.0" → "0.3.0"). Non-CLI tags (client-v*, service-v*, …) and
// prerelease tags don't match.
func ParseCLITag(tag string) (string, bool) {
	m := cliTagPattern.FindStringSubmatch(strings.TrimSpace(tag))
	if m == nil {
		return "", false
	}
	return m[1], true
}

// ShouldSkipVersion reports whether a build should be excluded from
// update checks: dev/source builds without injected version info, and
// client-managed builds (build metadata suffix) whose install plane
// belongs to the desktop client.
func ShouldSkipVersion(v string) bool {
	v = strings.TrimSpace(v)
	if v == "" {
		return true
	}
	if strings.Contains(v, "+") {
		return true // build metadata: 1.2.3+clientmanaged
	}
	if strings.Contains(v, "-") {
		return true // prerelease/dev suffix: 0.1.0-dev, 0.2.0-rc1
	}
	if _, ok := parseSemver(v); !ok {
		return true // not X.Y.Z — source build or garbage
	}
	return false
}

// CompareVersions compares two bare semver strings numerically
// (major, minor, patch). Fail-closed: anything unparseable compares
// equal (0), so callers treating >0 as "update available" can never be
// talked into a downgrade or a bogus upgrade.
func CompareVersions(a, b string) int {
	pa, okA := parseSemver(a)
	pb, okB := parseSemver(b)
	if !okA || !okB {
		return 0
	}
	for i := range pa {
		switch {
		case pa[i] > pb[i]:
			return 1
		case pa[i] < pb[i]:
			return -1
		}
	}
	return 0
}

// parseSemver parses "X.Y.Z" into three ints. No prefix, no suffix —
// callers normalize beforehand.
func parseSemver(v string) ([3]int, bool) {
	var out [3]int
	parts := strings.Split(strings.TrimSpace(v), ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || (len(p) > 1 && p[0] == '0') {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// ─── toggle / state persistence ────────────────────────

// Enabled reports whether update checks are on: default true, unless
// BIU_UPDATE_CHECK=0/false or the state file says off.
func Enabled() bool {
	if v := os.Getenv("BIU_UPDATE_CHECK"); v == "0" || strings.EqualFold(v, "false") {
		return false
	}
	state, err := LoadState("")
	if err != nil {
		return true // unreadable state → default on
	}
	return state.enabled()
}

// State is the persisted check state at ~/.biu/update-check.json.
// It doubles as the on/off toggle so `biu config update-check off`
// and the throttle cache live in one small file.
type State struct {
	// Enabled defaults to true; nil (key absent) means true. Pointer
	// so a zero-value unmarshal can't silently flip it off.
	Enabled *bool `json:"enabled,omitempty"`

	// LastCheckedAt throttles the network call (one per CheckInterval).
	LastCheckedAt time.Time `json:"lastCheckedAt,omitempty"`

	// LatestVersion is the newest release seen at the last check.
	LatestVersion string `json:"latestVersion,omitempty"`

	// LastNotifiedVersion suppresses repeat notices for one version.
	LastNotifiedVersion string `json:"lastNotifiedVersion,omitempty"`

	// SkippedVersion is the version the user asked to stop hearing
	// about (`/upgrade check skip`).
	SkippedVersion string `json:"skippedVersion,omitempty"`
}

func (s State) enabled() bool {
	return s.Enabled == nil || *s.Enabled
}

// StatePath returns the state file location. Empty override →
// ~/.biu/update-check.json.
func statePath(override string) (string, error) {
	if override != "" {
		return override, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".biu", "update-check.json"), nil
}

// StatePath is the exported variant for cmd/biu use.
func StatePath() (string, error) { return statePath("") }

// LoadState reads the state file. Missing file → enabled defaults.
// A present-but-unparseable file is an error (corruption should be
// visible, not silently overwritten).
func LoadState(pathOverride string) (State, error) {
	path, err := statePath(pathOverride)
	if err != nil {
		return State{}, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return State{}, nil // Enabled==nil → on
		}
		return State{}, err
	}
	var s State
	if err := json.Unmarshal(raw, &s); err != nil {
		return State{}, fmt.Errorf("updatecheck: parse %s: %w", path, err)
	}
	return s, nil
}

// SaveState writes the state file atomically (tmp + rename, mode
// 0600 — same discipline as the telemetry control file).
func SaveState(pathOverride string, s State) error {
	path, err := statePath(pathOverride)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	body, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Enable turns checks back on.
func Enable() error {
	s, err := LoadState("")
	if err != nil {
		s = State{}
	}
	on := true
	s.Enabled = &on
	return SaveState("", s)
}

// Disable turns checks off (throttle cache preserved).
func Disable() error {
	s, err := LoadState("")
	if err != nil {
		s = State{}
	}
	off := false
	s.Enabled = &off
	return SaveState("", s)
}

// SkipVersion records "don't notify me about this version again".
func SkipVersion(v string) error {
	s, err := LoadState("")
	if err != nil {
		s = State{}
	}
	s.SkippedVersion = v
	return SaveState("", s)
}

// RecordCheck refreshes the cached latest-version + timestamp after an
// explicit (unthrottled) check, so `biu config update-check status`
// and `biu doctor` reflect reality.
func RecordCheck(latest string) error {
	s, err := LoadState("")
	if err != nil {
		s = State{}
	}
	s.LastCheckedAt = time.Now()
	s.LatestVersion = latest
	return SaveState("", s)
}
