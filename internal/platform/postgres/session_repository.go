package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SessionRepository implements domain.SessionRepository on PostgreSQL.
type SessionRepository struct {
	pool *pgxpool.Pool
}

// NewSessionRepository returns a SessionRepository backed by pool.
func NewSessionRepository(pool *pgxpool.Pool) *SessionRepository {
	return &SessionRepository{pool: pool}
}

// Create stores a new session.
func (r *SessionRepository) Create(ctx context.Context, s *domain.Session) error {
	query := `
		INSERT INTO sessions (token_hash, github_id, login, expires_at)
		VALUES ($1, $2, $3, $4)
		RETURNING created_at
	`
	err := r.pool.QueryRow(ctx, query, s.TokenHash, s.GithubID, s.Login, s.ExpiresAt).Scan(&s.CreatedAt)
	if err != nil {
		return fmt.Errorf("create session query: %w", err)
	}
	return nil
}

// GetByTokenHash returns a live session and records last_seen_at. Expired and
// revoked sessions are treated as missing.
func (r *SessionRepository) GetByTokenHash(ctx context.Context, tokenHash string) (*domain.Session, error) {
	query := `
		UPDATE sessions
		SET last_seen_at = now()
		WHERE token_hash = $1 AND expires_at > now() AND revoked_at IS NULL
		RETURNING token_hash, github_id, login, expires_at, created_at, revoked_at, last_seen_at
	`
	var s domain.Session
	err := r.pool.QueryRow(ctx, query, tokenHash).Scan(
		&s.TokenHash, &s.GithubID, &s.Login, &s.ExpiresAt, &s.CreatedAt, &s.RevokedAt, &s.LastSeenAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("get session: %w", domain.ErrNotFound)
		}
		return nil, fmt.Errorf("get session query: %w", err)
	}
	return &s, nil
}

// Revoke marks a session revoked (logout). It does not error when the session
// is already gone or revoked.
func (r *SessionRepository) Revoke(ctx context.Context, tokenHash string) error {
	query := `UPDATE sessions SET revoked_at = now() WHERE token_hash = $1 AND revoked_at IS NULL`
	if _, err := r.pool.Exec(ctx, query, tokenHash); err != nil {
		return fmt.Errorf("revoke session query: %w", err)
	}
	return nil
}

// DeleteExpired removes sessions past their expiry, revoked or not.
func (r *SessionRepository) DeleteExpired(ctx context.Context) error {
	if _, err := r.pool.Exec(ctx, `DELETE FROM sessions WHERE expires_at <= now()`); err != nil {
		return fmt.Errorf("delete expired sessions query: %w", err)
	}
	return nil
}
