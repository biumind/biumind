// Package adapters bridges concrete biu types into the small
// interfaces engine / hooks expose, so the wiring layer doesn't
// have to redefine the same trivial shims in every entry point.
//
// Why these exist:
//
//   * engine declares minimal interfaces (PlanHinter, BgTaskNotifier)
//     to keep its dependency direction one-way — it can't import
//     planhint / bgtask without flipping the dependency graph and
//     creating an import cycle.
//   * hooks declares TrustGate for the same reason — it doesn't
//     want to depend on internal/trust.
//
// The natural shape that satisfies these interfaces lives in this
// package: each constructor takes the concrete type and returns the
// interface engine / hooks consume. Callers (cmd/biu, pkg/biumindkit)
// stop carrying private adapter types of their own.
//
// Each adapter is intentionally tiny (one method or two) — the
// idea is to be the thinnest possible glue, not a place that grows
// new behaviour. Behaviour belongs in the source package.

package adapters

import (
	"os"

	"github.com/biumind/biumind/apps/cli/biu/internal/bgtask"
	"github.com/biumind/biumind/apps/cli/biu/internal/engine"
	"github.com/biumind/biumind/apps/cli/biu/internal/hooks"
	"github.com/biumind/biumind/apps/cli/biu/internal/mcp"
	"github.com/biumind/biumind/apps/cli/biu/internal/planhint"
	"github.com/biumind/biumind/apps/cli/biu/internal/trust"
)

// PlanHint returns an engine.PlanHinter backed by the planhint
// Analyser. nil-safe: a nil analyser yields a nil interface, which
// engine.New treats as "no hint suggestion path".
func PlanHint(a *planhint.Analyser) engine.PlanHinter {
	if a == nil {
		return nil
	}
	return planHintAdapter{inner: a}
}

type planHintAdapter struct{ inner *planhint.Analyser }

func (a planHintAdapter) Enabled() bool { return a.inner.Enabled() }

func (a planHintAdapter) Analyse(prompt string) engine.PlanHint {
	s := a.inner.Analyse(prompt)
	return engine.PlanHint{Note: s.Note, MatchedKeyword: s.MatchedKeyword}
}

// BgTask returns an engine.BgTaskNotifier backed by the bgtask
// store. nil store yields a nil interface so the engine skips
// the notification block entirely.
//
// The tail size (number of trailing output lines surfaced in the
// completion attachment) is fixed at 10 — matches what main.go
// and sdk.go used. If a future caller needs a different size we
// can take it as a parameter.
func BgTask(s *bgtask.Store) engine.BgTaskNotifier {
	if s == nil {
		return nil
	}
	return bgTaskAdapter{store: s}
}

type bgTaskAdapter struct{ store *bgtask.Store }

func (a bgTaskAdapter) PendingCompletions() []engine.BgTaskCompletion {
	pending := a.store.Pending()
	if len(pending) == 0 {
		return nil
	}
	const tailLines = 10 // ~screen-friendly preview
	out := make([]engine.BgTaskCompletion, 0, len(pending))
	for _, snap := range pending {
		out = append(out, engine.BgTaskCompletion{
			ID:       snap.ID,
			Command:  snap.Command,
			Status:   string(snap.Status),
			ExitCode: snap.ExitCode,
			Tail:     a.store.Tail(snap.ID, tailLines),
		})
	}
	return out
}

// HookTrustGate returns a hooks.TrustGate that asks the trust
// store on every call whether the *current* cwd is trusted. nil
// store yields a nil interface = legacy "no gating" mode.
//
// We resolve cwd at IsTrustedNow time (not at construction) so an
// in-session `/trust here` flips the gate immediately for the next
// hook firing. cwd-resolution failure is treated as untrusted —
// erring on the safe side rather than silently letting hooks fire
// for an unexpected location.
func HookTrustGate(s *trust.Store) hooks.TrustGate {
	if s == nil {
		return nil
	}
	return cwdTrustAdapter{store: s}
}

// MCPTrustGate is the mcp.TrustGate variant. Same backing struct as
// HookTrustGate — both interfaces share the IsTrustedNow() bool shape;
// we expose two constructors so callers hand the engine a typed value
// matching the interface that package declares.
func MCPTrustGate(s *trust.Store) mcp.TrustGate {
	if s == nil {
		return nil
	}
	return cwdTrustAdapter{store: s}
}

type cwdTrustAdapter struct{ store *trust.Store }

func (a cwdTrustAdapter) IsTrustedNow() bool {
	cwd, err := os.Getwd()
	if err != nil {
		return false
	}
	return a.store.IsTrusted(cwd)
}
