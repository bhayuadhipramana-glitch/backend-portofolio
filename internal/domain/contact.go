package domain

import (
	"context"
	"time"
)

// Outbox constants for the contact aggregate.
const (
	OutboxAggregateContact    = "contact"
	OutboxEventContactCreated = "contact.created"

	OutboxStatusPending = "pending"
	OutboxStatusFailed  = "failed"
	OutboxStatusSent    = "sent"
	// OutboxStatusDead marks a message that exhausted its delivery attempts.
	OutboxStatusDead = "dead"

	// OutboxMaxAttempts is the number of delivery attempts before a message is dead.
	OutboxMaxAttempts = 5
)

// Contact is one stored contact form submission.
type Contact struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Email     string    `json:"email"`
	Subject   string    `json:"subject"`
	Message   string    `json:"message"`
	IPHash    string    `json:"-"`
	CreatedAt time.Time `json:"created_at"`
}

// OutboxMessage is a pending side effect written in the same transaction as
// the aggregate that caused it.
type OutboxMessage struct {
	ID            string
	AggregateType string
	AggregateID   string
	EventType     string
	Payload       []byte
	Attempts      int
}

// ContactRepository persists contacts.
type ContactRepository interface {
	// CreateWithOutbox stores the contact and its outbox message in one
	// transaction: both are committed or neither is. It fills c.ID/CreatedAt.
	CreateWithOutbox(ctx context.Context, c *Contact) error
	// List returns contacts newest first (admin only).
	List(ctx context.Context, page Page) ([]Contact, error)
}

// OutboxRepository is the worker's view of the outbox.
type OutboxRepository interface {
	// Dispatch claims up to limit due messages with FOR UPDATE SKIP LOCKED,
	// calls deliver for each inside the claiming transaction and records the
	// outcome: sent, failed with a backoff, or dead after maxAttempts. It
	// returns how many messages it handled.
	Dispatch(ctx context.Context, limit, maxAttempts int, backoff func(attempt int) time.Duration, deliver func(context.Context, OutboxMessage) error) (int, error)
}

// ContactNotifier delivers a contact notification to the site owner.
type ContactNotifier interface {
	Notify(ctx context.Context, msg OutboxMessage) error
}
