// Package skillmarket rewrites marketplace catalog URLs to direct
// SKILL.md fetch URLs so `biu skill install
// https://lobehub.com/agents/foo` works without making the user dig
// through a UI to find the raw file.
//
// The actual fetching still happens server-side via
// services/runtime/internal/skills/installer.FromURL — adapters in
// this package are pure URL transforms. Keeping the rewrite client-
// side means:
//
//   - The server has a single uniform "fetch one HTTPS URL" code path.
//   - Catalog URL conventions can change without redeploying runtime;
//     bumping the CLI is enough.
//   - Tests stay deterministic — no network in this package.
//
// Each catalog has its own adapter with a hostname match + a Resolve
// step that returns the raw URL. Unknown hosts fall through unchanged
// so private mirrors / GitHub raw / arbitrary URLs keep working.
package skillmarket

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// ErrAmbiguous — the URL matched a catalog adapter but the path
// shape doesn't carry enough info to resolve. Caller should surface
// this as "open the catalog in a browser, copy the SKILL.md URL".
var ErrAmbiguous = errors.New("marketplace URL doesn't carry enough info to resolve")

// Adapter is implemented by each catalog. Lifecycle:
//
//	Match(u)   — cheap host check; returns false to fall through
//	Resolve(u) — string-level rewrite; may return ErrAmbiguous
type Adapter interface {
	// Name returns a stable identifier used in CLI output / logs.
	Name() string
	// Match returns true when this adapter recognises the URL's host
	// (and maybe path-prefix). Must be cheap — called on every install.
	Match(u *url.URL) bool
	// Resolve rewrites the URL to a direct SKILL.md fetch URL. Only
	// called when Match returned true. The returned URL is guaranteed
	// to be valid — the caller passes it to the server's URL fetcher
	// without extra validation.
	Resolve(u *url.URL) (string, error)
}

// Default returns the built-in adapter list. Order matters — the
// first Match wins. Custom builds (forks, internal mirrors) can
// extend this by injecting their own Adapter ahead of these.
func Default() []Adapter {
	return []Adapter{
		LobeHub{},
		SkillsSh{},
		ClaudePlugins{},
	}
}

// Resolve walks the adapter list, rewrites the URL if matched, and
// otherwise returns the original string verbatim. Returns the
// adapter that matched (or "" for passthrough) so the CLI can
// surface "rewriting via lobehub adapter".
func Resolve(rawURL string) (resolved, adapterName string, err error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", "", fmt.Errorf("parse url: %w", err)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		// Non-HTTP — almost certainly a local file path. Pass through
		// untouched; the caller's path-shape detection will route it.
		return rawURL, "", nil
	}
	for _, a := range Default() {
		if !a.Match(u) {
			continue
		}
		out, err := a.Resolve(u)
		if err != nil {
			return "", a.Name(), err
		}
		return out, a.Name(), nil
	}
	return rawURL, "", nil
}

// ─── Adapters ──────────────────────────────────────────────

// LobeHub — https://lobehub.com/agents/<slug>
//
// LobeHub's agent catalog is published from
// https://github.com/lobehub/lobe-chat-agents under src/<slug>/. The
// agent definition itself is index.json (LobeHub schema), NOT a
// BiuMind SKILL.md, so a direct rewrite isn't lossless. We treat the
// adapter as "best-effort": rewrite to the GitHub-raw index.json so
// the user gets a friendly error from the runtime parser ("missing
// `description:` frontmatter") and can manually port the agent.
//
// When a BiuMind-shipped SKILL.md mirror exists for an upstream
// LobeHub agent, replace this with a direct fetch.
type LobeHub struct{}

func (LobeHub) Name() string { return "lobehub" }
func (LobeHub) Match(u *url.URL) bool {
	return strings.EqualFold(u.Host, "lobehub.com") &&
		strings.HasPrefix(u.Path, "/agents/")
}
func (LobeHub) Resolve(u *url.URL) (string, error) {
	slug := strings.Trim(strings.TrimPrefix(u.Path, "/agents/"), "/")
	if slug == "" {
		return "", ErrAmbiguous
	}
	// LobeHub agents repo layout is src/<slug>/index.json. We point at
	// it so users see what the upstream looks like even though parse
	// will reject — the goal is to surface "this isn't a SKILL.md"
	// loudly rather than silently doing nothing.
	return "https://raw.githubusercontent.com/lobehub/lobe-chat-agents/main/src/" +
		slug + "/index.json", nil
}

// SkillsSh — https://skills.sh/<slug>
//
// skills.sh is the BiuMind-aligned community catalog: every entry IS
// a SKILL.md. The catalog page at /<slug> serves an HTML wrapper; the
// raw SKILL.md is at /<slug>/SKILL.md.
type SkillsSh struct{}

func (SkillsSh) Name() string { return "skills.sh" }
func (SkillsSh) Match(u *url.URL) bool {
	return strings.EqualFold(u.Host, "skills.sh")
}
func (SkillsSh) Resolve(u *url.URL) (string, error) {
	// Already at /<slug>/SKILL.md — passthrough.
	if strings.HasSuffix(u.Path, "/SKILL.md") {
		return u.String(), nil
	}
	slug := strings.Trim(u.Path, "/")
	if slug == "" {
		return "", ErrAmbiguous
	}
	return "https://skills.sh/" + slug + "/SKILL.md", nil
}

// ClaudePlugins — https://claude-plugins.dev/<slug>
//
// Anthropic's plugin catalog. Layout assumption mirrors skills.sh —
// /<slug>/SKILL.md raw under the same host. If upstream moves to a
// CDN later, the adapter is the only place that needs updating.
type ClaudePlugins struct{}

func (ClaudePlugins) Name() string { return "claude-plugins.dev" }
func (ClaudePlugins) Match(u *url.URL) bool {
	return strings.EqualFold(u.Host, "claude-plugins.dev")
}
func (ClaudePlugins) Resolve(u *url.URL) (string, error) {
	if strings.HasSuffix(u.Path, "/SKILL.md") {
		return u.String(), nil
	}
	slug := strings.Trim(u.Path, "/")
	if slug == "" {
		return "", ErrAmbiguous
	}
	return "https://claude-plugins.dev/" + slug + "/SKILL.md", nil
}
