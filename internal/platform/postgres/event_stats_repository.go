package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
)

// bucketsSQL sums every event type per minute. The window is at most 15 rows,
// and LIMIT keeps the query bounded even if the caller passes an old since.
const bucketsSQL = `
SELECT bucket,
       SUM(total_count)::bigint,
       SUM(error_count)::bigint,
       CASE WHEN SUM(total_count) > 0 THEN SUM(sum_latency_ms) / SUM(total_count) ELSE 0 END,
       MAX(max_latency_ms)
FROM event_aggregates
WHERE bucket >= $1
GROUP BY bucket
ORDER BY bucket ASC
LIMIT $2`

// EventStatsRepository implements domain.EventStatsRepository on PostgreSQL.
type EventStatsRepository struct {
	pool *pgxpool.Pool
}

var _ domain.EventStatsRepository = (*EventStatsRepository)(nil)

// NewEventStatsRepository returns an EventStatsRepository on pool.
func NewEventStatsRepository(pool *pgxpool.Pool) *EventStatsRepository {
	return &EventStatsRepository{pool: pool}
}

// Buckets returns the per-minute rollups since the given minute.
func (r *EventStatsRepository) Buckets(ctx context.Context, since time.Time) ([]domain.AggregateBucket, error) {
	rows, err := r.pool.Query(ctx, bucketsSQL, since, domain.LiveWindowMinutes+1)
	if err != nil {
		return nil, fmt.Errorf("query event buckets: %w", err)
	}
	defer rows.Close()

	out := make([]domain.AggregateBucket, 0, domain.LiveWindowMinutes)
	for rows.Next() {
		var b domain.AggregateBucket
		if err := rows.Scan(&b.Bucket, &b.TotalCount, &b.ErrorCount, &b.AvgLatencyMS, &b.MaxLatencyMS); err != nil {
			return nil, fmt.Errorf("scan event bucket: %w", err)
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read event buckets: %w", err)
	}
	return out, nil
}
