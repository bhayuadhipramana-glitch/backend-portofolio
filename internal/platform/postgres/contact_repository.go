package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
)

const insertContactSQL = `
INSERT INTO contacts (name, email, subject, message, ip_hash)
VALUES ($1, $2, $3, $4, $5)
RETURNING id, created_at`

// The outbox payload carries everything the notifier needs, so delivery never
// has to read the contacts table (and never exposes ip_hash).
const insertOutboxSQL = `
INSERT INTO outbox (aggregate_type, aggregate_id, event_type, payload)
VALUES ($1, $2::uuid, $3, jsonb_build_object(
    'contact_id', $2::text, 'name', $4::text, 'email', $5::text,
    'subject', $6::text, 'message', $7::text))`

const listContactsSQL = `
SELECT id, name, email, subject, message, created_at
FROM contacts
ORDER BY created_at DESC, id DESC
LIMIT $1 OFFSET $2`

// ContactRepository implements domain.ContactRepository on PostgreSQL.
type ContactRepository struct {
	pool *pgxpool.Pool
}

var _ domain.ContactRepository = (*ContactRepository)(nil)

// NewContactRepository returns a ContactRepository on pool.
func NewContactRepository(pool *pgxpool.Pool) *ContactRepository {
	return &ContactRepository{pool: pool}
}

// CreateWithOutbox inserts the contact and its outbox row in one transaction.
func (r *ContactRepository) CreateWithOutbox(ctx context.Context, c *domain.Contact) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin contact tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after a successful commit

	if err := tx.QueryRow(ctx, insertContactSQL, c.Name, c.Email, c.Subject, c.Message, c.IPHash).
		Scan(&c.ID, &c.CreatedAt); err != nil {
		return fmt.Errorf("insert contact: %w", err)
	}
	if _, err := tx.Exec(ctx, insertOutboxSQL,
		domain.OutboxAggregateContact, c.ID, domain.OutboxEventContactCreated,
		c.Name, c.Email, c.Subject, c.Message); err != nil {
		return fmt.Errorf("insert outbox: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit contact tx: %w", err)
	}
	return nil
}

// List returns contacts newest first.
func (r *ContactRepository) List(ctx context.Context, page domain.Page) ([]domain.Contact, error) {
	page = page.Normalize()
	rows, err := r.pool.Query(ctx, listContactsSQL, page.Limit, page.Offset)
	if err != nil {
		return nil, fmt.Errorf("list contacts query: %w", err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Contact, error) {
		var c domain.Contact
		err := row.Scan(&c.ID, &c.Name, &c.Email, &c.Subject, &c.Message, &c.CreatedAt)
		return c, err
	})
}
