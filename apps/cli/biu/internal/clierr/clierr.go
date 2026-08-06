// Package clierr standardises user-facing error messages across the
// biu CLI.
//
// One format, applied everywhere:
//
//   biu: <component>: <what failed> — <fix-it hint>
//
// Example:
//
//   biu: config: parse ~/.biu/config.toml: line 14: invalid escape — fix the quoting then run `biu doctor`
//
// The single-line format is on purpose: subcommand RunE returns flow
// up to cmd/biu/main.go which already prefixes `biu: %v`. So the
// constructors here add the component + hint, but NOT the outer
// `biu:` prefix — main.go's printer adds that.
//
// Three primitives:
//
//   - Newf(component, format, args)     wraps fmt.Errorf with the
//                                       component prefix.
//   - Wrapf(component, err, format)     attaches a layer while
//                                       keeping the cause for
//                                       errors.Is/As.
//   - WithHint(err, hint)               appends ` — <hint>` and
//                                       preserves the cause.
//
// Plus path helpers — DisplayPath maps absolute paths under $HOME to
// `~/…` so error output doesn't leak whose laptop produced it.

package clierr

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// Newf returns an error formatted as `<component>: <msg>`. The outer
// `biu: ` is added by the cmd/biu/main.go printer when the error
// surfaces from a subcommand RunE.
func Newf(component, format string, args ...any) error {
	return fmt.Errorf(component+": "+format, args...)
}

// Wrapf attaches a layer onto an existing error while keeping the
// cause discoverable via errors.Is / errors.As.
//
//	if err != nil {
//	    return clierr.Wrapf("config", err, "load %s", DisplayPath(p))
//	}
//
// produces `config: load ~/.biu/config.toml: <inner>` — main.go then
// prints `biu: config: load ~/.biu/config.toml: <inner>`.
func Wrapf(component string, err error, format string, args ...any) error {
	if err == nil {
		return nil
	}
	prefix := fmt.Sprintf(component+": "+format, args...)
	return fmt.Errorf("%s: %w", prefix, err)
}

// WithHint appends ` — <hint>` to the error message. The hint should
// be a short, actionable next step ("run `biu auth login`") rather
// than a generic "try again". Returns nil when err is nil.
//
// The cause chain is preserved: errors.Is / errors.As still walk
// through the hinted error to whatever was wrapped.
func WithHint(err error, hint string) error {
	if err == nil {
		return nil
	}
	if hint == "" {
		return err
	}
	return &hintedError{cause: err, hint: hint}
}

// hintedError carries a cause + a fix-it hint that's printed once
// when the error surfaces. Implements Unwrap so errors.Is/As work.
type hintedError struct {
	cause error
	hint  string
}

func (e *hintedError) Error() string {
	return e.cause.Error() + " — " + e.hint
}

func (e *hintedError) Unwrap() error { return e.cause }

// DisplayPath returns p with $HOME collapsed to `~`. Use it whenever
// an error message contains a filesystem path the user might paste
// into a bug report — it strips machine-identifying detail without
// losing the "which file" signal.
//
// Returns p unchanged when:
//   - the path doesn't start with the user's home directory, or
//   - $HOME isn't resolvable (rare; the OS hasn't told us).
func DisplayPath(p string) string {
	if p == "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if p == home {
		return "~"
	}
	if strings.HasPrefix(p, home+string(os.PathSeparator)) {
		return "~" + p[len(home):]
	}
	return p
}

// IsHinted reports whether err carries a fix-it hint (i.e. WithHint
// has been applied somewhere in the chain). Mainly useful in tests.
func IsHinted(err error) bool {
	var h *hintedError
	return errors.As(err, &h)
}
