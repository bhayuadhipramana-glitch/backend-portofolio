package domain

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// Event stream constants shared by the publisher and the consumer.
const (
	// EventStreamName is the JetStream stream holding ingested events.
	EventStreamName = "PORTFOLIO_EVENTS"
	// EventSubject is the subject every ingested event is published on.
	EventSubject = "events.ingest"
	// MaxEventBatch is the largest number of events one request may carry.
	MaxEventBatch = 100
)

// ErrEventsUnavailable is returned when the event bus cannot accept events.
var ErrEventsUnavailable = errors.New("event bus unavailable")

// Event is one telemetry event. EventID is the idempotency key end to end: it
// is the JetStream message ID and the primary key of the events table.
type Event struct {
	EventID    string          `json:"event_id"`
	EventType  string          `json:"event_type"`
	Source     string          `json:"source"`
	LatencyMS  float64         `json:"latency_ms"`
	IsError    bool            `json:"is_error"`
	Payload    json.RawMessage `json:"payload,omitempty"`
	OccurredAt time.Time       `json:"occurred_at"`
}

// EventPublisher puts validated events on the event bus.
type EventPublisher interface {
	// Publish returns only after the bus has durably accepted every event. It
	// is safe to retry: a repeated EventID is deduplicated by the bus.
	Publish(ctx context.Context, events []Event) error
	// Ping reports whether the bus is reachable.
	Ping(ctx context.Context) error
}

// EventRepository persists events and their per-minute rollups.
type EventRepository interface {
	// InsertBatch stores the events and updates the per-minute aggregates in
	// one atomic statement. It is idempotent per EventID: an event that is
	// already stored is skipped and never counted again. It returns how many
	// events were newly stored.
	InsertBatch(ctx context.Context, events []Event) (inserted int, err error)
}

// EventDelivery is one event taken from the bus, with the means to settle it.
type EventDelivery struct {
	Event Event
	// Attempt is the 1-based delivery attempt of this message.
	Attempt int
	// Ack removes the message from the bus. Nak asks for redelivery after a
	// delay. Term stops redelivery for good (poison messages).
	Ack  func() error
	Nak  func() error
	Term func() error
}

// EventSource pulls batches of events from the bus.
type EventSource interface {
	// Fetch returns up to max deliveries, waiting at most wait for them. An
	// empty result without an error means nothing arrived in time. Messages
	// that cannot be decoded are terminated by the source and never returned.
	Fetch(ctx context.Context, max int, wait time.Duration) ([]EventDelivery, error)
}
