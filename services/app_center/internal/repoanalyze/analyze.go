// Repo analysis orchestration (tech plan §2.1): GitHub URL → repo meta
// → latest ref → stack detection → env schema → manifest draft.
//
// The draft is a biuapp.Manifest the install handler re-validates with
// biuapp.Validate before persisting (same five-step pattern as
// user_webview). Kind is pinned to "webview" — "container" is rejected
// by the validator (validator.go:256-264); the view URL is a loopback
// placeholder because the real port is only known once the local runner
// starts the process (design doc 4.4).

package repoanalyze

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"

	"github.com/biumind/biumind/packages/go-sdk/biu/biuapp"
)

// Result is the full analysis payload returned to the confirm page.
type Result struct {
	ManifestDraft biuapp.Manifest `json:"manifest_draft"`
	Stack         *Stack          `json:"stack"`
	EnvSchema     []EnvField      `json:"env_schema,omitempty"`
	RepoMeta      RepoMeta        `json:"repo_meta"`
	Warnings      []string        `json:"warnings,omitempty"`
}

// RepoMeta is the subset of repo state the install flow persists into
// app_center.apps.repo_meta (poll state is added later by the poller).
type RepoMeta struct {
	URL           string `json:"url"`
	DefaultBranch string `json:"default_branch"`
	LatestRef     string `json:"latest_ref"`
	LatestSHA     string `json:"latest_sha"`
	Stars         int    `json:"stars"`
	License       string `json:"license"`
}

// Analyze runs the full pipeline against a GitHub repo URL.
func Analyze(ctx context.Context, gh *Client, repoURL string) (*Result, error) {
	owner, repo, err := ParseRepoURL(repoURL)
	if err != nil {
		return nil, err
	}

	info, _, err := gh.Repo(ctx, owner, repo, "")
	if err != nil {
		return nil, fmt.Errorf("repoanalyze: repo meta: %w", err)
	}

	ref, sha, isTag, err := resolveLatestRef(ctx, gh, owner, repo, info.DefaultBranch)
	if err != nil {
		return nil, err
	}

	stack, err := Detect(ctx, gh, owner, repo, ref)
	if err != nil {
		return nil, fmt.Errorf("repoanalyze: detect: %w", err)
	}

	envSchema, err := fetchEnvSchema(ctx, gh, owner, repo, ref)
	if err != nil {
		return nil, fmt.Errorf("repoanalyze: env schema: %w", err)
	}

	version := "0.1.0"
	if isTag {
		version = normalizeVersion(ref)
	}

	res := &Result{
		ManifestDraft: synthesiseDraft(owner, repo, info, version),
		Stack:         stack,
		EnvSchema:     envSchema,
		RepoMeta: RepoMeta{
			URL:           "https://github.com/" + owner + "/" + repo,
			DefaultBranch: info.DefaultBranch,
			LatestRef:     ref,
			LatestSHA:     sha,
			Stars:         info.Stars,
			License:       info.LicenseSPDX,
		},
		Warnings: licenseWarnings(info.LicenseSPDX),
	}
	return res, nil
}

// ─── URL parsing ─────────────────────────────────────────────────────

var (
	// Pattern lifted from biuapp/rss/discover.go:49, extended with an
	// optional .git suffix (paste-from-browser URLs often carry it).
	githubURLRe = regexp.MustCompile(`^https?://(?:www\.)?github\.com/([A-Za-z0-9._-]+)/([A-Za-z0-9._-]+?)(?:\.git)?/?$`)
	shorthandRe = regexp.MustCompile(`^([A-Za-z0-9._-]+)/([A-Za-z0-9._-]+?)(?:\.git)?$`)
)

// ParseRepoURL accepts https://github.com/owner/repo (with optional
// .git suffix / trailing slash) and the owner/repo shorthand.
func ParseRepoURL(raw string) (owner, repo string, err error) {
	raw = strings.TrimSpace(raw)
	if m := githubURLRe.FindStringSubmatch(raw); m != nil {
		return m[1], m[2], nil
	}
	if m := shorthandRe.FindStringSubmatch(raw); m != nil {
		return m[1], m[2], nil
	}
	return "", "", fmt.Errorf("%w: %q", ErrInvalidRepoURL, raw)
}

// ─── latest ref resolution ───────────────────────────────────────────

// resolveLatestRef picks the ref to analyse/install: latest release tag
// → newest git tag → default branch HEAD. isTag reports whether the ref
// is a tag (i.e. eligible for version normalisation).
func resolveLatestRef(ctx context.Context, gh *Client, owner, repo, defaultBranch string) (ref, sha string, isTag bool, err error) {
	tag, _, err := gh.LatestRelease(ctx, owner, repo, "")
	if err != nil {
		return "", "", false, fmt.Errorf("repoanalyze: latest release: %w", err)
	}

	tags, _, err := gh.Tags(ctx, owner, repo, "")
	if err != nil {
		return "", "", false, fmt.Errorf("repoanalyze: tags: %w", err)
	}

	if tag != "" {
		// Map the release tag to its commit sha via the tags list; a
		// missing entry just leaves sha empty (poller reconciles later).
		for _, t := range tags {
			if t.Name == tag {
				return t.Name, t.SHA, true, nil
			}
		}
		return tag, "", true, nil
	}
	if len(tags) > 0 {
		return tags[0].Name, tags[0].SHA, true, nil
	}

	sha, err = gh.HeadSHA(ctx, owner, repo, defaultBranch)
	if err != nil {
		return "", "", false, fmt.Errorf("repoanalyze: head sha: %w", err)
	}
	return defaultBranch, sha, false, nil
}

// ─── manifest draft ──────────────────────────────────────────────────

// viewURLPlaceholder satisfies the validator's non-empty webview URL
// check; the client resolves the real loopback URL from the runtime API
// at open time (tech plan §1 decision 2).
const viewURLPlaceholder = "http://127.0.0.1:0/"

func synthesiseDraft(owner, repo string, info *RepoInfo, version string) biuapp.Manifest {
	identifier := deriveIdentifier(owner, repo)
	description := strings.TrimSpace(info.Description)
	if description == "" {
		description = fmt.Sprintf("GitHub 项目 %s/%s", owner, repo)
	}
	if len(description) > 200 { // validator.go:242 hard limit
		description = description[:200]
	}
	return biuapp.Manifest{
		Name:        identifier,
		Version:     version,
		Description: description,
		Author:      owner,
		// Bare "net.outbound": repo apps run arbitrary third-party code,
		// so per-host scoping is meaningless. The head alone is in the
		// validator's permission whitelist (validator.go:60).
		Permissions: []string{"net.outbound"},
		Actions:     []biuapp.ActionSpec{}, // repo apps expose no actions (M1)
		ManifestExt: biuapp.ManifestExt{
			Identifier: identifier,
			Title:      repo,
			Kind:       "webview",
			Category:   guessCategory(info.Topics),
			Views: []biuapp.ViewSpec{
				{
					ID:     "home",
					Route:  "/apps/" + identifier,
					Title:  repo,
					Layout: biuapp.LayoutWebView,
					URL:    viewURLPlaceholder,
				},
			},
		},
	}
}

var slugUnsafe = regexp.MustCompile(`[^a-z0-9-]+`)

// deriveIdentifier builds gh-<owner>-<repo>-<hash8>, the pattern from
// installs/user_webview.go:179-191. hash8 = first 4 bytes of
// sha256(owner+"/"+repo) hex — same repo re-analysed yields the same
// identifier (idempotent re-install).
func deriveIdentifier(owner, repo string) string {
	h := sha256.Sum256([]byte(strings.ToLower(owner) + "/" + strings.ToLower(repo)))
	suffix := hex.EncodeToString(h[:4])
	o, r := slugify(owner, 20), slugify(repo, 30)
	if o == "" {
		o = "x"
	}
	if r == "" {
		r = "x"
	}
	return "gh-" + o + "-" + r + "-" + suffix
}

// slugify cleans a name segment for slugRe (validator.go:107):
// lowercase kebab, no leading dash (the "gh-" prefix already guarantees
// a leading letter).
func slugify(s string, maxLen int) string {
	s = slugUnsafe.ReplaceAllString(strings.ToLower(s), "-")
	s = strings.Trim(s, "-")
	if len(s) > maxLen {
		s = strings.Trim(s[:maxLen], "-")
	}
	return s
}

// normalizeVersion turns a release tag into strict semver x.y.z
// (validator.go:113): strips a v/V prefix, pads missing segments with
// zeros, keeps a well-formed -prerelease tail, drops +build metadata.
// Unrecognisable tags fall back to 0.1.0.
func normalizeVersion(tag string) string {
	v := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(tag), "v"), "V")
	if i := strings.Index(v, "+"); i >= 0 {
		v = v[:i]
	}
	main, pre := v, ""
	if i := strings.Index(v, "-"); i >= 0 {
		main, pre = v[:i], v[i:]
	}
	parts := strings.Split(main, ".")
	nums := make([]string, 0, 3)
	for _, p := range parts {
		if !isDigits(p) {
			break
		}
		nums = append(nums, p)
		if len(nums) == 3 {
			break
		}
	}
	if len(nums) == 0 {
		return "0.1.0"
	}
	for len(nums) < 3 {
		nums = append(nums, "0")
	}
	out := strings.Join(nums, ".")
	if pre != "" && prereleaseRe.MatchString(pre) {
		out += pre
	}
	return out
}

var prereleaseRe = regexp.MustCompile(`^-[0-9A-Za-z.-]+$`)

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// ─── category guess ──────────────────────────────────────────────────

// topicCategory maps a GitHub topic to an app_center category. First
// match in categoryPriority wins; default is utility.
var topicCategory = map[string]string{
	"notes": "productivity", "note-taking": "productivity", "todo": "productivity",
	"kanban": "productivity", "calendar": "productivity", "task-manager": "productivity",
	"productivity": "productivity", "wiki": "productivity",
	"blog": "content", "cms": "content", "publishing": "content",
	"newsletter": "content", "static-site-generator": "content",
	"database": "data", "analytics": "data", "dashboard": "data",
	"data-visualization": "data", "monitoring": "data", "etl": "data",
	"chat": "comm", "messaging": "comm", "email": "comm",
	"forum": "comm", "matrix": "comm", "irc": "comm",
	"developer-tools": "dev", "devtools": "dev", "cli": "dev",
	"devops": "dev", "framework": "dev", "docker": "dev",
}

var categoryPriority = []string{"productivity", "content", "data", "comm", "dev"}

func guessCategory(topics []string) string {
	set := make(map[string]struct{}, len(topics))
	for _, t := range topics {
		set[strings.ToLower(t)] = struct{}{}
	}
	for _, cat := range categoryPriority {
		for t := range set {
			if topicCategory[t] == cat {
				return cat
			}
		}
	}
	return "utility"
}

// ─── env schema fetch ────────────────────────────────────────────────

func fetchEnvSchema(ctx context.Context, gh *Client, owner, repo, ref string) ([]EnvField, error) {
	for _, path := range envExampleCandidates {
		content, ok, err := gh.FileContent(ctx, owner, repo, path, ref)
		if err != nil {
			return nil, err
		}
		if ok {
			return ParseEnvSchema(string(content)), nil
		}
	}
	return nil, nil
}

// ─── license warnings ────────────────────────────────────────────────

func licenseWarnings(spdx string) []string {
	upper := strings.ToUpper(spdx)
	switch {
	case strings.Contains(upper, "AGPL") || strings.Contains(upper, "SSPL"):
		return []string{fmt.Sprintf("该仓库采用 %s 许可证，具有强 copyleft 约束，二次分发或商用前请确认合规", spdx)}
	case spdx == "" || upper == "NOASSERTION":
		return []string{"该仓库未声明开源许可证，代码默认保留所有权利，安装与二次分发存在法律风险"}
	}
	return nil
}
