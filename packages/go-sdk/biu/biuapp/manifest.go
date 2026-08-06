// Manifest schema extensions for App Center v1.5+.
//
// The original Manifest (v1.0) contained only Name/Version/Description/
// Author/Actions/Permissions/UI. v1.5 broadens it to drive declarative
// view rendering, scheduled triggers, attached Skills, marketplace
// billing, and sidebar hints — without breaking existing in-tree
// bundled apps that build literal Manifest{Name:"rss",...} structs.
//
// Compat strategy:
//   * Manifest.Name (the v1.0 field) stays untouched as the routing
//     slug; existing bundled apps need no source change.
//   * New fields are additive with `omitempty` so JSON serialisation
//     of v1.0 manifests is byte-compatible.
//   * Author is kept as a plain string field for v1.0 compat; richer
//     metadata lands as sibling fields (AuthorURL, AuthorPublicKey)
//     instead of changing the type of Author.
//   * The YAML loader (loader.go) maps `identifier:` → Identifier
//     (preferred) and `name:` → Title (display); on-the-wire JSON
//     and Go struct usage continue to use Name as the slug.
//
// Validation lives in validator.go; this file is shape-only.

package biuapp

// ─── ActionSpec extensions ─────────────────────────────────────────

// ActionRisk classifies the security/cost weight of an action. Drives
// PermissionMode policy (auto/ask/dangerously) at the Runtime tools
// layer and decides whether the call must enter Sandbox.
type ActionRisk string

const (
	RiskLow    ActionRisk = "low"
	RiskMedium ActionRisk = "medium"
	RiskHigh   ActionRisk = "high"
)

// HumanIntervention overrides the per-user PermissionMode default for
// a specific action. "required" forces an approval prompt regardless
// of mode (used for actions that must always have human-in-the-loop).
type HumanIntervention string

const (
	InterventionNever    HumanIntervention = "never"
	InterventionOptional HumanIntervention = "optional"
	InterventionRequired HumanIntervention = "required"
)

// RateLimit caps how often a single installation can invoke this
// action. Enforced server-side via Redis sliding window. Both fields
// optional; zero values mean "no limit" / "service default".
type RateLimit struct {
	PerMinute int `json:"per_minute,omitempty" yaml:"per_minute,omitempty"`
	PerHour   int `json:"per_hour,omitempty"   yaml:"per_hour,omitempty"`
	PerDay    int `json:"per_day,omitempty"    yaml:"per_day,omitempty"`
}

// CostEstimate gives the platform a hint for budget gating before the
// action is run. Optional; advisory only — actual cost is recorded
// post-hoc in app_center.invocations.
type CostEstimate struct {
	RelayTokensMax  int `json:"hub_tokens_max,omitempty"  yaml:"hub_tokens_max,omitempty"`
	DurationMsP95 int `json:"duration_ms_p95,omitempty" yaml:"duration_ms_p95,omitempty"`
}

// ─── View specification ────────────────────────────────────────────

// ViewLayout enumerates the renderer kinds AppViewHost knows. v1.5
// supports a subset; v2.0 adds grid/dashboard/agent_chat/custom.
type ViewLayout string

const (
	LayoutList       ViewLayout = "list"
	LayoutListDetail ViewLayout = "list_detail"
	LayoutForm       ViewLayout = "form"
	LayoutWebView    ViewLayout = "webview"
	LayoutGrid       ViewLayout = "grid"        // v2.0
	LayoutDashboard  ViewLayout = "dashboard"   // v2.0
	LayoutAgentChat  ViewLayout = "agent_chat"  // v2.0
	LayoutCustom     ViewLayout = "custom"      // v2.0 — A2UI subtree
)

// ViewSpec describes one route/screen the App contributes to the
// client. The set of legal fields per layout is enforced in the
// validator; this struct holds the union.
type ViewSpec struct {
	ID     string     `json:"id"               yaml:"id"`
	Route  string     `json:"route"            yaml:"route"`              // must start with /apps/<identifier>
	Title  string     `json:"title,omitempty"  yaml:"title,omitempty"`    // i18n key allowed
	Layout ViewLayout `json:"layout"           yaml:"layout"`

	// DataSource declares what action the client should call when
	// opening the view (and pass its result as the rendering context).
	DataSource *ViewDataSource `json:"data_source,omitempty" yaml:"data_source,omitempty"`

	// RefreshOn lists Realtime topic event names that should
	// invalidate the cached data. Special token "<self>" is replaced
	// with the resolved install_id at runtime.
	RefreshOn []string `json:"refresh_on,omitempty" yaml:"refresh_on,omitempty"`

	// ItemTemplate (list / list_detail / grid only) describes how
	// each element of DataSource.items renders.
	ItemTemplate *ViewItemTemplate `json:"item_template,omitempty" yaml:"item_template,omitempty"`

	// DetailView (list_detail only) — id of the sub-view to navigate
	// to on item tap. The route is resolved by the client.
	DetailView string `json:"detail_view,omitempty" yaml:"detail_view,omitempty"`

	// Toolbar (list / list_detail / grid / dashboard) — top-bar
	// actions / buttons.
	Toolbar []ViewActionRef `json:"toolbar,omitempty" yaml:"toolbar,omitempty"`

	// SchemaRef (form layout) — dotted path into manifest, e.g.
	// "actions.subscribe.input_schema" — re-uses the schema directly
	// rather than duplicating it inline.
	SchemaRef string         `json:"schema_ref,omitempty" yaml:"schema_ref,omitempty"`
	Submit    *FormSubmit    `json:"submit,omitempty"     yaml:"submit,omitempty"`

	// URL (webview layout) — required iff Layout==webview.
	URL string `json:"url,omitempty" yaml:"url,omitempty"`

	// AgentID (agent_chat layout) — required iff Layout==agent_chat.
	AgentID string `json:"agent_id,omitempty" yaml:"agent_id,omitempty"`

	// Cards (dashboard layout) — list of card descriptors, each with
	// its own data_source and renderer kind.
	Cards []ViewCard `json:"cards,omitempty" yaml:"cards,omitempty"`

	// Grid (grid layout) — responsive column hints + tile config.
	Grid *ViewGrid `json:"grid,omitempty" yaml:"grid,omitempty"`

	// AgentChat (agent_chat layout) — initial prompt + tool filter for
	// the embedded conversation panel.
	AgentChat *ViewAgentChat `json:"agent_chat,omitempty" yaml:"agent_chat,omitempty"`

	// Pagination (list / grid) — server-driven paging hints.
	Pagination *ViewPagination `json:"pagination,omitempty" yaml:"pagination,omitempty"`
}

type ViewDataSource struct {
	Action string         `json:"action"          yaml:"action"`
	Input  map[string]any `json:"input,omitempty" yaml:"input,omitempty"`
}

type ViewItemTemplate struct {
	// Kind ∈ {text, card, kv_list, chart, markdown, progress, custom_widget}
	Kind     string          `json:"kind"               yaml:"kind"`
	Title    string          `json:"title,omitempty"    yaml:"title,omitempty"`
	Subtitle string          `json:"subtitle,omitempty" yaml:"subtitle,omitempty"`
	Body     string          `json:"body,omitempty"     yaml:"body,omitempty"`
	Image    string          `json:"image,omitempty"    yaml:"image,omitempty"`
	Actions  []ViewActionRef `json:"actions,omitempty"  yaml:"actions,omitempty"`

	// Custom widget (v2.0): widget_type identifies an A2UI block kind
	// the App will push via Realtime; props are interpolated via
	// item template syntax (${item.x}).
	WidgetType string         `json:"widget_type,omitempty" yaml:"widget_type,omitempty"`
	Props      map[string]any `json:"props,omitempty"        yaml:"props,omitempty"`
}

// ViewActionRef binds a UI affordance (button / list item action /
// toolbar) to either an in-app action call or a route navigation.
// At least one of Action/Route must be set; the validator enforces.
type ViewActionRef struct {
	Label   string         `json:"label"             yaml:"label"`
	Icon    string         `json:"icon,omitempty"    yaml:"icon,omitempty"`
	Action  string         `json:"action,omitempty"  yaml:"action,omitempty"`
	Input   map[string]any `json:"input,omitempty"   yaml:"input,omitempty"`
	Route   string         `json:"route,omitempty"   yaml:"route,omitempty"`
	Confirm string         `json:"confirm,omitempty" yaml:"confirm,omitempty"` // confirm prompt text

	// RiskWarning text shown as a secondary confirm for high-impact
	// actions even if PermissionMode would normally auto-approve.
	RiskWarning string `json:"risk_warning,omitempty" yaml:"risk_warning,omitempty"`

	// OnSuccess decorates the post-call UX without round-tripping the
	// action itself (toast / refresh / navigate).
	OnSuccess *ViewActionEffect `json:"on_success,omitempty" yaml:"on_success,omitempty"`
}

type ViewActionEffect struct {
	Toast    string `json:"toast,omitempty"    yaml:"toast,omitempty"`
	Refresh  bool   `json:"refresh,omitempty"  yaml:"refresh,omitempty"`
	Navigate string `json:"navigate,omitempty" yaml:"navigate,omitempty"`
}

type FormSubmit struct {
	Action    string            `json:"action"               yaml:"action"`
	OnSuccess *ViewActionEffect `json:"on_success,omitempty" yaml:"on_success,omitempty"`
}

type ViewCard struct {
	ID         string          `json:"id"                    yaml:"id"`
	Title      string          `json:"title,omitempty"       yaml:"title,omitempty"`
	Kind       string          `json:"kind,omitempty"        yaml:"kind,omitempty"`         // text|number|list|chart (chart = placeholder in v2.0)
	DataSource *ViewDataSource `json:"data_source,omitempty" yaml:"data_source,omitempty"`

	// Span sizes the card across the 12-column dashboard grid.
	// Defaults to 4 (3-up on wide). Clamped to [1, 12] at render time.
	Span int `json:"span,omitempty" yaml:"span,omitempty"`

	// Field is a dotted path into the card's data_source response that
	// the renderer reads. For kind=number → "data.count". For kind=list
	// → "items". Empty defaults to the whole payload.
	Field string `json:"field,omitempty" yaml:"field,omitempty"`

	// Format hint applied to the resolved value: "comma" for thousands
	// separators on numbers, "percent" for fractional → % conversion.
	// Optional; renderer falls back to default str()ify.
	Format string `json:"format,omitempty" yaml:"format,omitempty"`
}

// ViewGrid configures the grid layout's responsive behaviour. The
// item rendering itself reuses ViewItemTemplate (same as list) so
// authors aren't forced to duplicate template fields.
type ViewGrid struct {
	// Columns is a triplet [narrow, medium, wide] mapped to the
	// MaterialBreakpoints used by Flutter (≤600 / 600-1200 / >1200 dp).
	// Empty array → defaults [1, 2, 3]. Length-1 → constant columns.
	// Values clamped to [1, 6].
	Columns []int `json:"columns,omitempty" yaml:"columns,omitempty"`

	// Spacing in dp between tiles. Default 12.
	Spacing int `json:"spacing,omitempty" yaml:"spacing,omitempty"`

	// AspectRatio (width/height) for each tile. Default 1.0 (square).
	AspectRatio float64 `json:"aspect_ratio,omitempty" yaml:"aspect_ratio,omitempty"`
}

// ViewAgentChat configures the embedded conversation panel.
type ViewAgentChat struct {
	// InitialPrompt seeds the first user message when the panel opens.
	// Supports the same ${...} interpolation as item templates so the
	// panel can carry context from the host view's route params.
	InitialPrompt string `json:"initial_prompt,omitempty" yaml:"initial_prompt,omitempty"`

	// ToolFilter restricts the agent's tool fleet to a whitelist of
	// tool name prefixes (e.g. ["rss.", "memory."]). Empty → no
	// restriction beyond the agent's normal grants.
	ToolFilter []string `json:"tool_filter,omitempty" yaml:"tool_filter,omitempty"`

	// SystemPromptOverride replaces the agent's system prompt for the
	// scope of this embedded session. Optional; when empty the agent's
	// own prompt applies.
	SystemPromptOverride string `json:"system_prompt_override,omitempty" yaml:"system_prompt_override,omitempty"`

	// Title shown above the chat panel. Defaults to view.title.
	Title string `json:"title,omitempty" yaml:"title,omitempty"`
}

type ViewPagination struct {
	PageParam  string `json:"page_param,omitempty"  yaml:"page_param,omitempty"`  // route param name
	TotalField string `json:"total_field,omitempty" yaml:"total_field,omitempty"` // path into data, e.g. "data.total"
	PageSize   int    `json:"page_size,omitempty"   yaml:"page_size,omitempty"`
}

// ─── Triggers ──────────────────────────────────────────────────────

type TriggerKind string

const (
	TriggerCron    TriggerKind = "cron"
	TriggerWebhook TriggerKind = "webhook"
	TriggerInbox   TriggerKind = "inbox"
)

// TriggerSpec describes one autonomous entry point into the App.
// Subset of fields used per Kind; validator enforces.
type TriggerSpec struct {
	Kind TriggerKind `json:"kind" yaml:"kind"`
	Name string      `json:"name" yaml:"name"` // unique within manifest

	// Cron-only fields.
	Expr           string `json:"expr,omitempty"             yaml:"expr,omitempty"`              // standard 5-field cron
	IfInactiveFor  string `json:"if_inactive_for,omitempty"  yaml:"if_inactive_for,omitempty"`   // skip if user idle ≥ duration

	// Webhook-only fields.
	Path          string   `json:"path,omitempty"           yaml:"path,omitempty"`             // e.g. "/callback"
	Auth          string   `json:"auth,omitempty"           yaml:"auth,omitempty"`             // hmac | none
	AcceptMethods []string `json:"accept_methods,omitempty" yaml:"accept_methods,omitempty"`   // POST | GET | ...

	// Inbox-only field — message pattern matched by Channels router.
	Pattern string `json:"pattern,omitempty" yaml:"pattern,omitempty"`

	// All triggers fire an action.
	Action string         `json:"action"           yaml:"action"`
	Input  map[string]any `json:"input,omitempty"  yaml:"input,omitempty"`
}

// ─── Skill bundling ────────────────────────────────────────────────

// SkillRef points at a SKILL.md attached to this App. Installation
// writes these into runtime.skills with source='bundled' and
// manifest.app_id=<this app id>; uninstall cascades.
type SkillRef struct {
	Identifier string `json:"identifier" yaml:"identifier"`
	File       string `json:"file"       yaml:"file"` // relative path within the .biuapp bundle
}

// ─── Marketplace billing ───────────────────────────────────────────

type BillingTier string

const (
	BillingFree  BillingTier = "free"
	BillingPro   BillingTier = "pro"
	BillingUsage BillingTier = "usage"
)

type BillingSpec struct {
	Tier      BillingTier   `json:"tier" yaml:"tier"`
	Price     *BillingPrice `json:"price,omitempty"      yaml:"price,omitempty"`     // pro only
	TrialDays int           `json:"trial_days,omitempty" yaml:"trial_days,omitempty"`
	Meters    []BillingMeter `json:"meters,omitempty" yaml:"meters,omitempty"`        // usage only
}

type BillingPrice struct {
	Currency string  `json:"currency" yaml:"currency"`
	Amount   float64 `json:"amount"   yaml:"amount"`
	Period   string  `json:"period,omitempty" yaml:"period,omitempty"` // monthly | yearly | lifetime
}

type BillingMeter struct {
	Name             string `json:"name"               yaml:"name"`
	Unit             string `json:"unit,omitempty"     yaml:"unit,omitempty"`
	UnitPriceMicro   int64  `json:"unit_price_micro,omitempty" yaml:"unit_price_micro,omitempty"` // microUSD
}

// ─── Sidebar hints ─────────────────────────────────────────────────

// SidebarHints declares the App's preferred sidebar behaviour. Only
// the platform may auto-pin (decision §21#10): bundled / org sources
// can request default_pin=true; marketplace MUST be false.
type SidebarHints struct {
	DefaultPin           bool   `json:"default_pin,omitempty"            yaml:"default_pin,omitempty"`
	PreferredPosition    string `json:"preferred_position,omitempty"     yaml:"preferred_position,omitempty"`     // top|middle|bottom
	BadgeAction          string `json:"badge_action,omitempty"           yaml:"badge_action,omitempty"`           // action name returning {count, severity}
	BadgeRefreshSec      int    `json:"badge_refresh,omitempty"          yaml:"badge_refresh,omitempty"`          // ≥ 60
	MobileBottomEligible bool   `json:"mobile_bottom_eligible,omitempty" yaml:"mobile_bottom_eligible,omitempty"`
}

// ─── Inter-App requirements ────────────────────────────────────────

// Requirement declares a hard dependency on another App or MCP server.
// Validated at install time: missing requirement → install fails with
// a user-facing "first install X" prompt.
type Requirement struct {
	Kind       string `json:"kind"       yaml:"kind"`        // app | mcp_server
	Identifier string `json:"identifier" yaml:"identifier"`
	MinVersion string `json:"min_version,omitempty" yaml:"min_version,omitempty"`
}

// ─── i18n ──────────────────────────────────────────────────────────

type I18nSpec struct {
	Default string   `json:"default,omitempty" yaml:"default,omitempty"`
	Locales []string `json:"locales,omitempty" yaml:"locales,omitempty"`
	Files   string   `json:"files,omitempty"   yaml:"files,omitempty"` // dir within bundle, default "locales/"
}

// ─── Author + version-2 fields ─────────────────────────────────────

// AuthorURL / AuthorPublicKey live as siblings to the legacy Author
// string field (which stays a plain string for v1.0 compat). The YAML
// loader accepts both `author: "Name"` (string form) and
// `author: { name, url, public_key }` (object form) — see loader.go.
//
// Identifier is the preferred slug accessor; if empty, callers should
// fall back to Manifest.Name. The YAML loader populates Identifier
// from the YAML `identifier:` key. Title is the human-readable display
// name; YAML key `name:` maps here. Code that built literal
// `Manifest{Name: "rss"}` continues to work because Name is still the
// slug for routing purposes.

// (We do not redeclare Manifest here — these fields are added in
// biuapp.go via the embedded extension type below.)

// ManifestExt holds v1.5+ fields that augment the v1.0 Manifest. We
// keep them in a separate struct embedded into Manifest so adding
// new sections in v2.0 only touches this file, and the diff against
// v1.0 stays a single embedding line in biuapp.go.
type ManifestExt struct {
	// Identifier and Title are populated by the YAML loader from the
	// `identifier:` and `name:` keys respectively; they're tagged
	// yaml:"-" here because rawManifest in loader.go owns those keys
	// (yaml.v3 panics on duplicate keys across embedded structs).
	// JSON tags remain so the on-the-wire / proto-mapped form has
	// access to them.
	Identifier string `json:"identifier,omitempty" yaml:"-"`
	Title      string `json:"title,omitempty"      yaml:"-"`

	AuthorURL       string `json:"author_url,omitempty"        yaml:"-"` // populated by loader from author.url
	AuthorPublicKey string `json:"author_public_key,omitempty" yaml:"-"` // populated by loader from author.public_key

	Icon     string `json:"icon,omitempty"     yaml:"icon,omitempty"`
	Category string `json:"category,omitempty" yaml:"category,omitempty"`
	Kind     string `json:"kind,omitempty"     yaml:"kind,omitempty"` // backend | view | hybrid | webview | container

	DataScopes []string `json:"data_scopes,omitempty" yaml:"data_scopes,omitempty"`

	Views    []ViewSpec    `json:"views,omitempty"    yaml:"views,omitempty"`
	Triggers []TriggerSpec `json:"triggers,omitempty" yaml:"triggers,omitempty"`
	Skills   []SkillRef    `json:"skills,omitempty"   yaml:"skills,omitempty"`
	Requires []Requirement `json:"requires,omitempty" yaml:"requires,omitempty"`

	Billing *BillingSpec  `json:"billing,omitempty" yaml:"billing,omitempty"`
	Sidebar *SidebarHints `json:"sidebar,omitempty" yaml:"sidebar,omitempty"`
	I18n    *I18nSpec     `json:"i18n,omitempty"    yaml:"i18n,omitempty"`
}

// Slug returns the preferred identifier — Identifier when populated,
// falling back to the v1.0 Name field. Use this in any new code that
// needs the routing slug.
func (m *Manifest) Slug() string {
	if m.Identifier != "" {
		return m.Identifier
	}
	return m.Name
}

// DisplayName returns the human-readable name — Title when populated,
// falling back to Name. UIs should call this rather than reading Name
// directly.
func (m *Manifest) DisplayName() string {
	if m.Title != "" {
		return m.Title
	}
	return m.Name
}
