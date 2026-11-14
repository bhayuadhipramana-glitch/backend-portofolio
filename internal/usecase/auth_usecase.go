package usecase

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
)

// AuthUsecase defines the admin login/session operations.
type AuthUsecase interface {
	// LoginURL returns the GitHub consent URL for the given CSRF state.
	LoginURL(state string) string
	// CompleteLogin exchanges the OAuth code, verifies the user is the admin and
	// opens a session. It returns the raw token to hand to the browser and the
	// session. Returns domain.ErrForbidden for any non-admin GitHub account.
	CompleteLogin(ctx context.Context, code string) (string, *domain.Session, error)
	// Authenticate resolves a raw session token. Returns domain.ErrUnauthorized
	// when the token is unknown, expired, revoked or not the admin's.
	Authenticate(ctx context.Context, token string) (*domain.Session, error)
	// Logout revokes the session for a raw token.
	Logout(ctx context.Context, token string) error
}

type authUsecase struct {
	provider domain.IdentityProvider
	repo     domain.SessionRepository
	adminID  int64
	ttl      time.Duration
}

// NewAuthUsecase constructs an AuthUsecase. adminID is the numeric GitHub user
// id of the single admin; ttl is how long a session stays valid.
func NewAuthUsecase(provider domain.IdentityProvider, repo domain.SessionRepository, adminID int64, ttl time.Duration) (AuthUsecase, error) {
	if provider == nil {
		return nil, errors.New("usecase: identity provider must not be nil")
	}
	if repo == nil {
		return nil, errors.New("usecase: session repository must not be nil")
	}
	if adminID <= 0 {
		return nil, errors.New("usecase: admin github id must be positive")
	}
	if ttl <= 0 {
		return nil, errors.New("usecase: session ttl must be positive")
	}
	return &authUsecase{provider: provider, repo: repo, adminID: adminID, ttl: ttl}, nil
}

func (u *authUsecase) LoginURL(state string) string {
	return u.provider.AuthCodeURL(state)
}

func (u *authUsecase) CompleteLogin(ctx context.Context, code string) (string, *domain.Session, error) {
	user, err := u.provider.Exchange(ctx, code)
	if err != nil {
		return "", nil, err
	}
	if user.ID != u.adminID {
		return "", nil, fmt.Errorf("github user %q: %w", user.Login, domain.ErrForbidden)
	}

	token, err := newToken()
	if err != nil {
		return "", nil, err
	}
	s := &domain.Session{
		TokenHash: hashToken(token),
		GithubID:  user.ID,
		Login:     user.Login,
		ExpiresAt: time.Now().Add(u.ttl),
	}
	if err := u.repo.Create(ctx, s); err != nil {
		return "", nil, err
	}

	// Housekeeping must never fail a successful login.
	_ = u.repo.DeleteExpired(ctx)

	return token, s, nil
}

func (u *authUsecase) Authenticate(ctx context.Context, token string) (*domain.Session, error) {
	if token == "" {
		return nil, domain.ErrUnauthorized
	}
	s, err := u.repo.GetByTokenHash(ctx, hashToken(token))
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, domain.ErrUnauthorized
		}
		return nil, err
	}
	// Defence in depth: the repository already rejects expired and revoked
	// sessions; also require the session to belong to the configured admin so
	// a stale session survives neither an admin change nor a forged row.
	if s.RevokedAt != nil || s.GithubID != u.adminID {
		return nil, domain.ErrUnauthorized
	}
	return s, nil
}

func (u *authUsecase) Logout(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	return u.repo.Revoke(ctx, hashToken(token))
}

// newToken returns 32 bytes of CSPRNG output, base64url encoded.
func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate session token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
