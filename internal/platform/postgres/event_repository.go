package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
)

// insertEventsSQL is one atomic statement, so it is a single transaction:
//
//   - inserted keeps only rows that were not stored before (ON CONFLICT DO
//     NOTHING also swallows duplicates inside the same batch);
//   - rollup groups just those new rows by minute and type;
//   - the upsert adds the rollup to event_aggregates.
//
// Because the rollup reads from inserted, a redelivered or duplicate event
// never reaches the counters. All inputs are bound parameters.
const insertEventsSQL = `
WITH input AS (
    SELECT * FROM unnest($1::text[], $2::text[], $3::text[], $4::float8[], $5::bool[], $6::text[], $7::timestamptz[])
        AS t(event_id, event_type, source, latency_ms, is_error, payload, occurred_at)
),
inserted AS (
    INSERT INTO events (event_id, event_type, source, latency_ms, is_error, payload, occurred_at)
    SELECT event_id::uuid, event_type, source, latency_ms, is_error, payload::jsonb, occurred_at
    FROM input
    ON CONFLICT (event_id) DO NOTHING
    RETURNING event_type, latency_ms, is_error, occurred_at
),
rollup AS (
    SELECT date_trunc('minute', occurred_at) AS bucket,
           event_type,
           count(*)                           AS total_count,
           count(*) FILTER (WHERE is_error)   AS error_count,
           sum(latency_ms)                    AS sum_latency_ms,
           max(latency_ms)                    AS max_latency_ms
    FROM inserted
    GROUP BY 1, 2
),
upserted AS (
    INSERT INTO event_aggregates AS a (bucket, event_type, total_count, error_count, sum_latency_ms, max_latency_ms, updated_at)
    SELECT bucket, event_type, total_count, error_count, sum_latency_ms, max_latency_ms, NOW()
    FROM rollup
    ON CONFLICT (bucket, event_type) DO UPDATE SET
        total_count    = a.total_count + EXCLUDED.total_count,
        error_count    = a.error_count + EXCLUDED.error_count,
        sum_latency_ms = a.sum_latency_ms + EXCLUDED.sum_latency_ms,
        max_latency_ms = GREATEST(a.max_latency_ms, EXCLUDED.max_latency_ms),
        updated_at     = NOW()
    RETURNING 1
)
SELECT count(*) FROM inserted`

// EventRepository implements domain.EventRepository on PostgreSQL.
type EventRepository struct {
	pool *pgxpool.Pool
}

var _ domain.EventRepository = (*EventRepository)(nil)

// NewEventRepository returns an EventRepository on pool.
func NewEventRepository(pool *pgxpool.Pool) *EventRepository {
	return &EventRepository{pool: pool}
}

// InsertBatch stores events and updates the per-minute aggregates atomically.
// It is idempotent per event_id.
func (r *EventRepository) InsertBatch(ctx context.Context, events []domain.Event) (int, error) {
	if len(events) == 0 {
		return 0, nil
	}

	n := len(events)
	ids := make([]string, n)
	types := make([]string, n)
	sources := make([]string, n)
	latencies := make([]float64, n)
	errs := make([]bool, n)
	payloads := make([]string, n)
	occurred := make([]time.Time, n)
	for i, e := range events {
		ids[i] = e.EventID
		types[i] = e.EventType
		sources[i] = e.Source
		latencies[i] = e.LatencyMS
		errs[i] = e.IsError
		payloads[i] = string(orEmptyObject(e.Payload))
		occurred[i] = e.OccurredAt
	}

	var inserted int
	err := r.pool.QueryRow(ctx, insertEventsSQL, ids, types, sources, latencies, errs, payloads, occurred).Scan(&inserted)
	if err != nil {
		return 0, fmt.Errorf("insert events batch: %w", err)
	}
	return inserted, nil
}
