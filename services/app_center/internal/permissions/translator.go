// Package permissions translates App manifest.permissions[] strings
// into the Cedar entity attributes the central app.cedar policy uses
// to gate per-action calls.
//
// Why a translator (not Cedar policy generation):
//
//   * The static app.cedar policy is checked into deploy/.../policies
//     and reviewed by hand. Generating per-install Cedar would put
//     untrusted data into policy text — a vector for policy injection.
//   * Instead, the static policy contains a small set of permit/forbid
//     rules guarded by `principal has X && resource.permissions.contains(...)`
//     conditions. The translator's job is to:
//       (a) verify the permission strings parse cleanly into the
//           {scope, params} pairs Cedar expects,
//       (b) produce the Resource / Principal attribute maps the
//           Authz client passes alongside each Decide call.
//
// We do NOT phone Authz at translate time — translation is a pure
// function of the manifest, and the runtime Decide call carries the
// translated attributes inline.

package permissions

import (
	"fmt"
	"sort"
	"strings"

	"github.com/biumind/biumind/packages/go-sdk/biu/biuapp"
)

// Permission is a parsed manifest permission string.
type Permission struct {
	// Scope is the prefix before the optional ":<param>". E.g.
	// "net.outbound", "hub.invoke", "oauth", "secrets.read".
	Scope string
	// Params is the comma-separated tail. May be empty (for unscoped
	// permissions like "hub.invoke") or a single value (oauth:gmail)
	// or a list (net.outbound:*.a.com,*.b.com).
	Params []string
}

// String renders the canonical wire form. Idempotent w.r.t. Parse.
func (p Permission) String() string {
	if len(p.Params) == 0 {
		return p.Scope
	}
	return p.Scope + ":" + strings.Join(p.Params, ",")
}

// Parse splits a single manifest permission string into its scope
// and parameters. Returns an error if the string is malformed; the
// validator (biuapp.Validate) catches most of these but we re-check
// here so the runtime path is self-defending.
func Parse(s string) (Permission, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Permission{}, fmt.Errorf("permissions: empty")
	}
	idx := strings.IndexByte(s, ':')
	if idx < 0 {
		return Permission{Scope: s}, nil
	}
	scope := s[:idx]
	tail := s[idx+1:]
	if scope == "" {
		return Permission{}, fmt.Errorf("permissions: missing scope before ':' in %q", s)
	}
	if tail == "" {
		// "scope:" with empty tail is invalid — either drop the colon
		// (no params) or write "scope:value".
		return Permission{}, fmt.Errorf("permissions: empty tail after ':' in %q", s)
	}
	parts := strings.Split(tail, ",")
	for i, p := range parts {
		parts[i] = strings.TrimSpace(p)
		if parts[i] == "" {
			return Permission{}, fmt.Errorf("permissions: empty param in %q", s)
		}
	}
	return Permission{Scope: scope, Params: parts}, nil
}

// ParseAll parses all manifest.permissions and returns the structured
// list. Aggregates errors instead of failing on first.
func ParseAll(manifest []string) ([]Permission, error) {
	out := make([]Permission, 0, len(manifest))
	var errs []string
	for i, s := range manifest {
		p, err := Parse(s)
		if err != nil {
			errs = append(errs, fmt.Sprintf("[%d] %v", i, err))
			continue
		}
		out = append(out, p)
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("permissions: %s", strings.Join(errs, "; "))
	}
	return out, nil
}

// ─── Cedar resource attribute maps ────────────────────────────────
//
// AppAttributes is what the runtime puts on the Cedar `resource` for
// app:invoke / app:read_data / app:write_data checks. It collapses
// the per-permission params into a small set of derived attribute
// values that the static app.cedar policy can probe via
// `resource.permissions.contains("net.outbound")` etc.

// AppAttributes builds the entity attribute map for an installation
// resource. The shape is stable; new fields are additive (Cedar
// `resource has X` guards in policy keep older policies forward-
// compatible).
func AppAttributes(install Installation, manifestPerms []Permission, granted []string) (map[string]any, error) {
	// Granted is the user-approved subset of manifestPerms — runtime
	// checks against this, not against manifest, so users who declined
	// optional permissions actually get the deny.
	grantedSet := map[string]struct{}{}
	for _, g := range granted {
		grantedSet[g] = struct{}{}
	}

	// Build the flat scope set (for `permissions.contains("scope")`)
	// and the per-scope params lookup (for net.outbound / oauth /
	// secrets.read which need the specific value).
	scopes := map[string]struct{}{}
	netOutbound := []string{}
	oauth := []string{}
	secretsRead := []string{}
	dataScopes := append([]string(nil), install.DataScopes...)

	for _, p := range manifestPerms {
		// If the user didn't grant this permission, omit it. Cedar
		// will deny via the missing scope.
		if _, ok := grantedSet[p.String()]; !ok {
			// Check by scope-only too (oauth:gmail granted as oauth:gmail
			// matches; granting just "oauth" does not).
			continue
		}
		scopes[p.Scope] = struct{}{}
		switch p.Scope {
		case "net.outbound":
			netOutbound = append(netOutbound, p.Params...)
		case "oauth":
			oauth = append(oauth, p.Params...)
		case "secrets.read":
			secretsRead = append(secretsRead, p.Params...)
		}
	}

	// Cedar set type expects deterministic ordering; sort to keep
	// policy decisions reproducible across calls.
	scopeList := make([]string, 0, len(scopes))
	for s := range scopes {
		scopeList = append(scopeList, s)
	}
	sort.Strings(scopeList)
	sort.Strings(netOutbound)
	sort.Strings(oauth)
	sort.Strings(secretsRead)
	sort.Strings(dataScopes)

	return map[string]any{
		"id":               install.ID,
		"identifier":       install.Identifier,
		"app_id":           install.AppID,
		"scope":            install.Scope,
		"scope_id":         install.ScopeID,
		"enabled":          install.Enabled,
		"forced":           install.Forced,
		"source":           install.Source,
		"version":          install.Version,
		"permissions":      scopeList,   // Cedar set<string>
		"net_outbound":     netOutbound, // Cedar set<string>
		"oauth_providers":  oauth,
		"secret_providers": secretsRead,
		"data_scopes":      dataScopes,
	}, nil
}

// Installation captures just the runtime fields the translator needs.
// Defined here (not imported from a service-side struct) so this
// package stays a pure transform with no upstream coupling.
type Installation struct {
	ID         string
	Identifier string
	AppID      string
	Scope      string // "org" | "user"
	ScopeID    string
	Enabled    bool
	Forced     bool
	Source     string // bundled | org | marketplace | user_webview
	Version    string
	DataScopes []string
}

// FromManifest is a small helper that pulls manifest.data_scopes into
// an Installation struct alongside whatever fields the caller already
// has from the installations row. Keeps the call sites short.
func FromManifest(m *biuapp.Manifest, base Installation) Installation {
	base.DataScopes = append([]string(nil), m.DataScopes...)
	return base
}

// ─── Action vocabulary ────────────────────────────────────────────
//
// The runtime calls Authz.Decide with one of these action strings.
// Adding a new action requires:
//   1. New permit rule in deploy/.../policies/20-apps.cedar
//   2. New constant here
//   3. Test coverage in app.cedar tests under services/authz
//
// We keep them in a single file rather than scattered constants so
// the audit story is "what can apps possibly do = read this list".

const (
	// Lifecycle (caller is User; resource is App or Installation).
	ActionInstall   = "app:install"
	ActionUninstall = "app:uninstall"
	ActionUpgrade   = "app:upgrade"
	ActionConfigure = "app:configure"
	ActionEnable    = "app:enable"
	ActionDisable   = "app:disable"

	// Runtime (caller is User or AgentSession; resource is Installation).
	ActionInvoke    = "app:invoke"
	ActionReadData  = "app:read_data"
	ActionWriteData = "app:write_data"

	// Agent grants (caller is User; resource is Installation).
	ActionGrantAgent  = "app:grant_agent"
	ActionRevokeAgent = "app:revoke_agent"

	// Sidebar (caller is User; resource is User).
	ActionSidebarRead  = "sidebar:read"
	ActionSidebarWrite = "sidebar:write"

	// Marketplace (caller is User; resource is App catalogue row).
	ActionPublish   = "app:publish"
	ActionDeprecate = "app:deprecate"
	ActionSuspend   = "app:suspend"
)

// AllActions returns the full vocabulary, sorted, for tooling
// (audit, docs generation).
func AllActions() []string {
	all := []string{
		ActionInstall, ActionUninstall, ActionUpgrade,
		ActionConfigure, ActionEnable, ActionDisable,
		ActionInvoke, ActionReadData, ActionWriteData,
		ActionGrantAgent, ActionRevokeAgent,
		ActionSidebarRead, ActionSidebarWrite,
		ActionPublish, ActionDeprecate, ActionSuspend,
	}
	sort.Strings(all)
	return all
}
