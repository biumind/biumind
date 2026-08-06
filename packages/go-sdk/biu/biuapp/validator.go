// Manifest validation.
//
// The loader is forgiving (unknown keys ignored, missing fields
// tolerated). The validator is the strict layer: it returns errors
// that block install / publish / startup.
//
// We split semantic groups into helpers so callers can pick a
// stricter or looser stance:
//
//   * Validate          — the full check used by `biu app validate`,
//                          server-side install path, marketplace
//                          submission. Returns all errors found, not
//                          just the first.
//   * ValidateBundled   — looser variant used at service startup for
//                          in-tree bundled apps. Skips checks that
//                          only matter for distribution (signature,
//                          marketplace-required fields, scoped
//                          identifier shape).
//
// Validation errors are wrapped in `*ValidationError` which carries a
// list of field-paths so the UI can render per-field errors at the
// install / submission step.

package biuapp

import (
	"fmt"
	"regexp"
	"strings"
)

// ValidationError aggregates one or more field-level problems into a
// single error returned by Validate. Use errors.As to extract.
type ValidationError struct {
	Issues []ValidationIssue
}

type ValidationIssue struct {
	Path    string // dotted path into manifest, e.g. "actions[2].input_schema"
	Code    string // short stable code, e.g. "invalid_cron"
	Message string // human-readable detail
}

func (e *ValidationError) Error() string {
	parts := make([]string, 0, len(e.Issues))
	for _, i := range e.Issues {
		parts = append(parts, fmt.Sprintf("%s: %s", i.Path, i.Message))
	}
	return "manifest validation: " + strings.Join(parts, "; ")
}

// ─── Whitelists / regexes ──────────────────────────────────────────

// permissionPrefixes is the set of platform-recognised permission
// scopes (the bit before any ":" parameter). Any manifest permission
// that doesn't start with one of these is rejected. Adding a new
// permission requires adding the prefix here AND a matching Cedar
// translation rule in services/authz.
var permissionPrefixes = map[string]struct{}{
	"net.outbound":       {},
	"hub.invoke":         {}, // legacy alias; kept for v1.0 compat
	"model-relay.invoke": {},
	"wiki.read":          {},
	"wiki.write":         {},
	"graph.read":         {},
	"graph.write":        {},
	"memory.read":        {},
	"memory.write":       {},
	"files.read":         {},
	"files.write":        {},
	"cron.register":      {},
	"webhook.register":   {},
	"notify.send":        {},
	"sandbox.exec":       {},
	"oauth":              {}, // oauth:<provider>
	"secrets.read":       {}, // secrets.read:<provider>
}

var validCategories = map[string]struct{}{
	"productivity": {}, "content": {}, "data": {},
	"comm": {}, "dev": {}, "utility": {},
}

var validKinds = map[string]struct{}{
	"backend": {}, "view": {}, "hybrid": {}, "webview": {}, "container": {},
}

var validRisks = map[ActionRisk]struct{}{
	RiskLow: {}, RiskMedium: {}, RiskHigh: {},
}

var validInterventions = map[HumanIntervention]struct{}{
	"":                   {}, // empty = use action default per risk
	InterventionNever:    {},
	InterventionOptional: {},
	InterventionRequired: {},
}

var validLayouts = map[ViewLayout]struct{}{
	LayoutList: {}, LayoutListDetail: {}, LayoutForm: {}, LayoutWebView: {},
	LayoutGrid: {}, LayoutDashboard: {}, LayoutAgentChat: {}, LayoutCustom: {},
}

// slugRe — kebab-case identifier or `<scope>/<slug>` for marketplace.
// Allows dots in scope to support reverse-DNS-style author handles
// (e.g. `com.acme/cool-app`).
var slugRe = regexp.MustCompile(`^[a-z][a-z0-9._-]*(\/[a-z][a-z0-9-]*)?$`)

// actionNameRe — pure kebab-case (no slashes; that's the slug).
var actionNameRe = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

// semverRe — a permissive semver matcher; full spec is overkill here.
var semverRe = regexp.MustCompile(`^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$`)

// cronFieldRe — each cron field accepts these characters; we don't
// fully parse, just sanity-check. The dispatcher (M4) does proper
// parsing via robfig/cron at registration time.
var cronFieldRe = regexp.MustCompile(`^[\d*/,\-?LWA-Za-z]+$`)

// routeRe — view routes must begin with /apps/<slug>; the slug part
// is checked against the manifest's own identifier in checkViews.
var routeRe = regexp.MustCompile(`^/apps/[a-z][a-z0-9._/:-]*$`)

// ─── Public entrypoints ────────────────────────────────────────────

// Validate runs the full set of checks. Use for install paths /
// marketplace submission / `biu app validate` CLI.
func Validate(m *Manifest) error {
	v := newValidator(m, false)
	v.run()
	return v.result()
}

// ValidateBundled is the relaxed variant: it skips marketplace-only
// checks (signature presence, scoped identifier shape, etc.). Use
// at service startup so a bundled app under development doesn't
// have to mint signing keys to compile.
func ValidateBundled(m *Manifest) error {
	v := newValidator(m, true)
	v.run()
	return v.result()
}

// ─── Implementation ────────────────────────────────────────────────

type validator struct {
	m       *Manifest
	bundled bool // skip marketplace-only checks
	issues  []ValidationIssue
}

func newValidator(m *Manifest, bundled bool) *validator {
	return &validator{m: m, bundled: bundled}
}

func (v *validator) add(path, code, msg string) {
	v.issues = append(v.issues, ValidationIssue{Path: path, Code: code, Message: msg})
}

func (v *validator) result() error {
	if len(v.issues) == 0 {
		return nil
	}
	return &ValidationError{Issues: v.issues}
}

func (v *validator) run() {
	v.checkIdentity()
	v.checkPermissions()
	v.checkActions()
	v.checkViews()
	v.checkTriggers()
	v.checkSidebar()
	v.checkSkillsAndRequires()
	v.checkIcon()
}

// checkIcon — manifest.icon 字段格式校验。允许:
//
//  1. 空字符串 (客户端 fallback)
//  2. 单字符 / 短文字 (≤ 8 字节, 给 emoji 用 — emoji UTF-8 多字节 + ZWJ
//     组合最多约 7 字节)
//  3. http(s):// URL (公网 / 用户自填)
//  4. cas:<sha256> — 64 位小写 hex 表示的 sha256
//
// 拒绝其它前缀 (`caz:` / `cas: ` 含空格 / 错长度的 sha) — 这种 typo
// 客户端只能 letter fallback, 早 fail 让作者立即看到。
func (v *validator) checkIcon() {
	icon := v.m.Icon
	if icon == "" {
		return
	}
	if strings.HasPrefix(icon, "cas:") {
		sha := strings.TrimPrefix(icon, "cas:")
		if len(sha) != 64 {
			v.add("icon", "invalid_cas_hash",
				fmt.Sprintf("cas:<sha256> requires 64 hex chars, got %d", len(sha)))
			return
		}
		for _, c := range sha {
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
				v.add("icon", "invalid_cas_hash",
					"cas:<sha256> must be lowercase hex")
				return
			}
		}
		return
	}
	if strings.HasPrefix(icon, "http://") || strings.HasPrefix(icon, "https://") {
		return
	}
	// emoji / 短文字 — 字节长度兜底 (≤ 8, 防止整段 URL 漏写 scheme 当
	// emoji 处理)。
	if len(icon) > 8 {
		v.add("icon", "unknown_format",
			"icon must be empty / emoji / http(s) URL / cas:<sha256>; long literals "+
				"are likely a typo (e.g. 'caz:...' / 'cas:abc' / 'kimi.com')")
	}
}

func (v *validator) checkIdentity() {
	slug := v.m.Slug()
	if slug == "" {
		v.add("identifier", "missing", "either name (legacy) or identifier must be set")
	} else if !slugRe.MatchString(slug) {
		v.add("identifier", "invalid_slug",
			"must be lowercase kebab-case (or scoped <author>/<slug> for marketplace)")
	}
	// Marketplace submission requires scoped identifier (decision §21#8).
	// We cannot tell here whether this manifest is being submitted to
	// marketplace vs installed bundled, so the publish path applies the
	// "must contain '/'" check itself; the validator stays neutral.

	if v.m.Version == "" {
		v.add("version", "missing", "version is required")
	} else if !semverRe.MatchString(v.m.Version) {
		v.add("version", "invalid_semver", "version must be semver (e.g. 1.2.3)")
	}

	if v.m.Description == "" {
		v.add("description", "missing", "description is required")
	} else if len(v.m.Description) > 200 {
		v.add("description", "too_long", "description must be ≤ 200 characters")
	}

	if v.m.Category != "" {
		if _, ok := validCategories[v.m.Category]; !ok {
			v.add("category", "invalid", "must be one of productivity|content|data|comm|dev|utility")
		}
	}

	if v.m.Kind != "" {
		if _, ok := validKinds[v.m.Kind]; !ok {
			v.add("kind", "invalid", "must be one of backend|view|hybrid|webview|container")
		}
		// Container kind is reserved but not yet implementable. Decision
		// (2026-05-30): M14 推迟到 v2.5 与 marketplace M19 投稿审核 pipeline
		// 合并；manifest 协议保留 kind=container 枚举值以保持向后兼容，
		// 但 install path 拒绝它直到 M19 落地。详见 DevPlan §3 M14。
		if v.m.Kind == "container" {
			v.add("kind", "not_yet_supported",
				"container 形态计划于 v2.5 开放（M14 → M19 marketplace pipeline 合并）；"+
					"目前 v2.0 仅支持 backend|view|hybrid|webview")
		}
	}
}

func (v *validator) checkPermissions() {
	for i, p := range v.m.Permissions {
		path := fmt.Sprintf("permissions[%d]", i)
		// Split off the optional `:param` tail so we check the prefix
		// alone against the whitelist. Multi-value params like
		// `net.outbound:*.a.com,*.b.com` are accepted (they go through
		// to Cedar's domain-pattern matcher unchanged).
		head := p
		if idx := strings.IndexByte(p, ':'); idx >= 0 {
			head = p[:idx]
		}
		if _, ok := permissionPrefixes[head]; !ok {
			v.add(path, "unknown_permission",
				fmt.Sprintf("%q is not a recognised permission scope", p))
		}
	}
}

func (v *validator) checkActions() {
	seen := map[string]int{}
	for i, a := range v.m.Actions {
		path := fmt.Sprintf("actions[%d]", i)
		if a.Name == "" {
			v.add(path+".name", "missing", "action name is required")
			continue
		}
		if !actionNameRe.MatchString(a.Name) {
			v.add(path+".name", "invalid_name",
				"action name must be kebab/snake-case (a-z0-9_-)")
		}
		if prev, dup := seen[a.Name]; dup {
			v.add(path+".name", "duplicate",
				fmt.Sprintf("duplicate action name %q (also at actions[%d])", a.Name, prev))
		}
		seen[a.Name] = i

		if a.Risk != "" {
			if _, ok := validRisks[a.Risk]; !ok {
				v.add(path+".risk", "invalid", "must be one of low|medium|high")
			}
		}
		if _, ok := validInterventions[a.HumanIntervention]; !ok {
			v.add(path+".human_intervention", "invalid",
				"must be empty or one of never|optional|required")
		}
		if a.TimeoutMs < 0 || a.TimeoutMs > 600_000 {
			v.add(path+".timeout_ms", "out_of_range",
				"timeout_ms must be in [0, 600000] (0 = service default)")
		}
		if a.RateLimit != nil {
			if a.RateLimit.PerMinute < 0 || a.RateLimit.PerHour < 0 || a.RateLimit.PerDay < 0 {
				v.add(path+".rate_limit", "negative", "rate limits must be non-negative")
			}
		}
	}
}

func (v *validator) checkViews() {
	slug := v.m.Slug()
	actionSet := map[string]struct{}{}
	for _, a := range v.m.Actions {
		actionSet[a.Name] = struct{}{}
	}
	viewIDs := map[string]int{}

	for i, view := range v.m.Views {
		path := fmt.Sprintf("views[%d]", i)
		if view.ID == "" {
			v.add(path+".id", "missing", "view id is required")
		} else if prev, dup := viewIDs[view.ID]; dup {
			v.add(path+".id", "duplicate",
				fmt.Sprintf("duplicate view id %q (also at views[%d])", view.ID, prev))
		} else {
			viewIDs[view.ID] = i
		}

		if view.Route == "" {
			v.add(path+".route", "missing", "view route is required")
		} else {
			if !routeRe.MatchString(view.Route) {
				v.add(path+".route", "invalid", "route must start with /apps/<slug>")
			}
			if slug != "" && !strings.HasPrefix(view.Route, "/apps/"+slug) {
				v.add(path+".route", "wrong_prefix",
					fmt.Sprintf("route must start with /apps/%s", slug))
			}
		}

		if _, ok := validLayouts[view.Layout]; !ok {
			v.add(path+".layout", "invalid", "unknown layout")
		}

		// Per-layout required-field checks. Keep these narrow: the
		// validator's job is to prevent obvious manifest errors, not
		// to enforce every renderer rule.
		switch view.Layout {
		case LayoutWebView:
			if view.URL == "" {
				v.add(path+".url", "missing", "webview layout requires url")
			}
		case LayoutAgentChat:
			if view.AgentID == "" {
				v.add(path+".agent_id", "missing", "agent_chat layout requires agent_id")
			}
		case LayoutForm:
			if view.SchemaRef == "" && view.Submit == nil {
				v.add(path, "underspecified",
					"form layout requires schema_ref and/or submit")
			}
			if view.Submit != nil && view.Submit.Action != "" {
				if _, ok := actionSet[view.Submit.Action]; !ok {
					v.add(path+".submit.action", "unknown_action",
						fmt.Sprintf("submit action %q not declared in actions[]", view.Submit.Action))
				}
			}
		case LayoutDashboard:
			if len(view.Cards) == 0 {
				v.add(path+".cards", "missing", "dashboard layout requires at least one card")
			}
			cardIDs := map[string]int{}
			for j, c := range view.Cards {
				cp := fmt.Sprintf("%s.cards[%d]", path, j)
				if c.ID == "" {
					v.add(cp+".id", "missing", "card id is required")
				} else if prev, dup := cardIDs[c.ID]; dup {
					v.add(cp+".id", "duplicate",
						fmt.Sprintf("duplicate card id %q (also at cards[%d])", c.ID, prev))
				} else {
					cardIDs[c.ID] = j
				}
				if c.Span < 0 || c.Span > 12 {
					v.add(cp+".span", "out_of_range", "span must be in [1, 12]")
				}
				if c.Kind != "" {
					switch c.Kind {
					case "text", "number", "list", "chart":
						// ok
					default:
						v.add(cp+".kind", "invalid", "kind must be one of text|number|list|chart")
					}
				}
				if c.DataSource != nil && c.DataSource.Action != "" {
					if _, ok := actionSet[c.DataSource.Action]; !ok {
						v.add(cp+".data_source.action", "unknown_action",
							fmt.Sprintf("%q not in actions[]", c.DataSource.Action))
					}
				}
			}
		case LayoutGrid:
			if view.ItemTemplate == nil {
				v.add(path+".item_template", "missing", "grid layout requires item_template")
			}
			if view.Grid != nil {
				for j, c := range view.Grid.Columns {
					if c < 1 || c > 6 {
						v.add(fmt.Sprintf("%s.grid.columns[%d]", path, j),
							"out_of_range", "columns must be in [1, 6]")
					}
				}
				if view.Grid.AspectRatio < 0 {
					v.add(path+".grid.aspect_ratio", "negative", "aspect_ratio must be ≥ 0")
				}
				if view.Grid.Spacing < 0 {
					v.add(path+".grid.spacing", "negative", "spacing must be ≥ 0")
				}
			}
		}

		// Cross-check toolbar / item_template action references.
		for j, a := range view.Toolbar {
			if a.Action != "" {
				if _, ok := actionSet[a.Action]; !ok {
					v.add(fmt.Sprintf("%s.toolbar[%d].action", path, j),
						"unknown_action",
						fmt.Sprintf("%q not in actions[]", a.Action))
				}
			}
		}
		if view.ItemTemplate != nil {
			for j, a := range view.ItemTemplate.Actions {
				if a.Action != "" {
					if _, ok := actionSet[a.Action]; !ok {
						v.add(fmt.Sprintf("%s.item_template.actions[%d].action", path, j),
							"unknown_action",
							fmt.Sprintf("%q not in actions[]", a.Action))
					}
				}
			}
		}

		// data_source.action (if set) must point to a real action.
		if view.DataSource != nil && view.DataSource.Action != "" {
			if _, ok := actionSet[view.DataSource.Action]; !ok {
				v.add(path+".data_source.action", "unknown_action",
					fmt.Sprintf("%q not in actions[]", view.DataSource.Action))
			}
		}
	}
}

func (v *validator) checkTriggers() {
	actionSet := map[string]struct{}{}
	for _, a := range v.m.Actions {
		actionSet[a.Name] = struct{}{}
	}
	names := map[string]int{}
	for i, t := range v.m.Triggers {
		path := fmt.Sprintf("triggers[%d]", i)
		if t.Name == "" {
			v.add(path+".name", "missing", "trigger name is required")
		} else if prev, dup := names[t.Name]; dup {
			v.add(path+".name", "duplicate",
				fmt.Sprintf("duplicate trigger name %q (also at triggers[%d])", t.Name, prev))
		} else {
			names[t.Name] = i
		}
		if t.Action == "" {
			v.add(path+".action", "missing", "trigger.action is required")
		} else if _, ok := actionSet[t.Action]; !ok {
			v.add(path+".action", "unknown_action",
				fmt.Sprintf("%q not in actions[]", t.Action))
		}

		switch t.Kind {
		case TriggerCron:
			v.checkCron(path, t.Expr)
		case TriggerWebhook:
			if t.Path == "" {
				v.add(path+".path", "missing", "webhook trigger requires path")
			} else if !strings.HasPrefix(t.Path, "/") {
				v.add(path+".path", "invalid", "webhook path must begin with '/'")
			}
			if t.Auth != "" && t.Auth != "hmac" && t.Auth != "none" {
				v.add(path+".auth", "invalid", "auth must be hmac|none")
			}
		case TriggerInbox:
			if t.Pattern == "" {
				v.add(path+".pattern", "missing", "inbox trigger requires pattern")
			}
		default:
			v.add(path+".kind", "invalid", "kind must be cron|webhook|inbox")
		}
	}
}

func (v *validator) checkCron(path, expr string) {
	if expr == "" {
		v.add(path+".expr", "missing", "cron expr is required")
		return
	}
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		v.add(path+".expr", "invalid",
			"cron expr must be 5 fields (minute hour dom month dow)")
		return
	}
	for j, f := range fields {
		if !cronFieldRe.MatchString(f) {
			v.add(path+".expr", "invalid_chars",
				fmt.Sprintf("cron field %d (%q) contains illegal characters", j, f))
			return
		}
	}
	// Decision: minimum interval is 1 minute. The "every minute" pattern
	// (`* * * * *`) is rejected; users with that need should use cron.
	// register=false and run their loop in their own runtime.
	if fields[0] == "*" {
		v.add(path+".expr", "too_frequent",
			"minimum interval is 1 minute; '* * * * *' (every minute) is not allowed")
	}
}

func (v *validator) checkSidebar() {
	if v.m.Sidebar == nil {
		return
	}
	s := v.m.Sidebar
	if s.PreferredPosition != "" {
		switch s.PreferredPosition {
		case "top", "middle", "bottom":
			// ok
		default:
			v.add("sidebar.preferred_position", "invalid",
				"must be top|middle|bottom")
		}
	}
	if s.BadgeAction != "" {
		actionSet := map[string]struct{}{}
		for _, a := range v.m.Actions {
			actionSet[a.Name] = struct{}{}
		}
		if _, ok := actionSet[s.BadgeAction]; !ok {
			v.add("sidebar.badge_action", "unknown_action",
				fmt.Sprintf("%q not in actions[]", s.BadgeAction))
		}
	}
	if s.BadgeRefreshSec != 0 && s.BadgeRefreshSec < 60 {
		v.add("sidebar.badge_refresh", "too_frequent",
			"badge_refresh must be ≥ 60 seconds")
	}
	// default_pin=true is platform-restricted to bundled/org. The
	// validator can't tell the source here; the install path enforces.
}

func (v *validator) checkSkillsAndRequires() {
	skillIDs := map[string]int{}
	for i, s := range v.m.Skills {
		path := fmt.Sprintf("skills[%d]", i)
		if s.Identifier == "" {
			v.add(path+".identifier", "missing", "skill identifier is required")
		}
		if s.File == "" {
			v.add(path+".file", "missing", "skill file path is required")
		}
		if prev, dup := skillIDs[s.Identifier]; dup && s.Identifier != "" {
			v.add(path+".identifier", "duplicate",
				fmt.Sprintf("duplicate skill identifier %q (also at skills[%d])", s.Identifier, prev))
		} else {
			skillIDs[s.Identifier] = i
		}
	}
	for i, r := range v.m.Requires {
		path := fmt.Sprintf("requires[%d]", i)
		if r.Kind != "app" && r.Kind != "mcp_server" {
			v.add(path+".kind", "invalid", "kind must be app|mcp_server")
		}
		if r.Identifier == "" {
			v.add(path+".identifier", "missing", "requirement identifier is required")
		}
		if r.MinVersion != "" && !semverRe.MatchString(r.MinVersion) {
			v.add(path+".min_version", "invalid_semver", "must be semver")
		}
	}
}
