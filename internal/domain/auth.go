package domain

import (
	"context"
	"errors"
	"time"
)

var (
	// ErrUnauthorized is returned when a request carries no valid session.
	ErrUnauthorized = errors.New("unauthorized")
	// ErrForbidden is returned when the authenticated GitHub user is not the admin.
	ErrForbidden = errors.New("forbidden")
)

// GitHubUser is the minimal identity GitHub returns after login.
type GitHubUser struct {
	ID    int64
	Login string
}

// Session is a server-side admin session. TokenHash is the SHA-256 (hex) of
// the token sent to the browser.
type Session struct {
	TokenHash  string     `json:"-"`
	GithubID   int64      `json:"github_id"`
	Login      string     `json:"login"`
	ExpiresAt  time.Time  `json:"expires_at"`
	CreatedAt  time.Time  `json:"created_at"`
	RevokedAt  *time.Time `json:"-"`
	LastSeenAt *time.Time `json:"-"`
}

// SessionRepository is the persistence contract for sessions.
type SessionRepository interface {
	Create(ctx context.Context, s *Session) error
	// GetByTokenHash returns ErrNotFound when the session is missing, expired
	// or revoked. A successful lookup records last_seen_at.
	GetByTokenHash(ctx context.Context, tokenHash string) (*Session, error)
	// Revoke marks the session revoked; it is a no-op for an unknown session.
	Revoke(ctx context.Context, tokenHash string) error
	DeleteExpired(ctx context.Context) error
}

// IdentityProvider is the contract to the OAuth2 provider (GitHub).
type IdentityProvider interface {
	AuthCodeURL(state string) string
	Exchange(ctx context.Context, code string) (*GitHubUser, error)
}
