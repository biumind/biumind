// Package decay applies time-based score decay.
//
//	final = base * exp(-lambda * age_days)
//
// Default half-life is 30 days (lambda = ln(2)/30 ≈ 0.0231).
// Set HalfLifeDays = 0 to disable.
package decay

import (
	"math"
	"time"
)

type Decay struct {
	HalfLifeDays float64
	Now          func() time.Time // injectable for tests
}

func New(halfLifeDays float64) *Decay {
	return &Decay{HalfLifeDays: halfLifeDays, Now: time.Now}
}

// Apply returns the score after decay relative to t.
func (d *Decay) Apply(base float64, t time.Time) float64 {
	if d.HalfLifeDays <= 0 || t.IsZero() {
		return base
	}
	now := time.Now()
	if d.Now != nil {
		now = d.Now()
	}
	ageDays := now.Sub(t).Hours() / 24
	if ageDays < 0 {
		ageDays = 0
	}
	lambda := math.Ln2 / d.HalfLifeDays
	return base * math.Exp(-lambda*ageDays)
}
