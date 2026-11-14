package domain

import (
	"context"
	"time"
)

// LiveWindowMinutes is how many per-minute buckets the live feed covers.
const LiveWindowMinutes = 15

// AggregateBucket is the rollup of every event type in one minute.
type AggregateBucket struct {
	Bucket       time.Time `json:"bucket"`
	TotalCount   int64     `json:"total_count"`
	ErrorCount   int64     `json:"error_count"`
	AvgLatencyMS float64   `json:"avg_latency_ms"`
	MaxLatencyMS float64   `json:"max_latency_ms"`
}

// RollingStats describes the most recent window. Because aggregates are kept
// per minute, the window starts at the previous minute boundary and ends now,
// so it is between 60 and 120 seconds wide and WindowSeconds says exactly how
// wide. That keeps the figures fresh without waiting for a minute to close.
type RollingStats struct {
	WindowSeconds float64 `json:"window_seconds"`
	TotalCount    int64   `json:"total_count"`
	RPS           float64 `json:"rps"`
	ErrorRate     float64 `json:"error_rate"` // 0..1
	AvgLatencyMS  float64 `json:"avg_latency_ms"`
	MaxLatencyMS  float64 `json:"max_latency_ms"`
}

// LiveSnapshot is one frame of the live feed.
type LiveSnapshot struct {
	GeneratedAt time.Time         `json:"generated_at"`
	Buckets     []AggregateBucket `json:"buckets"` // oldest first, one per minute, gaps filled with zeros
	Rolling     RollingStats      `json:"rolling"`
}

// EventStatsRepository reads the per-minute aggregates.
type EventStatsRepository interface {
	// Buckets returns one row per minute with bucket >= since, oldest first,
	// summed over every event type. Minutes without events are absent.
	Buckets(ctx context.Context, since time.Time) ([]AggregateBucket, error)
}
