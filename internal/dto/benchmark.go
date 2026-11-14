package dto

import (
	"encoding/json"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
)

// BenchmarkRequest is the body of POST /api/v1/benchmarks, sent by CI.
type BenchmarkRequest struct {
	CommitSHA   string          `json:"commit_sha"   validate:"required,hexadecimal,min=7,max=40"`
	Branch      string          `json:"branch"       validate:"omitempty,notblank,max=100"`
	Scenario    string          `json:"scenario"     validate:"required,notblank,max=64"`
	TargetRPS   *int            `json:"target_rps"   validate:"required,gte=0"`
	AchievedRPS *float64        `json:"achieved_rps" validate:"required,gte=0"`
	P50MS       *float64        `json:"p50_ms"       validate:"required,gte=0"`
	P95MS       *float64        `json:"p95_ms"       validate:"required,gte=0"`
	P99MS       *float64        `json:"p99_ms"       validate:"required,gte=0"`
	ErrorRate   *float64        `json:"error_rate"   validate:"required,gte=0,lte=1"`
	RawSummary  json.RawMessage `json:"raw_summary"  validate:"omitempty,jsonobject"`
}

// ToDomain converts a validated request. Call it only after Validate returned nil.
func (r BenchmarkRequest) ToDomain() domain.Benchmark {
	branch := r.Branch
	if branch == "" {
		branch = "main"
	}
	return domain.Benchmark{
		CommitSHA:   r.CommitSHA,
		Branch:      branch,
		Scenario:    r.Scenario,
		TargetRPS:   *r.TargetRPS,
		AchievedRPS: *r.AchievedRPS,
		P50MS:       *r.P50MS,
		P95MS:       *r.P95MS,
		P99MS:       *r.P99MS,
		ErrorRate:   *r.ErrorRate,
		RawSummary:  r.RawSummary,
	}
}
