// Registry connects multiple MCP servers and surfaces their tools
// under namespaced names (`mcp__<server>__<tool>`).
//
// The unified naming scheme is critical for two reasons:
//
//   1. Tools in the LLM-visible registry must have unique names —
//      two MCP servers can both ship a "search" tool, namespacing
//      keeps them distinct.
//   2. The LLM can decide which server's tool to call by name alone,
//      no extra metadata routing.

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// Registry owns a set of connected MCP servers and produces wrapped
// tool descriptors that route execution back through the right
// connection. Clients are stored behind the transport-agnostic
// Client interface so a registry that holds a mix of stdio and
// HTTP servers behaves identically — adding a transport is one new
// file (sse.go / http.go), not a registry-wide rewrite.
type Registry struct {
	mu      sync.RWMutex
	clients map[string]Client // by server name
	tools   map[string]*RegisteredTool

	// deferTools maps server name → "every tool from this server is
	// deferred" (P20.51 Phase 2). engineAdapter.ShouldDefer() consults
	// this map at engine-side catalog-build time, so flipping the
	// flag at runtime (currently only at connect time, but the future
	// `/mcp defer <name>` slash would need this) takes effect on the
	// next provider request without touching individual tool entries.
	deferTools map[string]bool
}

// RegisteredTool holds the catalog entry plus the server it belongs
// to. The bridging layer (cmd/biu/main.go) wraps these into the
// existing tools.Tool struct.
type RegisteredTool struct {
	QualifiedName string  // "mcp__github__create_pr"
	Server        string  // "github"
	OriginalName  string  // "create_pr"
	Def           ToolDef // from the server's tools/list
}

func NewRegistry() *Registry {
	return &Registry{
		clients:    map[string]Client{},
		tools:      map[string]*RegisteredTool{},
		deferTools: map[string]bool{},
	}
}

// SetDeferTools toggles the deferred-tools flag for a server.
// Idempotent. Called by Bootstrap when an entry has DeferTools=true.
func (r *Registry) SetDeferTools(server string, defer_ bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.deferTools == nil {
		r.deferTools = map[string]bool{}
	}
	if defer_ {
		r.deferTools[server] = true
	} else {
		delete(r.deferTools, server)
	}
}

// IsServerDeferred reports whether the named server's tools should be
// deferred. False when the server is unknown or hasn't opted in.
func (r *Registry) IsServerDeferred(server string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.deferTools[server]
}

// Connect launches a stdio server, performs the handshake, fetches
// its tool catalog, and registers the tools under namespaced names.
//
// On any failure the partial state is rolled back (process killed +
// no tools registered) so a single bad server doesn't poison the
// registry. Backward-compat shim around connectClient — code paths
// that already had a StdioConfig keep working unchanged.
func (r *Registry) Connect(ctx context.Context, cfg StdioConfig) error {
	if cfg.Name == "" {
		return fmt.Errorf("mcp: server name required")
	}
	if !validServerName(cfg.Name) {
		return fmt.Errorf("mcp: invalid server name %q (use [a-z0-9_-])", cfg.Name)
	}
	return r.connectClient(ctx, NewStdio(cfg))
}

// ConnectHTTP connects to a Streamable-HTTP MCP server. Same
// lifecycle as Connect (handshake → tool catalog → register), just
// over HTTP instead of stdio. The HTTPConfig field on this method
// is the same shape as StdioConfig but with URL+Headers in place of
// Command+Args.
func (r *Registry) ConnectHTTP(ctx context.Context, cfg HTTPConfig) error {
	if cfg.Name == "" {
		return fmt.Errorf("mcp: server name required")
	}
	if !validServerName(cfg.Name) {
		return fmt.Errorf("mcp: invalid server name %q (use [a-z0-9_-])", cfg.Name)
	}
	if cfg.URL == "" {
		return fmt.Errorf("mcp: http transport requires URL")
	}
	return r.connectClient(ctx, NewHTTP(cfg))
}

// connectClient is the transport-agnostic core of the connection
// flow: Start → Initialize → ListTools → register. Both Connect
// (stdio) and ConnectHTTP funnel through this so a future transport
// (websocket, in-process) plugs in by adding a `ConnectXxx` shim
// without duplicating the lifecycle.
func (r *Registry) connectClient(ctx context.Context, c Client) error {
	name := c.Name()
	r.mu.RLock()
	_, exists := r.clients[name]
	r.mu.RUnlock()
	if exists {
		_ = c.Close()
		return fmt.Errorf("mcp: server %q already connected", name)
	}

	if err := c.Start(ctx); err != nil {
		return err
	}
	if _, err := c.Initialize(ctx); err != nil {
		// needs-auth (P20.49): record the client without tools so
		// the engine_adapter surfaces the authenticate pseudo-tool.
		// Anything else is a hard handshake failure.
		if errors.Is(err, ErrNeedsAuth) {
			r.mu.Lock()
			r.clients[name] = c
			r.mu.Unlock()
			return nil
		}
		_ = c.Close()
		return err
	}
	tools, err := c.ListTools(ctx)
	if err != nil {
		// Same dance for tools/list — some servers do auth at the
		// tools/list endpoint instead of (or in addition to)
		// initialize.
		if errors.Is(err, ErrNeedsAuth) {
			r.mu.Lock()
			r.clients[name] = c
			r.mu.Unlock()
			return nil
		}
		_ = c.Close()
		return err
	}

	r.mu.Lock()
	r.clients[name] = c
	for _, t := range tools {
		// Normalise tool names so `file.read` from upstream doesn't
		// produce `mcp__server__file.read` — the LLM tool-spec only
		// allows [a-zA-Z0-9_-]. The OriginalName stays unmangled so
		// CallTool reaches the upstream tool by its real id.
		safe := NormalizeToolName(t.Name)
		q := QualifyName(name, safe)
		r.tools[q] = &RegisteredTool{
			QualifiedName: q,
			Server:        name,
			OriginalName:  t.Name,
			Def:           t,
		}
	}
	r.mu.Unlock()
	return nil
}

// replaceServerTools swaps the registered tool set for `server`
// with `newTools`. Used by HealthMonitor after a successful
// reconnect so the engine catalog stays in sync with what the
// server actually exposes (server upgrades may add or remove
// tools across a restart).
//
// The qualified-name keying matches Connect's normalisation —
// every entry is added under `mcp__<server>__<safe>`. Tools that
// were previously registered under this server but are absent
// from `newTools` are removed.
//
// engine.SimpleRegistry doesn't have a "remove" affordance today
// (Register is last-write-wins, no Unregister). The downstream
// effect: a tool that vanished from the server's catalog still
// shows up in the engine's tools list until biu restarts. The
// model would see a tool spec but calling it routes through
// Registry.Call which now returns "unknown tool". Acceptable
// degraded mode — the cleaner fix lives in the engine package
// (a future Unregister API).
func (r *Registry) replaceServerTools(server string, newTools []ToolDef) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Remove old tools belonging to this server. Iterate over
	// a snapshot of keys because we mutate the map mid-loop.
	for q, t := range r.tools {
		if t.Server == server {
			delete(r.tools, q)
		}
	}
	// Re-register from the fresh catalog using the same
	// normalisation Connect uses.
	for _, t := range newTools {
		safe := NormalizeToolName(t.Name)
		q := QualifyName(server, safe)
		r.tools[q] = &RegisteredTool{
			QualifiedName: q,
			Server:        server,
			OriginalName:  t.Name,
			Def:           t,
		}
	}
}

// All returns every registered tool. Caller iterates and wraps into
// tools.Tool.
func (r *Registry) All() []*RegisteredTool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*RegisteredTool, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, t)
	}
	return out
}

// ServerStatus is a stable, JSON-serialisable snapshot of one
// connected MCP server — what the `/mcp` slash and any future
// telemetry surface render. Distinct from the wire-protocol
// ServerInfo type (defined in protocol.go), which describes the
// server's *self-advertised* identity in the MCP handshake; this
// type is biu's view of what's actually wired up.
type ServerStatus struct {
	Name      string
	Transport Transport // "stdio" | "http"
	Command   string    // stdio: executable path. http: endpoint URL.
	Args      []string  // stdio: argv tail. http: nil.
	ToolCount int
	Tools     []ServerToolStatus
}

// ServerToolStatus is one tool entry inside a ServerStatus. The
// QualifiedName matches what the engine catalog calls the tool;
// OriginalName is the upstream MCP tool ID (useful when upstream
// uses dots / slashes that biu's normaliser strips).
type ServerToolStatus struct {
	QualifiedName string
	OriginalName  string
	Description   string
}

// Servers returns one ServerStatus per connected server, sorted by
// name. Cheap — no I/O, just a snapshot of in-memory state.
//
// Used by the REPL `/mcp` slash to render a status table without
// reaching into private fields of the Registry. A future health-
// check ping (`tools/list` round-trip) would extend ServerStatus
// with a Reachable bool.
func (r *Registry) Servers() []ServerStatus {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]ServerStatus, 0, len(r.clients))
	for name, c := range r.clients {
		spec := c.Spec()
		info := ServerStatus{
			Name:      name,
			Transport: spec.Transport,
			Command:   spec.Command,
			Args:      spec.Args,
		}
		for _, t := range r.tools {
			if t.Server != name {
				continue
			}
			info.Tools = append(info.Tools, ServerToolStatus{
				QualifiedName: t.QualifiedName,
				OriginalName:  t.OriginalName,
				Description:   t.Def.Description,
			})
		}
		info.ToolCount = len(info.Tools)
		sortToolsByName(info.Tools)
		out = append(out, info)
	}
	sortServerInfos(out)
	return out
}

func sortToolsByName(t []ServerToolStatus) {
	for i := 0; i < len(t); i++ {
		for j := i + 1; j < len(t); j++ {
			if t[i].QualifiedName > t[j].QualifiedName {
				t[i], t[j] = t[j], t[i]
			}
		}
	}
}

func sortServerInfos(s []ServerStatus) {
	for i := 0; i < len(s); i++ {
		for j := i + 1; j < len(s); j++ {
			if s[i].Name > s[j].Name {
				s[i], s[j] = s[j], s[i]
			}
		}
	}
}

// Call invokes a tool by qualified name. This is the bridge surface —
// engine.tool layer calls this with the LLM's chosen tool name.
func (r *Registry) Call(
	ctx context.Context, qualifiedName string, args map[string]any,
) (*CallToolResult, error) {
	r.mu.RLock()
	t, ok := r.tools[qualifiedName]
	if !ok {
		r.mu.RUnlock()
		return nil, fmt.Errorf("mcp: unknown tool %q", qualifiedName)
	}
	c, ok := r.clients[t.Server]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("mcp: server %q gone", t.Server)
	}
	return c.CallTool(ctx, t.OriginalName, args)
}

// ResourceEntry is one row in a Registry.ListResources result —
// a Resource flattened to include the server name so the caller
// can disambiguate when multiple servers are connected.
type ResourceEntry struct {
	Server      string
	URI         string
	Name        string
	Description string
	MimeType    string
}

// ListResources walks every connected server (or just `server` when
// non-empty) and returns the union of their resources/list responses.
// One server's failure is logged into the error slice but does not
// abort the others — the caller surfaces partial success to the
// model via the soft-error channel rather than dropping everything.
func (r *Registry) ListResources(ctx context.Context, server string) ([]ResourceEntry, []error) {
	r.mu.RLock()
	clients := make([]Client, 0, len(r.clients))
	for name, c := range r.clients {
		if server != "" && name != server {
			continue
		}
		clients = append(clients, c)
	}
	r.mu.RUnlock()
	if server != "" && len(clients) == 0 {
		return nil, []error{fmt.Errorf("mcp: no connected server named %q", server)}
	}
	var out []ResourceEntry
	var errs []error
	for _, c := range clients {
		list, err := c.ListResources(ctx)
		if err != nil {
			errs = append(errs, fmt.Errorf("mcp[%s]: %w", c.Name(), err))
			continue
		}
		for _, res := range list {
			out = append(out, ResourceEntry{
				Server:      c.Name(),
				URI:         res.URI,
				Name:        res.Name,
				Description: res.Description,
				MimeType:    res.MimeType,
			})
		}
	}
	return out, errs
}

// ReadResource fetches `uri` from `server`. server="" tries every
// connected server in turn and returns the first hit — useful when
// the URI is globally unique (e.g. a UUID).
func (r *Registry) ReadResource(ctx context.Context, server, uri string) (*ReadResourceResult, string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if server != "" {
		c, ok := r.clients[server]
		if !ok {
			return nil, "", fmt.Errorf("mcp: no connected server named %q", server)
		}
		res, err := c.ReadResource(ctx, uri)
		return res, server, err
	}
	// No server pinned — first hit wins. Servers that don't have the
	// URI typically return a JSON-RPC error which we treat as "miss".
	var lastErr error
	for name, c := range r.clients {
		res, err := c.ReadResource(ctx, uri)
		if err == nil {
			return res, name, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("mcp: no servers connected")
	}
	return nil, "", lastErr
}

// PromptEntry is one row in a Registry.ListPrompts result —
// a Prompt flattened with the server name so the caller can
// disambiguate when two servers expose the same prompt name.
type PromptEntry struct {
	Server      string
	Name        string
	Description string
	Arguments   []PromptArgument
}

// ListPrompts walks every connected server (or just `server` when
// non-empty) and returns the union of their prompts/list responses.
// Failures are aggregated into the second return value; the caller
// surfaces them as soft errors rather than aborting on the first
// uncooperative server.
func (r *Registry) ListPrompts(ctx context.Context, server string) ([]PromptEntry, []error) {
	r.mu.RLock()
	clients := make([]Client, 0, len(r.clients))
	for name, c := range r.clients {
		if server != "" && name != server {
			continue
		}
		clients = append(clients, c)
	}
	r.mu.RUnlock()
	if server != "" && len(clients) == 0 {
		return nil, []error{fmt.Errorf("mcp: no connected server named %q", server)}
	}
	var out []PromptEntry
	var errs []error
	for _, c := range clients {
		list, err := c.ListPrompts(ctx)
		if err != nil {
			errs = append(errs, fmt.Errorf("mcp[%s]: %w", c.Name(), err))
			continue
		}
		for _, p := range list {
			out = append(out, PromptEntry{
				Server:      c.Name(),
				Name:        p.Name,
				Description: p.Description,
				Arguments:   p.Arguments,
			})
		}
	}
	return out, errs
}

// GetPrompt fetches `name` from `server` with the supplied
// arguments. server="" tries every connected server and returns
// the first successful render — useful when the prompt name is
// globally unique.
func (r *Registry) GetPrompt(ctx context.Context, server, name string, args map[string]string) (*GetPromptResult, string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if server != "" {
		c, ok := r.clients[server]
		if !ok {
			return nil, "", fmt.Errorf("mcp: no connected server named %q", server)
		}
		res, err := c.GetPrompt(ctx, name, args)
		return res, server, err
	}
	var lastErr error
	for srv, c := range r.clients {
		res, err := c.GetPrompt(ctx, name, args)
		if err == nil {
			return res, srv, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("mcp: no servers connected")
	}
	return nil, "", lastErr
}

// BootstrapInput is one logical entry the caller wants connected.
// Pre-expansion: ${VAR} and ${VAR:-def} in command/args/env/url
// values are resolved by Bootstrap.
//
// Two transports are supported. Transport defaults to "stdio" when
// empty so existing TOML configs (Command + Args) keep working
// unchanged. Transport "http" routes to ConnectHTTP and uses URL +
// Headers; Command/Args are ignored. Mixed-transport batches are
// supported — Bootstrap dispatches per-entry.
type BootstrapInput struct {
	Source string // 'manual' | 'plugin' | 'project' — used for dedup priority
	Name   string
	// Transport selects the wire protocol. Empty → stdio (default).
	Transport Transport

	// stdio fields
	Command string
	Args    []string
	Env     map[string]string
	Cwd     string

	// http fields
	URL     string            // Streamable HTTP endpoint, e.g. https://api.example.com/mcp
	Headers map[string]string // Additional request headers (Authorization, etc.)

	// OAuth (P20.49b) is attached when the user wants biu to drive a
	// PKCE flow on 401. Carried opaque from cfg.MCPServers[].OAuth.
	OAuth *OAuthSpec

	// DeferTools (P20.51 Phase 2) marks every tool from this server
	// as deferred — its full JSONSchema is hidden from the system
	// prompt until the model unlocks it via the ToolSearch tool. Use
	// for servers with large tool counts (Slack, GitHub, Notion).
	// Default false: tools land in the catalog upfront.
	DeferTools bool
}

// BootstrapResult records what happened for one entry.
type BootstrapResult struct {
	Name         string
	Skipped      bool     // true → duplicate of a higher-priority entry
	Err          error    // non-nil → connect failed
	Missing      []string // env vars referenced without defaults that weren't set
	TrustBlocked bool     // true → trust gate refused to spawn this entry
}

// TrustGate is the small surface Bootstrap consults before spawning
// project-sourced servers. Returns true when the current cwd is
// allowed to launch shell commands. Entries with Source != "project"
// are NOT gated — they come from `~/.biu/config.toml` which the
// user authored themselves, so trust is implicit.
//
// nil gate = legacy mode (everything spawns regardless of trust).
// Mirrors hooks.TrustGate's design — different package because the
// hook surface returns []Entry whereas this one is a one-shot
// boot-time check.
type TrustGate interface {
	IsTrustedNow() bool
}

// BootstrapOptions threads optional behaviour into Bootstrap without
// breaking the original 3-arg signature. New fields (TrustGate,
// future health-check ping, etc.) live here.
type BootstrapOptions struct {
	// StderrSink, when non-nil, receives each server's stderr lines
	// prefixed with "[<name>] ". Same callback the legacy Bootstrap
	// took as its third positional arg.
	StderrSink func(string)
	// TrustGate filters out project-source entries when the gate
	// reports untrusted. Nil = no gating.
	TrustGate TrustGate
	// SkipNotifier fires once per blocked entry so the wiring
	// layer can surface a stderr breadcrumb. Optional.
	SkipNotifier func(name string)
}

// sourceRank gives manual the highest priority; plugin/project fall
// behind. Two entries with the same signature: the lower-rank one
// wins.
func sourceRank(s string) int {
	switch s {
	case "manual", "":
		return 0
	case "project":
		return 1
	case "plugin":
		return 2
	default:
		return 3
	}
}

// Bootstrap is the legacy 3-arg entry point — stderr-only options.
// New code should call BootstrapWithOptions to opt into trust gating
// + skip notifications. Kept for backward compatibility with
// existing callers.
func (r *Registry) Bootstrap(
	ctx context.Context,
	inputs []BootstrapInput,
	stderrSink func(string),
) []BootstrapResult {
	return r.BootstrapWithOptions(ctx, inputs, BootstrapOptions{
		StderrSink: stderrSink,
	})
}

// BootstrapWithOptions connects every input, deduplicating by command
// signature and expanding env vars. Returns one Result per input
// (even skipped or failed). Does NOT roll back the whole batch on
// partial failure — callers want every healthy server up.
//
// Trust gate (when set in opt.TrustGate) refuses to spawn entries
// whose Source is "project" — assumes user-authored configs are
// trusted by definition (`~/.biu/config.toml`) but project-shipped
// configs (`<cwd>/.biumind/config.toml`) need explicit approval
// from the user via /trust. opt.SkipNotifier fires once per blocked
// entry so the caller can surface a "X servers blocked" stderr
// breadcrumb without losing the count.
//
// stderr from each server is forwarded to opt.StderrSink (one func,
// fed "[<name>] <line>" so callers can grep).
func (r *Registry) BootstrapWithOptions(
	ctx context.Context,
	inputs []BootstrapInput,
	opt BootstrapOptions,
) []BootstrapResult {
	stderrSink := opt.StderrSink
	// Dedup pass.
	type entry struct {
		idx int
		in  BootstrapInput
		sig string
	}
	bySig := map[string]entry{}
	keep := []entry{}
	for i, in := range inputs {
		// Expand command + args + env values for signature stability:
		// two configs that resolve to the same final command should
		// dedup even if one used ${VAR} and the other a literal.
		expandedCmd, _ := ExpandEnvVars(in.Command)
		expandedArgs, _ := ExpandEnvSlice(in.Args)
		sig := SignatureFor(expandedCmd, expandedArgs)
		ent := entry{idx: i, in: in, sig: sig}
		if prev, ok := bySig[sig]; ok {
			if sourceRank(in.Source) < sourceRank(prev.in.Source) {
				bySig[sig] = ent
			}
			continue
		}
		bySig[sig] = ent
	}
	for _, e := range bySig {
		keep = append(keep, e)
	}

	// Connect kept entries.
	out := make([]BootstrapResult, len(inputs))
	for i := range inputs {
		out[i] = BootstrapResult{Name: inputs[i].Name, Skipped: true}
	}
	// Trust gate: precompute once per Bootstrap call so a long
	// bootstrap doesn't keep re-querying the gate for each entry.
	// "project"-source entries are gated; "manual" / "plugin" /
	// other sources are user-authored and pass through.
	trustOK := true
	if opt.TrustGate != nil {
		trustOK = opt.TrustGate.IsTrustedNow()
	}
	for _, e := range keep {
		// Mark this index as not skipped.
		out[e.idx] = BootstrapResult{Name: e.in.Name}

		if !trustOK && e.in.Source == "project" {
			out[e.idx].TrustBlocked = true
			if opt.SkipNotifier != nil {
				opt.SkipNotifier(e.in.Name)
			}
			continue
		}

		// P20.51 Phase 2: pre-record the deferred-tools flag so any
		// tools that arrive (even before the connect call returns)
		// inherit it on the engine side. SetDeferTools is idempotent.
		if e.in.DeferTools {
			r.SetDeferTools(NormalizeServerName(e.in.Name), true)
		}

		// Per-transport dispatch. URL / Headers expansion only
		// matters for http; command / args / env only for stdio.
		// Each branch fills its own Missing list and forwards to
		// the matching connect method.
		switch e.in.Transport {
		case TransportHTTP:
			expURL, m1 := ExpandEnvVars(e.in.URL)
			expHeaders, m2 := ExpandEnvMap(e.in.Headers)
			out[e.idx].Missing = append(out[e.idx].Missing, m1...)
			out[e.idx].Missing = append(out[e.idx].Missing, m2...)
			err := r.ConnectHTTP(ctx, HTTPConfig{
				Name:    NormalizeServerName(e.in.Name),
				URL:     expURL,
				Headers: expHeaders,
				OAuth:   e.in.OAuth,
			})
			if err != nil {
				out[e.idx].Err = err
			}
		default:
			// stdio (empty Transport falls through here so legacy
			// TOML configs without a transport field keep working).
			expCmd, m1 := ExpandEnvVars(e.in.Command)
			expArgs, m2 := ExpandEnvSlice(e.in.Args)
			expEnv, m3 := ExpandEnvMap(e.in.Env)
			out[e.idx].Missing = append(out[e.idx].Missing, m1...)
			out[e.idx].Missing = append(out[e.idx].Missing, m2...)
			out[e.idx].Missing = append(out[e.idx].Missing, m3...)
			err := r.Connect(ctx, StdioConfig{
				Name:    NormalizeServerName(e.in.Name),
				Command: expCmd,
				Args:    expArgs,
				Env:     expEnv,
				Cwd:     e.in.Cwd,
				StderrSink: func(line string) {
					if stderrSink != nil {
						stderrSink("[" + e.in.Name + "] " + line)
					}
				},
			})
			if err != nil {
				out[e.idx].Err = err
			}
		}
	}
	return out
}

// Close terminates every connected server. Errors are joined.
func (r *Registry) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	var errs []string
	for name, c := range r.clients {
		if err := c.Close(); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", name, err))
		}
	}
	r.clients = map[string]Client{}
	r.tools = map[string]*RegisteredTool{}
	if len(errs) > 0 {
		return fmt.Errorf("mcp: shutdown errors: %s", strings.Join(errs, "; "))
	}
	return nil
}

// QualifyName builds the registry key. Public so the bridging layer
// can reverse the mapping if needed.
func QualifyName(server, tool string) string {
	return "mcp__" + server + "__" + tool
}

// SplitName is the inverse of QualifyName. Returns ("", "", false)
// when the name doesn't follow the convention (i.e. it's a builtin
// tool, not an MCP one).
func SplitName(qualified string) (server, tool string, ok bool) {
	const prefix = "mcp__"
	if !strings.HasPrefix(qualified, prefix) {
		return "", "", false
	}
	rest := qualified[len(prefix):]
	idx := strings.Index(rest, "__")
	if idx < 0 {
		return "", "", false
	}
	return rest[:idx], rest[idx+2:], true
}

// FlattenSchema makes sure inputSchema is non-nil + has the required
// {type:"object"} root the LLM tool-spec expects. Some MCP servers
// return null when a tool takes no args; we coerce to an empty
// object schema.
func FlattenSchema(s map[string]any) map[string]any {
	if s == nil {
		return map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		}
	}
	if _, hasType := s["type"]; !hasType {
		s["type"] = "object"
	}
	if _, hasProps := s["properties"]; !hasProps {
		s["properties"] = map[string]any{}
	}
	return s
}

func validServerName(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '_' || r == '-':
		default:
			return false
		}
	}
	return true
}

// raw is here just to make sure encoding/json is used (it's used in
// stdio.go but the linter scans per-file).
var _ = json.RawMessage{}
