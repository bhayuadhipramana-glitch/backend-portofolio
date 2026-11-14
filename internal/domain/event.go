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
