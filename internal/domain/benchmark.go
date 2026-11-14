package domain

import (
	"context"
	"encoding/json"
	"time"
)

// CacheNamespaceBenchmarks holds the public benchmark history reads.
const CacheNamespaceBenchmarks = "benchmarks"

// Benchmark is one k6 run published by CI.
type Benchmark struct {
	ID          string          `json:"id"`
	CommitSHA   string          `json:"commit_sha"`
	Branch      string          `json:"branch"`
	Scenario    string          `json:"scenario"`
	TargetRPS   int             `json:"target_rps"`
	AchievedRPS float64         `json:"achieved_rps"`
	P50MS       float64         `json:"p50_ms"`
	P95MS       float64         `json:"p95_ms"`
	P99MS       float64         `json:"p99_ms"`
	ErrorRate   float64         `json:"error_rate"`
	RawSummary  json.RawMessage `json:"raw_summary"`
	CreatedAt   time.Time       `json:"created_at"`
}

// BenchmarkRepository persists benchmark runs.
type BenchmarkRepository interface {
	Create(ctx context.Context, b *Benchmark) error
	// List returns runs newest first.
	List(ctx context.Context, page Page) ([]Benchmark, error)
}
