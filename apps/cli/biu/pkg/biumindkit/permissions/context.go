// ToolPermissionContext aggregates rules from every source plus the
// active mode + ephemeral session grants.
//
// Shape:
//
//   * AllowRules / DenyRules / AskRules — keyed by Source so callers
//     can show "blocked by user settings" vs "blocked by --deny CLI flag".
//   * Mode — currently active permission mode.
//   * SessionGrants — ephemeral "always-allow during this session"
//     decisions made by the user via the dialog ('s' key).
//
// Concurrency: the context is mutated by the REPL (when the user
// presses 's' to remember a grant) and read by the runner. Use the
// embedded sync.RWMutex.

package permissions

import (
	"sort"
	"strings"
	"sync"
)

// AdditionalDirectory captures one extra working directory beyond
// originalCwd along with where the entry came from. The Source label is
// surfaced by /permissions and lets callers reason about persistence
// (entries whose Source is SrcSession / SrcCLIArg evaporate at exit;
// SrcLocalSettings / SrcUserSettings / SrcProjectSettings persist on
// disk).
type AdditionalDirectory struct {
	Path   string
	Source Source
}

// Context is the merged, runtime-resolved permission state for one
// session. Construct via NewContext + LoadFrom*.
type Context struct {
	mu sync.RWMutex

	mode Mode

	// prePlanMode remembers whatever mode was active before
	// EnterPlanMode so ExitPlanMode can restore it instead of
	// hard-coding default. Empty string when the session has never
	// entered plan mode.
	prePlanMode Mode

	// Per-source rule sets. Keyed by Source so we can render which
	// layer produced a decision. Values are the raw rule strings as
	// they appeared in settings.json (parsed lazily).
	allow map[Source][]string
	deny  map[Source][]string
	ask   map[Source][]string

	// SessionGrants memoizes "always-allow this exact (tool, key)" for
	// the lifetime of the Context. Key is built by SessionGrantKey.
	grants map[string]bool

	// allowedPrompts captures semantic batch-approvals from
	// ExitPlanMode. A request is allowed if its tool name + args
	// satisfy any entry's classifier match (see classifier.go).
	// Cleared on /clear and on plan-attachment clear; survives
	// compact (the user's approval should outlast a summary).
	//
	// Uses a deterministic in-house matcher instead of an external
	// LLM classifier.
	allowedPrompts []AllowedPrompt

	// planAttachment carries the most recent plan body the model
	// signed off via ExitPlanMode. Non-empty value tells the
	// compactor to re-inject the plan as a system attachment so the
	// model retains its commitments across compact / clear-context
	// boundaries.
	//
	// Cleared on /clear; preserved across compact (the whole point).
	planAttachment string

	// planObserver, when non-nil, is invoked from SetPlanAttachment
	// so external listeners (planverify.Verifier) can stay in sync
	// without us having to import them.
	planObserver func(plan string)

	// additionalDirs holds extra working directories beyond
	// originalCwd. Keyed by absolute path.
	//
	// Sources matter for persistence and UI: SrcSession / SrcCLIArg
	// disappear at process exit; SrcLocalSettings / SrcUserSettings /
	// SrcProjectSettings round-trip through settings.json on disk.
	//
	// Callers feed this set into the sandbox allow-write list, the
	// file-tool working-dir gate, and the system prompt assembly so
	// the model knows which paths it may touch. See update.go and
	// filesystem.go for those consumers.
	additionalDirs map[string]AdditionalDirectory

	// dirObservers fire whenever AddDirectories / RemoveDirectories /
	// ReplaceDirectories mutates the set. Used by BashTool (so the
	// sandbox allowWrite list re-builds without restart) and by the
	// engine (so the next system prompt rebuild picks up the new
	// listing). Slice not map — observers are append-only and rare.
	dirObservers []func()

	// originalCwd is the working directory the engine was launched
	// in, snapshotted once at startup so file-tool gating compares
	// against a stable anchor (a Bash command may `cd` mid-session
	// but the model's "primary" working dir does not change).
	// Wiring sets this via SetOriginalCwd; Decide consults it
	// through allWorkingDirsForCheck.
	originalCwd string
}

// NewContext returns an empty context in default mode.
func NewContext() *Context {
	return &Context{
		mode:           ModeDefault,
		allow:          map[Source][]string{},
		deny:           map[Source][]string{},
		ask:            map[Source][]string{},
		grants:         map[string]bool{},
		additionalDirs: map[string]AdditionalDirectory{},
	}
}

// Mode returns the active permission mode (read-locked).
func (c *Context) Mode() Mode {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.mode
}

// SetMode swaps the active mode. Used by /mode slash and CLI flags.
func (c *Context) SetMode(m Mode) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.mode = m
}

// EnterPlanMode flips into plan mode and remembers the pre-existing
// mode so ExitPlanMode can restore it. No-op when already in plan
// (prePlanMode is preserved across nested calls).
//
// Returns the previous mode so callers (the slash handler / tool)
// can show a useful "switched from X" message.
func (c *Context) EnterPlanMode() Mode {
	c.mu.Lock()
	defer c.mu.Unlock()
	prev := c.mode
	if c.mode != ModePlan {
		c.prePlanMode = c.mode
	}
	c.mode = ModePlan
	return prev
}

// ExitPlanMode restores the mode that was active before EnterPlanMode
// was called. If the session never entered plan mode (or prePlanMode
// is unset for any reason) it falls back to ModeDefault. Returns the
// new active mode for diagnostics.
func (c *Context) ExitPlanMode() Mode {
	c.mu.Lock()
	defer c.mu.Unlock()
	restore := c.prePlanMode
	if restore == "" {
		restore = ModeDefault
	}
	c.mode = restore
	c.prePlanMode = ""
	return restore
}

// PrePlanMode reports the mode saved when EnterPlanMode last ran.
// Empty string when the session has never been in plan mode.
func (c *Context) PrePlanMode() Mode {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.prePlanMode
}

// SetPlanAttachment stores the plan body that should follow the
// session through compact runs. Pass empty to clear (e.g. on /clear).
//
// Engines that wire a plan-drift verifier should call its SetPlan
// alongside this so the two stay in sync. We don't import
// internal/planverify here to avoid a cycle.
func (c *Context) SetPlanAttachment(plan string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.planAttachment = plan
	if c.planObserver != nil {
		c.planObserver(plan)
	}
}

// SetPlanObserver registers a callback fired whenever the plan
// attachment changes. The engine wires this to the planverify
// Verifier so SetPlan fires automatically without an explicit
// import in this package.
func (c *Context) SetPlanObserver(fn func(plan string)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.planObserver = fn
}

// PlanAttachment returns the saved plan body. Empty when nothing is
// pending. The compactor's Attachments closure reads this at compact
// time so the value is always fresh.
func (c *Context) PlanAttachment() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.planAttachment
}

// AddRules merges a set of rule strings into the given source bucket.
// Behaviour controls which bucket; passing duplicates is harmless.
func (c *Context) AddRules(src Source, behavior Behavior, rules []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	target := c.bucket(behavior)
	target[src] = append(target[src], rules...)
}

// ReplaceRules replaces all rules of (src, behavior) with the new set.
func (c *Context) ReplaceRules(src Source, behavior Behavior, rules []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	target := c.bucket(behavior)
	target[src] = append([]string(nil), rules...)
}

// AllRules returns a flat list of every rule of the requested behavior
// across every source, in deterministic source-then-insertion order.
func (c *Context) AllRules(behavior Behavior) []Rule {
	c.mu.RLock()
	defer c.mu.RUnlock()
	bucket := c.bucket(behavior)
	sources := make([]Source, 0, len(bucket))
	for s := range bucket {
		sources = append(sources, s)
	}
	sort.Slice(sources, func(i, j int) bool {
		return sourceOrder(sources[i]) < sourceOrder(sources[j])
	})
	out := make([]Rule, 0, 16)
	for _, s := range sources {
		for _, raw := range bucket[s] {
			out = append(out, Rule{
				Source: s, Behavior: behavior,
				Value: ParseRuleString(raw),
			})
		}
	}
	return out
}

// Grant remembers an "always-allow" for the given memoization key.
// Persists only for the lifetime of the Context (i.e. the session).
func (c *Context) Grant(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.grants[key] = true
}

// HasGrant reports whether key was previously Grant()ed.
func (c *Context) HasGrant(key string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.grants[key]
}

// AllowedPrompt is one (tool, prompt-description) pre-approval
// staged at ExitPlanMode time. A real tool call is auto-allowed
// when its tool name matches and the classifier accepts the args.
type AllowedPrompt struct {
	Tool   string
	Prompt string
}

// AddAllowedPrompts appends batch approvals to the session. Idempotent
// per (tool, prompt) pair so re-running ExitPlanMode with the same
// list doesn't duplicate entries.
func (c *Context) AddAllowedPrompts(prompts []AllowedPrompt) {
	if len(prompts) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, p := range prompts {
		if p.Tool == "" || p.Prompt == "" {
			continue
		}
		dup := false
		for _, existing := range c.allowedPrompts {
			if existing.Tool == p.Tool && existing.Prompt == p.Prompt {
				dup = true
				break
			}
		}
		if !dup {
			c.allowedPrompts = append(c.allowedPrompts, p)
		}
	}
}

// AllowedPrompts returns a snapshot of staged batch approvals.
func (c *Context) AllowedPrompts() []AllowedPrompt {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]AllowedPrompt(nil), c.allowedPrompts...)
}

// ClearAllowedPrompts wipes batch approvals — used by /clear and
// when the user explicitly revokes a plan.
func (c *Context) ClearAllowedPrompts() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.allowedPrompts = nil
}

// MatchAllowedPrompt looks for a staged approval whose tool name
// matches and whose prompt classifier accepts the request args.
// Returns the matched prompt (for diagnostics) and ok=true on hit.
//
// Matching surface today: Bash `command` arg only. Other tools fall
// back to per-call ask/confirm — extending this requires a per-tool
// arg-extractor, which we leave behind a TODO until a real use case
// surfaces.
func (c *Context) MatchAllowedPrompt(tool string, args map[string]any) (AllowedPrompt, bool) {
	c.mu.RLock()
	prompts := c.allowedPrompts
	c.mu.RUnlock()
	if len(prompts) == 0 {
		return AllowedPrompt{}, false
	}
	cmd, _ := args["command"].(string)
	if cmd == "" {
		// Only Bash-shaped requests carry "command"; all others get
		// no semantic match. Keep the function honest about that.
		return AllowedPrompt{}, false
	}
	for _, p := range prompts {
		if !strings.EqualFold(p.Tool, tool) {
			continue
		}
		if matchPromptToCommand(p.Prompt, cmd) {
			return p, true
		}
	}
	return AllowedPrompt{}, false
}

func (c *Context) bucket(b Behavior) map[Source][]string {
	switch b {
	case BehaviorAllow:
		return c.allow
	case BehaviorDeny:
		return c.deny
	case BehaviorAsk:
		return c.ask
	}
	// Unknown behavior — return a temporary map so caller's writes are
	// silently dropped rather than panicking.
	return map[Source][]string{}
}

// sourceOrder controls precedence when iterating rules. Higher-priority
// sources go first so callers can short-circuit on the most specific
// match. Precedence: cliArg > local > project > user > policy
// (we don't model `policy` yet — comes with MDM in P1).
func sourceOrder(s Source) int {
	switch s {
	case SrcSession:
		return 0
	case SrcCLIArg:
		return 1
	case SrcLocalSettings:
		return 2
	case SrcProjectSettings:
		return 3
	case SrcUserSettings:
		return 4
	case SrcFlagSettings:
		return 5
	case SrcCommand:
		return 6
	}
	return 100
}

// SessionGrantKey builds the (tool, args) memoization key used when
// the user clicks "always allow this". Mirrors the runner's existing
// permissionKey() helper but lives here so the context owns its own
// vocabulary. Conservative — falls back to tool name only when no
// "interesting" arg is present.
func SessionGrantKey(toolName string, input map[string]any) string {
	switch toolName {
	case "Bash", "bash":
		if cmd, _ := input["command"].(string); cmd != "" {
			return toolName + ":" + cmd
		}
	case "Edit", "edit", "Write", "write":
		if p, _ := input["path"].(string); p != "" {
			return toolName + ":" + p
		}
		if p, _ := input["file_path"].(string); p != "" {
			return toolName + ":" + p
		}
	}
	return toolName
}

// ─── Additional working directories ───────────────────
//
// Only paths absent from the set get added —
// duplicate adds preserve the original Source so a path first
// contributed by settings.json is not silently relabelled when the
// user later types `/add-dir <same path>` for this session.

// AddDirectories registers absolute paths as extra working
// directories sourced from src. Empty strings, duplicates, and
// paths that already exist (regardless of source) are skipped. Fires
// every registered dir observer once if at least one path was newly
// added so callers (sandbox, system prompt) can refresh.
func (c *Context) AddDirectories(src Source, paths []string) {
	if len(paths) == 0 {
		return
	}
	c.mu.Lock()
	added := false
	for _, p := range paths {
		if p == "" {
			continue
		}
		if _, ok := c.additionalDirs[p]; ok {
			continue
		}
		c.additionalDirs[p] = AdditionalDirectory{Path: p, Source: src}
		added = true
	}
	observers := c.dirObservers
	c.mu.Unlock()
	if added {
		for _, fn := range observers {
			fn()
		}
	}
}

// RemoveDirectories drops the given paths from the set. Unknown paths
// are ignored. Fires observers if at least one path was actually
// removed.
func (c *Context) RemoveDirectories(paths []string) {
	if len(paths) == 0 {
		return
	}
	c.mu.Lock()
	removed := false
	for _, p := range paths {
		if _, ok := c.additionalDirs[p]; ok {
			delete(c.additionalDirs, p)
			removed = true
		}
	}
	observers := c.dirObservers
	c.mu.Unlock()
	if removed {
		for _, fn := range observers {
			fn()
		}
	}
}

// AdditionalDirectories returns a snapshot of every extra working
// directory currently registered, sorted by path for deterministic
// output. Does NOT include originalCwd — callers that need the full
// "what may I touch" list prepend cwd themselves (see
// AllWorkingDirectories in filesystem.go).
func (c *Context) AdditionalDirectories() []AdditionalDirectory {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if len(c.additionalDirs) == 0 {
		return nil
	}
	out := make([]AdditionalDirectory, 0, len(c.additionalDirs))
	for _, d := range c.additionalDirs {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// AdditionalDirectoryPaths is the path-only flavour for callers that
// just want a slice they can feed to the sandbox / system prompt.
// Sorted, deduplicated.
func (c *Context) AdditionalDirectoryPaths() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if len(c.additionalDirs) == 0 {
		return nil
	}
	out := make([]string, 0, len(c.additionalDirs))
	for p := range c.additionalDirs {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// DirectorySource returns the Source that registered path, or
// ("", false) if the path is not in the set. Used by /permissions to
// label each entry.
func (c *Context) DirectorySource(path string) (Source, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	d, ok := c.additionalDirs[path]
	if !ok {
		return "", false
	}
	return d.Source, true
}

// ClearDirectoriesBySource removes every additional directory whose
// Source equals src. Used by the settings hot-reload path so an
// edited settings.json that drops an entry actually loses the entry
// from the running ctx (without disturbing entries contributed by
// other sources like SrcSession or SrcCLIArg). Fires observers if
// at least one path was actually removed.
func (c *Context) ClearDirectoriesBySource(src Source) {
	c.mu.Lock()
	removed := false
	for path, d := range c.additionalDirs {
		if d.Source == src {
			delete(c.additionalDirs, path)
			removed = true
		}
	}
	observers := c.dirObservers
	c.mu.Unlock()
	if removed {
		for _, fn := range observers {
			fn()
		}
	}
}

// SetOriginalCwd records the engine's launch cwd. Used by the file-
// tool working-dir gate so its containment check has a stable anchor
// even when Bash invocations `cd` mid-session. Empty string means
// "not yet wired" — gating then degrades to allowing every path
// (caller falls back to default ask flow).
func (c *Context) SetOriginalCwd(p string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.originalCwd = p
}

// OriginalCwd returns the cwd recorded at startup. Empty when wiring
// hasn't called SetOriginalCwd yet.
func (c *Context) OriginalCwd() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.originalCwd
}

// OnDirectoriesChanged registers a callback fired after any
// successful AddDirectories / RemoveDirectories mutation. Wired by
// BashTool (to refresh sandbox allowWrite without restart) and by the
// engine (system prompt rebuild). Idempotent registration is the
// caller's responsibility; we don't attempt to dedup function values.
func (c *Context) OnDirectoriesChanged(fn func()) {
	if fn == nil {
		return
	}
	c.mu.Lock()
	c.dirObservers = append(c.dirObservers, fn)
	c.mu.Unlock()
}
