// Package tools defines the tool registry shared by Chat agent loops
// (cloud-side in Brain, client-side in Flutter via JSON catalog).
//
// Design doc: docs/BiuMind-Chat-Optimization-Design.md §4.6
//
// The registry is metadata-only at this stage. The agent loop that
// actually dispatches tools (W6/W7) consumes this registry to know:
//   - which tools exist
//   - which can run on the cloud (Brain) vs the client
//   - which to advertise to the LLM for a given thread.execution_mode
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
)

// ExecutionMode mirrors chat.threads.execution_mode.
type ExecutionMode string

const (
	ExecutionCloud  ExecutionMode = "cloud"
	ExecutionClient ExecutionMode = "client"
)

func ValidExecutionMode(s string) bool {
	switch ExecutionMode(s) {
	case ExecutionCloud, ExecutionClient:
		return true
	}
	return false
}

// Runtime is a bitmask of locations a tool can execute. A tool that
// can run anywhere uses RuntimeBoth; one that depends on Brain-only
// state (DB pool, vector index) uses RuntimeCloud; one that needs the
// user's machine or VPN reach uses RuntimeClient.
type Runtime uint8

const (
	RuntimeCloud  Runtime = 1 << 0
	RuntimeClient Runtime = 1 << 1
	RuntimeBoth           = RuntimeCloud | RuntimeClient
)

func (r Runtime) Has(other Runtime) bool { return r&other == other }

func (r Runtime) String() string {
	switch r {
	case RuntimeCloud:
		return "cloud"
	case RuntimeClient:
		return "client"
	case RuntimeBoth:
		return "both"
	}
	return fmt.Sprintf("runtime(%d)", r)
}

// MarshalJSON emits "cloud" / "client" / "both" so client catalogs
// stay readable.
func (r Runtime) MarshalJSON() ([]byte, error) {
	return json.Marshal(r.String())
}

func (r *Runtime) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	switch s {
	case "cloud":
		*r = RuntimeCloud
	case "client":
		*r = RuntimeClient
	case "both":
		*r = RuntimeBoth
	default:
		return fmt.Errorf("tools: unknown runtime %q", s)
	}
	return nil
}

// AvailableIn reports whether a tool with this runtime mask can be
// dispatched in the given execution mode.
func (r Runtime) AvailableIn(m ExecutionMode) bool {
	switch m {
	case ExecutionCloud:
		return r.Has(RuntimeCloud)
	case ExecutionClient:
		return r.Has(RuntimeClient)
	}
	return false
}

// Descriptor is the static metadata for one tool: how to advertise it
// to the LLM, and where it may run. Descriptor is intentionally
// JSON-serializable so it can be shipped to the client as a catalog
// without leaking the runtime implementation.
type Descriptor struct {
	// Name as advertised to the LLM (must be unique inside a registry).
	Name string `json:"name"`

	// Human-readable description; goes into the LLM tool prompt.
	Description string `json:"description"`

	// Source describes where the tool comes from. Useful for UI
	// grouping ("builtin" / "skill:foo" / "mcp:user-server").
	Source string `json:"source"`

	// JSON Schema for tool input. Stored as raw JSON so we can pass
	// it through to the LLM without re-marshaling.
	InputSchema json.RawMessage `json:"input_schema,omitempty"`

	// Runtime mask: where this tool can execute.
	Runtime Runtime `json:"runtime"`
}

// Invoker executes a tool. `input` is the raw JSON the LLM emitted as
// the tool_use input; the implementation is expected to validate it.
// The return value is JSON-encoded by the caller and fed back to the
// LLM as a tool_result.
type Invoker func(ctx context.Context, input json.RawMessage) (any, error)

// Tool wraps a Descriptor with its execution function. Cloud-runtime
// tools must supply Invoke; client-runtime tools may leave it nil
// (they're descriptor-only on the server, the actual invoker lives
// on the client).
//
// ReadOnly (S3 P0-1) classifies the tool's side-effect profile for the
// biumindkit adapter (see biumindkit_adapter.go): true = read-only /
// concurrency-safe (biumindkit skips permission confirmation); false =
// mutating write (biumindkit treats as needing confirmation — though
// RunV2 currently BypassPermissions, write tools still rely on the
// store-layer create-only hard gate + page_revisions rollback for
// safety, NOT on biumindkit permission). Existing chat tools set this
// true; wiki write tools (create/update/merge_page) set it false.
// Pure server-runtime attribute — intentionally NOT in Descriptor, so
// it never leaks into the client tool catalog.
type Tool struct {
	Descriptor
	Invoke   Invoker
	ReadOnly bool
}

// Registry is a goroutine-safe, in-memory tool catalog. New() returns
// an empty one; package callers register their tools at startup.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

func New() *Registry {
	return &Registry{tools: map[string]Tool{}}
}

var (
	ErrDuplicate    = errors.New("tools: duplicate registration")
	ErrUnknownTool  = errors.New("tools: unknown tool")
	ErrEmptyName    = errors.New("tools: empty tool name")
	ErrInvalidRT    = errors.New("tools: invalid runtime")
	ErrNotInvocable = errors.New("tools: tool has no invoker (descriptor-only)")
)

// Register adds a Tool. Returns ErrDuplicate if Name is taken.
func (r *Registry) Register(t Tool) error {
	if t.Name == "" {
		return ErrEmptyName
	}
	if t.Runtime == 0 {
		return ErrInvalidRT
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.tools[t.Name]; ok {
		return fmt.Errorf("%w: %s", ErrDuplicate, t.Name)
	}
	r.tools[t.Name] = t
	return nil
}

// RegisterDescriptor is a convenience for client-runtime tools where
// the server only knows the metadata (Invoke is nil).
func (r *Registry) RegisterDescriptor(d Descriptor) error {
	return r.Register(Tool{Descriptor: d})
}

// MustRegister panics on duplicate; for use in init() blocks where
// registration failure is a programmer error.
func (r *Registry) MustRegister(t Tool) {
	if err := r.Register(t); err != nil {
		panic(err)
	}
}

// Get returns the descriptor by name, or false if absent.
func (r *Registry) Get(name string) (Descriptor, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	if !ok {
		return Descriptor{}, false
	}
	return t.Descriptor, true
}

// All returns a stable-ordered snapshot of every tool descriptor.
func (r *Registry) All() []Descriptor {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Descriptor, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, t.Descriptor)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Available returns the tools dispatchable under the given execution
// mode, sorted by name. Used by the agent loop to pick the LLM-facing
// tool list, and by the API to render UI catalogs.
func (r *Registry) Available(mode ExecutionMode) []Descriptor {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Descriptor, 0, len(r.tools))
	for _, t := range r.tools {
		if t.Runtime.AvailableIn(mode) {
			out = append(out, t.Descriptor)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// AvailableNames is a convenience wrapper returning just the names.
func (r *Registry) AvailableNames(mode ExecutionMode) []string {
	ds := r.Available(mode)
	out := make([]string, len(ds))
	for i, d := range ds {
		out[i] = d.Name
	}
	return out
}

// Invoke runs the named tool with the given JSON input. Returns
// ErrUnknownTool if the name is not registered, or ErrNotInvocable
// if the tool is descriptor-only (client-runtime tool registered on
// the server for catalog purposes). Returns ErrInvalidRT if the tool
// is not allowed under the requested execution mode.
func (r *Registry) Invoke(ctx context.Context, mode ExecutionMode,
	name string, input json.RawMessage,
) (any, error) {
	r.mu.RLock()
	t, ok := r.tools[name]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownTool, name)
	}
	if !t.Runtime.AvailableIn(mode) {
		return nil, fmt.Errorf("%w: %s not available in %s",
			ErrInvalidRT, name, mode)
	}
	if t.Invoke == nil {
		return nil, fmt.Errorf("%w: %s", ErrNotInvocable, name)
	}
	return t.Invoke(ctx, input)
}
