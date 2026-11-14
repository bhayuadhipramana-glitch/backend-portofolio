package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
)

const insertBenchmarkSQL = `
INSERT INTO benchmarks (commit_sha, branch, scenario, target_rps, achieved_rps, p50_ms, p95_ms, p99_ms, error_rate, raw_summary)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10::jsonb)
RETURNING id, created_at`

const listBenchmarksSQL = `
SELECT id, commit_sha, branch, scenario, target_rps, achieved_rps, p50_ms, p95_ms, p99_ms, error_rate, raw_summary, created_at
FROM benchmarks
ORDER BY created_at DESC, id DESC
LIMIT $1 OFFSET $2`

// BenchmarkRepository implements domain.BenchmarkRepository on PostgreSQL.
type BenchmarkRepository struct {
	pool *pgxpool.Pool
}

var _ domain.BenchmarkRepository = (*BenchmarkRepository)(nil)

// NewBenchmarkRepository returns a BenchmarkRepository on pool.
func NewBenchmarkRepository(pool *pgxpool.Pool) *BenchmarkRepository {
	return &BenchmarkRepository{pool: pool}
}

// Create stores a benchmark run and fills b.ID and b.CreatedAt.
func (r *BenchmarkRepository) Create(ctx context.Context, b *domain.Benchmark) error {
	b.RawSummary = orEmptyObject(b.RawSummary)
	err := r.pool.QueryRow(ctx, insertBenchmarkSQL,
		b.CommitSHA, b.Branch, b.Scenario, b.TargetRPS, b.AchievedRPS,
		b.P50MS, b.P95MS, b.P99MS, b.ErrorRate, string(b.RawSummary)).
		Scan(&b.ID, &b.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert benchmark: %w", err)
	}
	return nil
}

// List returns benchmark runs newest first.
func (r *BenchmarkRepository) List(ctx context.Context, page domain.Page) ([]domain.Benchmark, error) {
	page = page.Normalize()
	rows, err := r.pool.Query(ctx, listBenchmarksSQL, page.Limit, page.Offset)
	if err != nil {
		return nil, fmt.Errorf("list benchmarks query: %w", err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Benchmark, error) {
		var b domain.Benchmark
		var raw []byte
		err := row.Scan(&b.ID, &b.CommitSHA, &b.Branch, &b.Scenario, &b.TargetRPS, &b.AchievedRPS,
			&b.P50MS, &b.P95MS, &b.P99MS, &b.ErrorRate, &raw, &b.CreatedAt)
		b.RawSummary = raw
		return b, err
	})
}
