// Cron expression parsing.
//
// Wraps robfig/cron/v3 with a tiny shim that:
//
//   1. Adds the same minimum-interval guard as biuapp.Validator (no
//      "* * * * *"), so a manifest that slipped past the validator
//      can't sneak past the dispatcher either.
//   2. Returns NextRun anchored to a caller-supplied "now" so unit
//      tests are deterministic.
//
// We do NOT support seconds-precision cron (5-field only). Apps that
// need sub-minute cadence should be re-modelled as long-poll loops
// inside their own runtime, not run through the platform scheduler.

package triggers

import (
	"errors"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

// stdParser accepts the standard 5-field cron form (minute hour dom
// month dow). robfig/cron defaults to a 6-field form with seconds;
// we explicitly disable that to match validator semantics.
var stdParser = cron.NewParser(
	cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow,
)

// ErrTooFrequent rejects "* * * * *" (every-minute cadence).
var ErrTooFrequent = errors.New("cron: minimum interval is 1 minute; '* * * * *' is not allowed")

// Parse validates the cron expression and returns the parsed schedule.
// Returns ErrTooFrequent for the every-minute pattern (matches
// biuapp.Validator's check).
func Parse(expr string) (cron.Schedule, error) {
	expr = strings.TrimSpace(expr)
	fields := strings.Fields(expr)
	if len(fields) == 5 && fields[0] == "*" {
		return nil, ErrTooFrequent
	}
	return stdParser.Parse(expr)
}

// NextRun returns the next fire time for expr after `from`. The
// caller supplies `from` so tests can pin a fake clock.
func NextRun(expr string, from time.Time) (time.Time, error) {
	s, err := Parse(expr)
	if err != nil {
		return time.Time{}, err
	}
	return s.Next(from), nil
}
