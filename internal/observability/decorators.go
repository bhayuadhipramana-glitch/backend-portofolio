package observability

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
)

// InstrumentCache counts every Fetch by namespace and result (HIT-L1, HIT-L2,
// MISS). The cache reports its result through the context trace, so the
// decorator gives the inner cache its own trace, reads it, and forwards the
// status to the caller's trace (which feeds the X-Cache header).
func (p *Provider) InstrumentCache(next domain.ContentCache) domain.ContentCache {
	return &cacheMetrics{next: next, p: p}
}

type cacheMetrics struct {
	next domain.ContentCache
	p    *Provider
}

func (c *cacheMetrics) Fetch(ctx context.Context, namespace, key string, load func(context.Context) ([]byte, error)) ([]byte, error) {
	inner, tr := domain.WithCacheTrace(ctx)
	v, err := c.next.Fetch(inner, namespace, key, load)
	if err != nil {
		return nil, err
	}
	if status := tr.Status(); status != "" {
		domain.RecordCacheStatus(ctx, status)
		c.p.cacheReqs.Add(ctx, 1, metric.WithAttributes(
			attribute.String("cache_namespace", namespace),
			attribute.String("cache_result", string(status)),
		))
	}
	return v, nil
}

func (c *cacheMetrics) Invalidate(ctx context.Context, namespace string) error {
	return c.next.Invalidate(ctx, namespace)
}

func (c *cacheMetrics) Ping(ctx context.Context) error { return c.next.Ping(ctx) }

// InstrumentPublisher counts published events by outcome (ok or error).
func (p *Provider) InstrumentPublisher(next domain.EventPublisher) domain.EventPublisher {
	return &publisherMetrics{next: next, p: p}
}

type publisherMetrics struct {
	next domain.EventPublisher
	p    *Provider
}

func (m *publisherMetrics) Publish(ctx context.Context, events []domain.Event) error {
	err := m.next.Publish(ctx, events)
	outcome := "ok"
	if err != nil {
		outcome = "error"
	}
	m.p.natsPub.Add(ctx, int64(len(events)), metric.WithAttributes(attribute.String("outcome", outcome)))
	return err
}

func (m *publisherMetrics) Ping(ctx context.Context) error { return m.next.Ping(ctx) }

// InstrumentSource counts events pulled by the aggregation worker.
func (p *Provider) InstrumentSource(next domain.EventSource) domain.EventSource {
	return &sourceMetrics{next: next, p: p}
}

type sourceMetrics struct {
	next domain.EventSource
	p    *Provider
}

func (s *sourceMetrics) Fetch(ctx context.Context, max int, wait time.Duration) ([]domain.EventDelivery, error) {
	ds, err := s.next.Fetch(ctx, max, wait)
	if n := len(ds); n > 0 {
		s.p.natsConsumed.Add(ctx, int64(n))
	}
	return ds, err
}

// InstrumentOutbox counts delivery attempts by outcome: sent, failed, or dead
// (the attempt that exhausted maxAttempts, mirroring the repository's rule).
func (p *Provider) InstrumentOutbox(next domain.OutboxRepository) domain.OutboxRepository {
	return &outboxMetrics{next: next, p: p}
}

type outboxMetrics struct {
	next domain.OutboxRepository
	p    *Provider
}

func (o *outboxMetrics) Dispatch(
	ctx context.Context,
	limit, maxAttempts int,
	backoff func(attempt int) time.Duration,
	deliver func(context.Context, domain.OutboxMessage) error,
) (int, error) {
	return o.next.Dispatch(ctx, limit, maxAttempts, backoff, func(ctx context.Context, m domain.OutboxMessage) error {
		err := deliver(ctx, m)
		outcome := "sent"
		switch {
		case err != nil && m.Attempts+1 >= maxAttempts:
			outcome = "dead"
		case err != nil:
			outcome = "failed"
		}
		o.p.outboxOut.Add(ctx, 1, metric.WithAttributes(attribute.String("outcome", outcome)))
		return err
	})
}
