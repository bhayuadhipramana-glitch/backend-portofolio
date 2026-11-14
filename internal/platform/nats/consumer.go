package nats

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
)

const (
	// consumerName is the durable pull consumer of the aggregation worker.
	consumerName = "event-aggregator"
	// consumerAckWait is how long a fetched message may stay unacked before
	// JetStream redelivers it.
	consumerAckWait = 30 * time.Second
	// ConsumerMaxDeliver bounds redelivery of a message that keeps failing.
	ConsumerMaxDeliver = 10
	// nakDelay spaces out retries of a failed batch.
	nakDelay = 5 * time.Second
	// maxFetchBytes bounds the memory of one fetched batch.
	maxFetchBytes = 8 << 20
)

// EventSource implements domain.EventSource with a durable pull consumer.
type EventSource struct {
	cons jetstream.Consumer
}

var _ domain.EventSource = (*EventSource)(nil)

// NewEventSource creates (or reattaches to) the durable consumer, so unacked
// messages survive a worker restart.
func (c *Client) NewEventSource(ctx context.Context) (*EventSource, error) {
	ctx, cancel := context.WithTimeout(ctx, streamSetupTimeout)
	defer cancel()

	cons, err := c.js.CreateOrUpdateConsumer(ctx, c.stream, jetstream.ConsumerConfig{
		Durable:       consumerName,
		FilterSubject: c.subject,
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       consumerAckWait,
		MaxDeliver:    ConsumerMaxDeliver,
		DeliverPolicy: jetstream.DeliverAllPolicy,
	})
	if err != nil {
		return nil, fmt.Errorf("ensure consumer %s: %w", consumerName, err)
	}
	return &EventSource{cons: cons}, nil
}

// Fetch pulls up to max messages, waiting at most wait. Undecodable messages
// are terminated here so they never block the stream.
func (s *EventSource) Fetch(ctx context.Context, max int, wait time.Duration) ([]domain.EventDelivery, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	batch, err := s.cons.Fetch(max, jetstream.FetchMaxWait(wait), jetstream.FetchMaxBytes(maxFetchBytes))
	if err != nil {
		if errors.Is(err, nats.ErrTimeout) || errors.Is(err, jetstream.ErrNoMessages) {
			return nil, nil
		}
		return nil, fmt.Errorf("fetch events: %w", err)
	}

	var out []domain.EventDelivery
	for msg := range batch.Messages() {
		var ev domain.Event
		if err := json.Unmarshal(msg.Data(), &ev); err != nil || ev.EventID == "" {
			slog.WarnContext(ctx, "nats: dropping undecodable event message", "error", err)
			_ = msg.Term()
			continue
		}

		attempt := 1
		if md, err := msg.Metadata(); err == nil {
			attempt = int(md.NumDelivered)
		}
		out = append(out, domain.EventDelivery{
			Event:   ev,
			Attempt: attempt,
			Ack:     msg.Ack,
			Nak:     func() error { return msg.NakWithDelay(nakDelay) },
			Term:    msg.Term,
		})
	}
	if err := batch.Error(); err != nil && !errors.Is(err, nats.ErrTimeout) && !errors.Is(err, jetstream.ErrNoMessages) {
		if len(out) == 0 {
			return nil, fmt.Errorf("fetch events: %w", err)
		}
		slog.WarnContext(ctx, "nats: fetch ended with error after partial batch", "error", err)
	}
	return out, nil
}
