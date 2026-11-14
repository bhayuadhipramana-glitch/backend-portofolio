package worker

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
)

const (
	// outboxBatch is how many messages one dispatch claims. Delivery happens
	// inside the claiming transaction, so a small batch keeps locks short.
	outboxBatch = 10
	// outboxPoll is the pause when the outbox is empty.
	outboxPoll = 2 * time.Second
	// outboxDispatchTimeout bounds one claim-deliver-commit round, including
	// every notifier call in it.
	outboxDispatchTimeout = 60 * time.Second
	// outboxBackoffBase and outboxBackoffMax bound the retry delay of a failed
	// message: 30 s, 1 min, 2 min, 4 min (five attempts in total).
	outboxBackoffBase = 30 * time.Second
	outboxBackoffMax  = time.Hour
)

// OutboxBackoff returns how long to wait before the next attempt, given the
// number of attempts already made (1-based): exponential, capped.
func OutboxBackoff(attempt int) time.Duration {
	d := outboxBackoffBase
	for i := 1; i < attempt && d < outboxBackoffMax; i++ {
		d *= 2
	}
	return min(d, outboxBackoffMax)
}

// OutboxDispatcher delivers outbox messages through a ContactNotifier. The
// repository claims rows with FOR UPDATE SKIP LOCKED and records the outcome,
// so several instances never deliver the same message at once.
type OutboxDispatcher struct {
	repo     domain.OutboxRepository
	notifier domain.ContactNotifier
	logger   *slog.Logger

	batch       int
	poll        time.Duration
	maxAttempts int
	backoff     func(attempt int) time.Duration
	sleep       func(ctx context.Context, d time.Duration)
}

// NewOutboxDispatcher constructs an OutboxDispatcher.
func NewOutboxDispatcher(repo domain.OutboxRepository, notifier domain.ContactNotifier, logger *slog.Logger) (*OutboxDispatcher, error) {
	if repo == nil || notifier == nil {
		return nil, errors.New("worker: outbox repository and notifier must not be nil")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &OutboxDispatcher{
		repo:        repo,
		notifier:    notifier,
		logger:      logger,
		batch:       outboxBatch,
		poll:        outboxPoll,
		maxAttempts: domain.OutboxMaxAttempts,
		backoff:     OutboxBackoff,
		sleep:       sleepCtx,
	}, nil
}

// Run dispatches until ctx is cancelled. A round that already started is
// finished (its context is detached from ctx), so shutdown never abandons a
// half-delivered batch. Run returns after the loop has stopped.
func (d *OutboxDispatcher) Run(ctx context.Context) error {
	d.logger.Info("outbox dispatcher started", "batch", d.batch, "max_attempts", d.maxAttempts)
	defer d.logger.Info("outbox dispatcher stopped")

	for ctx.Err() == nil {
		n, err := d.DispatchOnce(ctx)
		switch {
		case err != nil:
			d.logger.Error("outbox dispatch failed", "error", err)
			d.sleep(ctx, d.poll)
		case n == 0:
			d.sleep(ctx, d.poll)
		}
		// n > 0: more messages may be due, loop again immediately.
	}
	return nil
}

// DispatchOnce runs one claim-deliver-commit round and returns how many
// messages it handled.
func (d *OutboxDispatcher) DispatchOnce(ctx context.Context) (int, error) {
	dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), outboxDispatchTimeout)
	defer cancel()

	return d.repo.Dispatch(dctx, d.batch, d.maxAttempts, d.backoff, func(ctx context.Context, m domain.OutboxMessage) error {
		err := d.notifier.Notify(ctx, m)
		if err != nil {
			d.logger.Warn("outbox delivery failed", "outbox_id", m.ID, "attempt", m.Attempts+1, "error", err)
		}
		return err
	})
}
