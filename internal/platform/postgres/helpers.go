package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// pgUniqueViolation is the SQLSTATE raised on a UNIQUE constraint violation.
const pgUniqueViolation = "23505"

// wrapWriteErr maps a unique-constraint violation (duplicate slug, second
// active profile/resume, ...) to domain.ErrConflict and wraps anything else.
func wrapWriteErr(op string, err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation {
		return fmt.Errorf("%s: constraint %s: %w", op, pgErr.ConstraintName, domain.ErrConflict)
	}
	return fmt.Errorf("%s query: %w", op, err)
}

// acquireLock takes a transaction-scoped advisory lock. It is released
// automatically on commit or rollback.
func acquireLock(ctx context.Context, tx pgx.Tx, key int64) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, key); err != nil {
		return fmt.Errorf("acquire advisory lock: %w", err)
	}
	return nil
}

// orEmptyStrings turns a nil slice into an empty one. pgx encodes a nil slice
// as SQL NULL, which a NOT NULL TEXT[] column rejects, and JSON would emit null.
func orEmptyStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// orEmptyObject turns a nil/empty RawMessage into {}. A nil RawMessage is
// encoded as SQL NULL, which a NOT NULL JSONB column rejects, and a NULL read
// back would marshal as null.
func orEmptyObject(m json.RawMessage) json.RawMessage {
	if len(m) == 0 {
		return json.RawMessage(`{}`)
	}
	return m
}
