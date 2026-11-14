package domain

import (
	"errors"
	"time"
)

// Demo burst bounds (F7). Requests are clamped into them, never rejected.
const (
	DemoMinCount       = 10
	DemoMaxCount       = 200
	DemoDefaultCount   = 100
	DemoMinDurationMS  = 500
	DemoMaxDurationMS  = 5000
	DemoDefaultDurMS   = 2000
	DemoEventSource    = "demo-burst"
	DemoMaxConcurrency = 2
)

// ErrBurstBusy is returned when the server-wide limit of concurrent bursts is reached.
var ErrBurstBusy = errors.New("too many demo bursts running")

// ClampDemoBurst applies the defaults for zero values and clamps the rest into
// the allowed ranges.
func ClampDemoBurst(count, durationMS int) (int, time.Duration) {
	if count == 0 {
		count = DemoDefaultCount
	}
	if durationMS == 0 {
		durationMS = DemoDefaultDurMS
	}
	count = min(max(count, DemoMinCount), DemoMaxCount)
	durationMS = min(max(durationMS, DemoMinDurationMS), DemoMaxDurationMS)
	return count, time.Duration(durationMS) * time.Millisecond
}
