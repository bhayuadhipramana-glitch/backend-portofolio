package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
)

// claimOutboxSQL locks due rows; concurrent dispatchers skip rows another one
// holds, so a message is never delivered by two workers at once.
const claimOutboxSQL = `
SELECT id, aggregate_type, aggregate_id, event_type, payload, attempts
FROM outbox
WHERE status IN ('pending', 'failed') AND next_attempt_at <= NOW()
ORDER BY next_attempt_at, id
LIMIT $1
FOR UPDATE SKIP LOCKED`

const markOutboxSentSQL = `
UPDATE outbox SET status = 'sent', attempts = attempts + 1, sent_at = NOW(), last_error = NULL
WHERE id = $1`

const markOutboxFailedSQL = `
UPDATE outbox SET status = $2, attempts = attempts + 1, last_error = $3, next_attempt_at = $4
WHERE id = $1`

// maxStoredError bounds the error text kept in last_error.
const maxStoredError = 1000

// OutboxRepository implements domain.OutboxRepository on PostgreSQL.
type OutboxRepository struct {
	pool *pgxpool.Pool
}

var _ domain.OutboxRepository = (*OutboxRepository)(nil)

// NewOutboxRepository returns an OutboxRepository on pool.
func NewOutboxRepository(pool *pgxpool.Pool) *OutboxRepository {
	return &OutboxRepository{pool: pool}
}

// Dispatch claims due messages, delivers them and records each outcome in the
// same transaction. A crash before commit releases the locks and leaves the
// rows untouched, so delivery is at-least-once.
func (r *OutboxRepository) Dispatch(
	ctx context.Context,
	limit, maxAttempts int,
	backoff func(attempt int) time.Duration,
	deliver func(context.Context, domain.OutboxMessage) error,
) (int, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin outbox tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx, claimOutboxSQL, limit)
	if err != nil {
		return 0, fmt.Errorf("claim outbox: %w", err)
	}
	msgs, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.OutboxMessage, error) {
		var m domain.OutboxMessage
		err := row.Scan(&m.ID, &m.AggregateType, &m.AggregateID, &m.EventType, &m.Payload, &m.Attempts)
		return m, err
	})
	if err != nil {
		return 0, fmt.Errorf("scan outbox: %w", err)
	}

	for _, m := range msgs {
		derr := deliver(ctx, m)
		if derr == nil {
			if _, err := tx.Exec(ctx, markOutboxSentSQL, m.ID); err != nil {
				return 0, fmt.Errorf("mark outbox sent: %w", err)
			}
			continue
		}

		attempts := m.Attempts + 1
		status := domain.OutboxStatusFailed
		if attempts >= maxAttempts {
			status = domain.OutboxStatusDead
		}
		text := derr.Error()
		if len(text) > maxStoredError {
			text = text[:maxStoredError]
		}
		next := time.Now().Add(backoff(attempts))
		if _, err := tx.Exec(ctx, markOutboxFailedSQL, m.ID, status, text, next); err != nil {
			return 0, fmt.Errorf("mark outbox failed: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit outbox tx: %w", err)
	}
	return len(msgs), nil
}

const pendingOutboxSQL = `SELECT COUNT(*) FROM outbox WHERE status IN ('pending', 'failed')`

// PendingCount returns how many messages still wait for delivery (pending or
// failed). It feeds the outbox backlog gauge; dead and sent rows are excluded.
func (r *OutboxRepository) PendingCount(ctx context.Context) (int64, error) {
	var n int64
	if err := r.pool.QueryRow(ctx, pendingOutboxSQL).Scan(&n); err != nil {
		return 0, fmt.Errorf("count pending outbox: %w", err)
	}
	return n, nil
}
