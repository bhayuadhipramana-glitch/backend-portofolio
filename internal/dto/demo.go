package dto

import (
	"time"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
)

// DemoBurstRequest is the optional body of POST /api/v1/demo/burst. Both
// fields are optional; out-of-range values are clamped, not rejected.
type DemoBurstRequest struct {
	Count      *int `json:"count"`
	DurationMS *int `json:"duration_ms"`
}

// Resolve returns the effective burst size and duration: defaults for absent
// fields, clamped to the allowed ranges (count 10..200, duration 500..5000 ms).
func (r DemoBurstRequest) Resolve() (count int, duration time.Duration) {
	c, d := 0, 0
	if r.Count != nil {
		c = *r.Count
		if c == 0 {
			c = domain.DemoMinCount // an explicit 0 is a request for the minimum, not the default
		}
	}
	if r.DurationMS != nil {
		d = *r.DurationMS
		if d == 0 {
			d = domain.DemoMinDurationMS
		}
	}
	return domain.ClampDemoBurst(c, d)
}
