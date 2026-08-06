// Engine-level error sentinels that are part of the public contract
// between the engine and biumindkit / wiring layers above it.

package engine

import "errors"

// ErrInterrupted is the cancel cause callers should attach to a
// context.WithCancelCause when deliberately stopping a Submit. The
// engine inspects context.Cause(ctx) on cancel and emits
// DoneEvent{StopReason:"interrupted"} instead of an ErrorEvent so:
//
//   - downstream consumers can render a clean "stopped" state
//   - history stays well-formed for replay (every tool_use that the
//     model emitted has a matching synthetic tool_result row)
//
// Plain ctx.Done() with no cause (or any cause other than this
// sentinel) keeps the legacy ErrorEvent behaviour for parent-cancel
// / timeout cases — those are still failures the caller wants to see.
var ErrInterrupted = errors.New("biu: interrupted by user")
