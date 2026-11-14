package nats

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
)

// Publisher implements domain.EventPublisher on JetStream.
type Publisher struct {
	client *Client
}

var _ domain.EventPublisher = (*Publisher)(nil)

// NewPublisher returns a Publisher on an established Client.
func NewPublisher(c *Client) *Publisher {
	return &Publisher{client: c}
}

// Ping reports whether NATS is reachable.
func (p *Publisher) Ping(ctx context.Context) error {
	return p.client.Ping(ctx)
}

// Publish sends every event with Nats-Msg-Id set to its event_id and waits
// for all JetStream acknowledgements, so a nil error means the events are
// stored. A repeated event_id inside the dedup window is acked and dropped.
func (p *Publisher) Publish(ctx context.Context, events []domain.Event) error {
	futures := make([]jetstream.PubAckFuture, 0, len(events))
	for _, ev := range events {
		body, err := json.Marshal(ev)
		if err != nil {
			return fmt.Errorf("marshal event %s: %w", ev.EventID, err)
		}
		msg := &nats.Msg{
			Subject: p.client.subject,
			Data:    body,
			Header:  nats.Header{},
		}
		msg.Header.Set(jetstream.MsgIDHeader, ev.EventID)

		f, err := p.client.js.PublishMsgAsync(msg)
		if err != nil {
			return fmt.Errorf("publish event %s: %w", ev.EventID, err)
		}
		futures = append(futures, f)
	}

	for _, f := range futures {
		select {
		case <-f.Ok():
		case err := <-f.Err():
			return fmt.Errorf("publish ack: %w", err)
		case <-ctx.Done():
			return fmt.Errorf("publish ack: %w", ctx.Err())
		}
	}
	return nil
}
