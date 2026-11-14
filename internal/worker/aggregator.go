// Package worker runs the background F4 aggregation of ingested events.
package worker

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
)

const (
	// BatchSize is how many events one fetch asks for.
	BatchSize = domain.MaxEventBatch
	// fetchWait is how long one fetch waits for messages. It also bounds how
	// long shutdown waits for an idle worker to notice the cancellation.
	fetchWait = 2 * time.Second
	// writeTimeout bounds one database write.
	writeTimeout = 10 * time.Second
	// backoffMin and backoffMax bound the pause after repeated failures.
	backoffMin = time.Second
	backoffMax = 30 * time.Second
	// maxAttempts is the delivery attempt after which a failing message is
	// terminated instead of retried. It matches the consumer's MaxDeliver.
	maxAttempts = 10
)

// Aggregator pulls event batches from the bus and stores them. Idempotency
// lives in the repository (ON CONFLICT on event_id), so at-least-once delivery
// from the bus never double counts.
type Aggregator struct {
	source domain.EventSource
	repo   domain.EventRepository
	logger *slog.Logger

	batchSize int
	wait      time.Duration
	sleep     func(ctx context.Context, d time.Duration)
}

// NewAggregator constructs an Aggregator.
func NewAggregator(source domain.EventSource, repo domain.EventRepository, logger *slog.Logger) (*Aggregator, error) {
	if source == nil || repo == nil {
		return nil, errors.New("worker: source and repository must not be nil")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Aggregator{
		source:    source,
		repo:      repo,
		logger:    logger,
		batchSize: BatchSize,
		wait:      fetchWait,
		sleep:     sleepCtx,
	}, nil
}

// Run processes batches until ctx is cancelled. A batch that was already
// fetched when cancellation arrives is still written and acknowledged (the
// database write uses a context detached from ctx), so shutdown drains the
// in-flight batch. Run returns after the loop has stopped.
func (a *Aggregator) Run(ctx context.Context) error {
	a.logger.Info("aggregation worker started", "batch_size", a.batchSize)
	defer a.logger.Info("aggregation worker stopped")

	backoff := time.Duration(0)
	for ctx.Err() == nil {
		deliveries, err := a.source.Fetch(ctx, a.batchSize, a.wait)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			backoff = nextBackoff(backoff)
			a.logger.Error("fetch events failed", "error", err, "retry_in", backoff.String())
			a.sleep(ctx, backoff)
			continue
		}
		if len(deliveries) == 0 {
			continue
		}

		if err := a.process(ctx, deliveries); err != nil {
			backoff = nextBackoff(backoff)
			a.logger.Error("store events failed", "error", err, "events", len(deliveries), "retry_in", backoff.String())
			a.sleep(ctx, backoff)
			continue
		}
		backoff = 0
	}
	return nil
}

// process writes one batch and settles every delivery. On a write failure the
// whole batch is nak'd (or terminated after maxAttempts) and the error returned.
func (a *Aggregator) process(ctx context.Context, deliveries []domain.EventDelivery) error {
	events := make([]domain.Event, len(deliveries))
	for i, d := range deliveries {
		events[i] = d.Event
	}

	// Detached from ctx so a shutdown signal cannot abort a write that is
	// already under way, but still bounded.
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), writeTimeout)
	defer cancel()

	inserted, err := a.repo.InsertBatch(wctx, events)
	if err != nil {
		for _, d := range deliveries {
			a.settleFailure(ctx, d)
		}
		return err
	}

	for _, d := range deliveries {
		if err := d.Ack(); err != nil {
			// The write is committed and idempotent; a missed ack only causes
			// a harmless redelivery that the repository skips.
			a.logger.Warn("ack failed", "event_id", d.Event.EventID, "error", err)
		}
	}
	a.logger.Debug("batch stored", "received", len(events), "inserted", inserted, "duplicates", len(events)-inserted)
	return nil
}

func (a *Aggregator) settleFailure(ctx context.Context, d domain.EventDelivery) {
	if d.Attempt >= maxAttempts {
		a.logger.Error("giving up on event", "event_id", d.Event.EventID, "attempts", d.Attempt)
		_ = d.Term()
		return
	}
	_ = d.Nak()
}

func nextBackoff(cur time.Duration) time.Duration {
	if cur < backoffMin {
		return backoffMin
	}
	return min(cur*2, backoffMax)
}

func sleepCtx(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
