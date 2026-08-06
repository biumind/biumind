// Optional lifecycle / runtime hooks an App may implement.
//
// v1.0 only required `App` (Manifest / Init / Invoke). v1.5 broadens
// the surface: an app that wants to run scheduled work, render
// declarative views, or react to install / upgrade / uninstall events
// implements the matching optional interface. The Registry checks
// each at dispatch time and skips silently if not implemented — no
// new mandatory methods are added to App, so all v1.0 bundled apps
// continue to compile.

package biuapp

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// Sentinel errors apps and the registry can return / wrap. Defined
// here (alongside the optional interfaces) so app authors don't have
// to discover them across files.
var (
	// ErrUnknownAction — the App's Invoke received an action name
	// that doesn't match any of its manifest.actions[]. Apps SHOULD
	// return this for unknown actions rather than a generic error so
	// the runtime can map it to a friendly tool-not-found.
	ErrUnknownAction = errors.New("biuapp: unknown action")

	// ErrSkillNotFound — the App's BundledSkillProvider was asked for
	// a skill identifier that isn't bundled. Returned by App
	// implementations; consumed by skillbridge.
	ErrSkillNotFound = errors.New("biuapp: bundled skill not found")
)

// Install is the runtime context handed to lifecycle hooks. It carries
// just enough to identify the tenant and look up config without
// pulling the entire app_center.installations row in.
type Install struct {
	ID         string         // installation id (uuid as string)
	Identifier string         // app slug
	Version    string         // installed version
	Scope      string         // "org" | "user"
	ScopeID    string         // org_id or user_id
	Config     map[string]any // installation config (no secrets — those go via Vault)
	Deps       Deps           // same Deps as Init, but install-scoped
}

// LifecycleHooks — implement any subset to react to install events.
// All four methods are optional; the registry detects implementations
// via type assertion and only calls those that are present.
type LifecycleHooks interface {
	OnInstall(ctx context.Context, in Install) error
	OnUninstall(ctx context.Context, in Install) error
	OnUpgrade(ctx context.Context, in Install, fromVersion string) error
	OnConfigUpdate(ctx context.Context, in Install) error
}

// TriggerEvent describes a fired trigger. Action is the manifest
// action name to call; Input is the merged static-from-manifest +
// dynamic-from-trigger payload.
type TriggerEvent struct {
	TriggerKind TriggerKind     // cron | webhook | inbox
	Name        string          // manifest trigger name
	Action      string          // manifest action to invoke
	Input       json.RawMessage // payload for the action
	FiredAt     time.Time
	Install     Install
}

// TriggerHandler is implemented by Apps that declare manifest.triggers.
// The registry routes a fired trigger to OnTrigger; the App typically
// dispatches to the corresponding action method internally.
//
// Apps that only declare cron triggers AND whose handlers are pure
// action invocations may rely on the registry's default routing
// (TriggerEvent → Invoke(action, input)) and skip implementing this
// interface — the registry calls Invoke directly when no
// TriggerHandler is found. Implement this only when the trigger needs
// pre/post processing distinct from the action itself.
type TriggerHandler interface {
	OnTrigger(ctx context.Context, ev TriggerEvent) error
}

// ViewDataRequest is what the platform passes when a client opens a
// view declared in manifest.views. The App returns the data the view
// will render against (typically by dispatching to the
// view.data_source.action under the hood).
type ViewDataRequest struct {
	Install     Install
	ViewID      string
	RouteParams map[string]string
	QueryParams map[string]string
	UserLocale  string
}

// ViewDataProvider is implemented by Apps that need view data fetched
// out-of-band from action invocation (e.g. layout=custom returning an
// A2UI subtree, or expensive joins that shouldn't go through the
// generic action path). For most layouts the default is "call the
// view's data_source.action via Invoke" — implement this only to
// override.
type ViewDataProvider interface {
	OnViewData(ctx context.Context, req ViewDataRequest) (any, error)
}

// EventEmitter is passed to streaming actions so they can push
// progress, partial results, or log lines while still running. The
// registry forwards each emit over Realtime/SSE to the client.
type EventEmitter func(StreamEvent) error

// StreamEvent is the shape pushed to the client for streamable
// actions. Implementations should never push more than 200ms apart;
// the client batches into a single notification.
type StreamEvent struct {
	Kind    StreamEventKind `json:"kind"`
	Message string          `json:"message,omitempty"`
	Data    any             `json:"data,omitempty"`
}

type StreamEventKind string

const (
	StreamLog      StreamEventKind = "log"
	StreamProgress StreamEventKind = "progress"
	StreamPartial  StreamEventKind = "partial"
	StreamFinal    StreamEventKind = "final"
)

// StreamingApp is implemented by Apps that declare actions with
// `streamable: true`. The registry dispatches such calls to Stream
// instead of Invoke; the App emits via the supplied EventEmitter and
// returns when finished. Non-streamable actions still go through
// Invoke even on a StreamingApp.
type StreamingApp interface {
	Stream(ctx context.Context, action string, in json.RawMessage, emit EventEmitter) error
}

// BundledSkillProvider is implemented by Apps that ship attached
// SKILL.md content in their manifest.skills. When App Center
// installs such an App, it calls SkillContent(identifier) for each
// declared skill and writes the bytes into runtime.skills with
// source='bundled' and manifest.app_install_id pointing back at
// the installation row.
//
// Bundled apps typically embed the file at compile time:
//
//	//go:embed skills/summarize.md
//	var summarizeSkill []byte
//
//	func (a *App) SkillContent(id string) ([]byte, error) {
//	    if id == "rss-summarize" { return summarizeSkill, nil }
//	    return nil, biuapp.ErrSkillNotFound
//	}
//
// Apps that don't ship skills (the majority) skip implementing this
// interface; the install path silently moves on.
type BundledSkillProvider interface {
	SkillContent(identifier string) ([]byte, error)
}
