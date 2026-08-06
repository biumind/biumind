// Internal hook handlers for bundled plugins.
//
// Each PP8d plugin (hookify / ralph-loop / security-guard) registers
// its handler functions here at package init() so the runtime hook
// dispatcher can find them by name when the plugin's hooks.json
// declares `"type": "internal"`.
//
// Per-plugin files live next to this one — handlers_hookify.go,
// handlers_ralph.go, handlers_security.go — to keep each plugin's
// implementation cohesive while sharing this single registration
// point.
//
// Why one init() rather than per-plugin init()s: a single init makes
// the handler-name → function map visible at a glance during code
// review, and disables the "did I forget to import the side-effect
// package" footgun. Each per-plugin file exports its handlers as
// regular functions; this file is the only place names get bound.

package bundled

import "github.com/biumind/biumind/apps/cli/biu/internal/hooks"

// init registers every bundled plugin's internal hook handlers.
//
// Names follow `<plugin>:<event>` so /plugin disable can prefix-match
// (today's disable already takes the whole plugin offline; the prefix
// convention is for forward compatibility with finer-grained hooks
// management).
func init() {
	hooks.RegisterInternal("hookify:pre-tool", hookifyPreTool)
	hooks.RegisterInternal("hookify:user-prompt", hookifyUserPrompt)

	hooks.RegisterInternal("ralph-loop:stop", ralphLoopStop)

	hooks.RegisterInternal("security-guard:pre-tool", securityGuardPreTool)
}
