// Package biuapp defines the contract every BiuApp implements.
//
// A BiuApp is a self-contained capability the platform can expose to
// agents and end-users without baking it into the core. Apps declare what
// actions they support, what permissions they need, and how to fulfill
// invocations. The App Center service hosts a [Registry] of these apps
// and exposes them over HTTP at `/v1/apps/{name}/invoke`.
//
// The contract is intentionally small:
//
//	type App interface {
//	    Manifest() Manifest
//	    Init(ctx, deps Deps) error
//	    Invoke(ctx, action string, in json.RawMessage) (any, error)
//	}
//
// Apps stay pure-Go (testable without the service) and ship as packages
// under packages/go-sdk/biu/biuapp/<name>/.
package biuapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
)

// Manifest describes an app to the registry and to consumers.
// Stable on-the-wire format — additions only, no field repurposing.
//
// v1.5 extensions (Views/Triggers/Skills/etc.) live on the embedded
// ManifestExt — see manifest.go. Existing v1.0 bundled apps that
// build literal `Manifest{Name: "rss", ...}` structs continue to
// compile and run unchanged because every new field is optional.
type Manifest struct {
	Name        string         `json:"name"        yaml:"-"`         // legacy slug; YAML loader fills this from `identifier:`
	Version     string         `json:"version"     yaml:"version"`   // semver
	Description string         `json:"description" yaml:"description"`
	Author      string         `json:"author,omitempty"  yaml:"-"`   // legacy plain-string author; loader fills from author.name
	Actions     []ActionSpec   `json:"actions"           yaml:"actions,omitempty"`
	Permissions []string       `json:"permissions,omitempty" yaml:"permissions,omitempty"`
	UI          map[string]any `json:"ui,omitempty"          yaml:"ui,omitempty"`

	// v1.5+ fields (Views, Triggers, Skills, Sidebar, Billing, ...).
	ManifestExt `yaml:",inline"`
}

// ActionSpec — one callable action on the app.
type ActionSpec struct {
	Name        string `json:"name"                    yaml:"name"`
	Description string `json:"description,omitempty"   yaml:"description,omitempty"`
	// JSON-schema fragments describing input/output. Loose for MVP;
	// tighten with full JSON Schema once we have a validator we like.
	InputSchema  map[string]any `json:"input_schema,omitempty"  yaml:"input_schema,omitempty"`
	OutputSchema map[string]any `json:"output_schema,omitempty" yaml:"output_schema,omitempty"`

	// v1.5+ fields. All optional with zero-value defaults that keep
	// existing literal struct usage compiling.
	Risk              ActionRisk        `json:"risk,omitempty"               yaml:"risk,omitempty"`
	HumanIntervention HumanIntervention `json:"human_intervention,omitempty" yaml:"human_intervention,omitempty"`
	TimeoutMs         int               `json:"timeout_ms,omitempty"         yaml:"timeout_ms,omitempty"`
	Streamable        bool              `json:"streamable,omitempty"         yaml:"streamable,omitempty"`
	RateLimit         *RateLimit        `json:"rate_limit,omitempty"         yaml:"rate_limit,omitempty"`
	CostEstimate      *CostEstimate     `json:"cost_estimate,omitempty"      yaml:"cost_estimate,omitempty"`
}

// Deps — what the registry hands an app at Init time. Any future
// platform-provided service goes here. Add sparingly: the wider this
// surface gets, the harder unit testing becomes.
type Deps struct {
	// HTTP — caller-provided client, lets tests inject a fake.
	HTTP HTTPClient
	// Logger — optional, app falls back to discard.
	Logger Logger
	// Now — overridable clock for deterministic tests.
	Now func() any

	// Events lets the App publish AG-UI CUSTOM events through the
	// platform's outbox (app_center.events). Optional — apps that
	// don't push view-data invalidations can ignore it. Production
	// wires this with a pgxpool-backed implementation; tests pass
	// a NoopEventPublisher.
	//
	// M17: this is the App-author entry point for
	// `biumind.app.view_data_changed` (and any future per-app CUSTOM
	// events). Direct DB writes from app code are forbidden; everything
	// goes through this surface so we keep one unified outbox.
	Events EventPublisher
}

// EventPublisher is the platform surface for App-originated CUSTOM
// events. Returns nil on success; errors are logged but should not
// propagate as the App's primary error path (events are non-critical
// telemetry, not the source of truth).
type EventPublisher interface {
	// PublishViewDataChanged signals to the host client that the data
	// behind one or more views has changed and should be re-fetched.
	// The optional viewIDs argument narrows the invalidation to specific
	// views; an empty list invalidates all views of the App.
	PublishViewDataChanged(ctx context.Context, installID string, viewIDs ...string) error
}

// NoopEventPublisher swallows every publish call. Default zero-value
// for Deps.Events when no platform Events surface is wired.
type NoopEventPublisher struct{}

func (NoopEventPublisher) PublishViewDataChanged(_ context.Context, _ string, _ ...string) error {
	return nil
}

type HTTPClient interface {
	Do(*Request) (*Response, error)
}

// Logger — minimal interface so apps don't pull all of slog.
type Logger interface {
	Debug(msg string, kv ...any)
	Info(msg string, kv ...any)
	Warn(msg string, kv ...any)
	Error(msg string, kv ...any)
}

// Request / Response shapes mirror net/http closely so adapters wrap
// trivially, but kept here so an app's tests can use a fake without
// pulling net/http.
type Request struct {
	Method string
	URL    string
	Header map[string][]string
	Body   []byte
}

type Response struct {
	Status int
	Header map[string][]string
	Body   []byte
}

// App is the contract.
type App interface {
	Manifest() Manifest
	// Init runs once when the registry mounts the app.
	Init(ctx context.Context, deps Deps) error
	// Invoke runs an action; in is opaque JSON the app validates per
	// its declared InputSchema. Output is anything JSON-marshalable.
	Invoke(ctx context.Context, action string, in json.RawMessage) (any, error)
}

// Registry — process-local catalogue of apps. Safe for concurrent use.
type Registry struct {
	mu   sync.RWMutex
	apps map[string]App
	deps Deps
}

func NewRegistry(deps Deps) *Registry {
	return &Registry{apps: map[string]App{}, deps: deps}
}

// Register installs an app. Overwriting an existing app is treated as a
// programming error; apps with duplicate names will panic at startup so
// it's caught in tests rather than at runtime.
func (r *Registry) Register(ctx context.Context, app App) error {
	m := app.Manifest()
	if m.Name == "" {
		return errors.New("biuapp: empty manifest name")
	}
	r.mu.Lock()
	if _, dup := r.apps[m.Name]; dup {
		r.mu.Unlock()
		return fmt.Errorf("biuapp: duplicate app name %q", m.Name)
	}
	r.apps[m.Name] = app
	r.mu.Unlock()
	return app.Init(ctx, r.deps)
}

// Replace overwrites an existing app's registration. Used by the
// user_webview path in app_center where the same identifier can be
// re-registered when the user edits the URL/title (no panic, just
// swap the App impl + re-Init). For all other call sites, prefer
// Register.
func (r *Registry) Replace(ctx context.Context, app App) error {
	m := app.Manifest()
	if m.Name == "" {
		return errors.New("biuapp: empty manifest name")
	}
	r.mu.Lock()
	r.apps[m.Name] = app
	r.mu.Unlock()
	return app.Init(ctx, r.deps)
}

// Unregister removes an app. Used by the user_webview uninstall path
// where the synthesised App should disappear from the in-memory
// catalogue once the user removes the install. No-op if absent.
func (r *Registry) Unregister(name string) {
	r.mu.Lock()
	delete(r.apps, name)
	r.mu.Unlock()
}

func (r *Registry) Get(name string) (App, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.apps[name]
	return a, ok
}

// List returns manifests in deterministic order (sorted by name).
func (r *Registry) List() []Manifest {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Manifest, 0, len(r.apps))
	for _, a := range r.apps {
		out = append(out, a.Manifest())
	}
	sortManifests(out)
	return out
}

// Invoke dispatches a single action. Permission gating lives here so
// apps can stay focused on the business logic; centralising it means
// future RBAC + scope rules apply uniformly.
func (r *Registry) Invoke(ctx context.Context, name, action string, in json.RawMessage) (any, error) {
	app, ok := r.Get(name)
	if !ok {
		return nil, fmt.Errorf("biuapp: unknown app %q", name)
	}
	m := app.Manifest()
	if !hasAction(m, action) {
		return nil, fmt.Errorf("biuapp: %s has no action %q", name, action)
	}
	return app.Invoke(ctx, action, in)
}

// ─── Lifecycle hook dispatchers ───────────────────────────────────
//
// These wrap optional interface assertions so callers don't have to
// repeat them. Each returns nil (silently) when the App didn't
// implement the matching interface — that's by design (decision §10):
// hooks are opt-in, not mandatory.
//
// They DO NOT acquire the registry mutex during the call: hooks may
// run for a long time (OAuth bootstrap, external API calls), and we
// don't want to block other Get / Invoke / Register on them. The
// app pointer itself is captured by Get; concurrent uninstall would
// race against the running hook, which is the App author's problem
// (the hook should be idempotent / cancel-aware).

// DispatchOnInstall calls app.OnInstall if the App implements
// LifecycleHooks. Errors propagate so the caller can roll back the
// installations row.
func (r *Registry) DispatchOnInstall(ctx context.Context, name string, in Install) error {
	app, ok := r.Get(name)
	if !ok {
		return fmt.Errorf("biuapp: unknown app %q", name)
	}
	if h, ok := app.(LifecycleHooks); ok {
		return h.OnInstall(ctx, in)
	}
	return nil
}

// DispatchOnUninstall calls app.OnUninstall if implemented. The
// caller still cascades DB cleanup unconditionally — the hook is for
// the App's external bookkeeping (revoke remote webhook, sign out of
// OAuth, etc.), not for DB.
func (r *Registry) DispatchOnUninstall(ctx context.Context, name string, in Install) error {
	app, ok := r.Get(name)
	if !ok {
		// Uninstalling an app no longer in the registry is fine — it
		// might be an org-private app that was deregistered before
		// the install row was cleaned up. Silent skip.
		return nil
	}
	if h, ok := app.(LifecycleHooks); ok {
		return h.OnUninstall(ctx, in)
	}
	return nil
}

// DispatchOnUpgrade calls app.OnUpgrade if implemented. fromVersion
// is the version the install was on BEFORE the upgrade; the new
// version is in in.Version.
func (r *Registry) DispatchOnUpgrade(ctx context.Context, name string, in Install, fromVersion string) error {
	app, ok := r.Get(name)
	if !ok {
		return fmt.Errorf("biuapp: unknown app %q", name)
	}
	if h, ok := app.(LifecycleHooks); ok {
		return h.OnUpgrade(ctx, in, fromVersion)
	}
	return nil
}

// DispatchOnConfigUpdate calls app.OnConfigUpdate if implemented.
// in.Config carries the freshly-merged configuration.
func (r *Registry) DispatchOnConfigUpdate(ctx context.Context, name string, in Install) error {
	app, ok := r.Get(name)
	if !ok {
		return fmt.Errorf("biuapp: unknown app %q", name)
	}
	if h, ok := app.(LifecycleHooks); ok {
		return h.OnConfigUpdate(ctx, in)
	}
	return nil
}

// DispatchOnTrigger routes a TriggerEvent to the App. Apps that
// implement TriggerHandler get the typed event; everyone else gets
// the default routing (TriggerEvent → Invoke(action, input)). This
// is the path the scheduler / webhook / inbox dispatcher takes.
func (r *Registry) DispatchOnTrigger(ctx context.Context, name string, ev TriggerEvent) error {
	app, ok := r.Get(name)
	if !ok {
		return fmt.Errorf("biuapp: unknown app %q", name)
	}
	if h, ok := app.(TriggerHandler); ok {
		return h.OnTrigger(ctx, ev)
	}
	// Default routing: dispatch to the action via Invoke. Discard
	// the result — triggers don't return data, only side effects.
	if !hasAction(app.Manifest(), ev.Action) {
		return fmt.Errorf("biuapp: trigger action %q not in actions[]", ev.Action)
	}
	_, err := app.Invoke(ctx, ev.Action, ev.Input)
	return err
}

func hasAction(m Manifest, action string) bool {
	for _, a := range m.Actions {
		if a.Name == action {
			return true
		}
	}
	return false
}

// sortManifests — small inline sort to avoid pulling sort just for this.
func sortManifests(ms []Manifest) {
	for i := 1; i < len(ms); i++ {
		j := i
		for j > 0 && ms[j-1].Name > ms[j].Name {
			ms[j-1], ms[j] = ms[j], ms[j-1]
			j--
		}
	}
}

// ─── Default helpers ──────────────────────────────────────

// DiscardLogger — Logger implementation that drops everything.
type DiscardLogger struct{}

func (DiscardLogger) Debug(string, ...any) {}
func (DiscardLogger) Info(string, ...any)  {}
func (DiscardLogger) Warn(string, ...any)  {}
func (DiscardLogger) Error(string, ...any) {}
