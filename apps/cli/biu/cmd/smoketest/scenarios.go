// Scenario type + global registry.
//
// Each Layer in the P20.47 v2 plan owns its own scenarios_<layer>.go
// file. Files register their scenarios via init() so the runner in
// main.go stays layer-agnostic and we don't need to touch a master
// switch every time a layer lands.

package main

import (
	"time"

	"github.com/biumind/biumind/apps/cli/biu/pkg/biumindkit"
)

type scenario struct {
	name      string
	prompt    string
	wantTools []string // any of these names firing satisfies the assertion
	timeout   time.Duration
	prep      func(workdir string) error // optional pre-test setup

	// system overrides the default system prompt for this scenario.
	system string

	// modelOverride forces a specific model (instead of $ANTHROPIC_MODEL).
	// Used by A19. Empty string = use the env default.
	modelOverride string

	// policy installs a custom permission policy. Default = PermissionAllow.
	// Deny-flow cases (A8, G-layer) install PermissionDeny / custom fns.
	policy biumindkit.PermissionPolicyFn

	// permissionMode pins the active mode (default / plan / acceptEdits /
	// bypass). Empty string = engine default. Used by Layer G.
	permissionMode string

	// extraTools registers additional tools for this scenario.
	extraTools []biumindkit.Tool

	// maxTokens caps the per-request output budget. 0 = SDK default (4096).
	maxTokens int

	// assertText is an optional final-text predicate. Returning a non-nil
	// error fails the case even if wantTools matched — useful for
	// verifying sentinel strings, output-style effects, etc.
	assertText func(text string) error

	// bannedTools fails the case if the model invokes any of these.
	// Used by Layer G/H to prove a deny / sandbox rule actually
	// closed the door instead of the model just choosing not to use
	// the tool.
	bannedTools []string

	// allowEmptyText, when true, accepts cases where the engine
	// produced zero assistant text. Default behaviour treats empty
	// text as a failure (most LLM rounds DO end with a final
	// message); some deny-flow scenarios where the model bails
	// without text need this escape.
	allowEmptyText bool

	// loadMemory toggles BIUMIND.md ingestion. Default = NoMemory so
	// scenarios are deterministic across machines. Cases that need
	// memory primer set this explicitly.
	loadMemory bool

	// loadSettings toggles ~/.biumind + project settings.json
	// loading. Default = NoSettings; Layer G/L use this to drive
	// settings-based permission rules.
	loadSettings bool

	// skipReason returns a non-empty string to skip the scenario (printed
	// as the reason). Empty = run.
	skipReason func() string
}

type result struct {
	scenario     string
	pass         bool
	skip         bool
	skipReason   string
	err          error
	elapsed      time.Duration
	inTokens     int
	outTokens    int
	cacheReadTks int
	cacheWriteTk int
	toolsFired   []string
	toolInputs   []string // one entry per ToolStart, "name(args-summary)"
	preview      string
	model        string
}

// scenarios is the global registry. Each Layer's init() appends to
// it via register(); the runner iterates in registration order.
var scenarios []scenario

// register adds one scenario to the global list. Call from init().
func register(s scenario) { scenarios = append(scenarios, s) }
