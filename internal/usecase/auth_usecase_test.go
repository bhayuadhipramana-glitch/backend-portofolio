package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
)

type fakeProvider struct{ user domain.GitHubUser }

func (fakeProvider) AuthCodeURL(state string) string { return "https://example.test/?state=" + state }
func (f fakeProvider) Exchange(context.Context, string) (*domain.GitHubUser, error) {
	return &f.user, nil
}

// fakeSessions is an in-memory domain.SessionRepository.
type fakeSessions struct {
	rows map[string]*domain.Session
}

func newFakeSessions() *fakeSessions { return &fakeSessions{rows: map[string]*domain.Session{}} }

func (f *fakeSessions) Create(_ context.Context, s *domain.Session) error {
	cp := *s
	f.rows[s.TokenHash] = &cp
	return nil
}

func (f *fakeSessions) GetByTokenHash(_ context.Context, h string) (*domain.Session, error) {
	s, ok := f.rows[h]
	if !ok || s.RevokedAt != nil || !s.ExpiresAt.After(time.Now()) {
		return nil, domain.ErrNotFound
	}
	cp := *s
	return &cp, nil
}

func (f *fakeSessions) Revoke(_ context.Context, h string) error {
	if s, ok := f.rows[h]; ok {
		now := time.Now()
		s.RevokedAt = &now
	}
	return nil
}

func (f *fakeSessions) DeleteExpired(context.Context) error { return nil }

func newAuth(t *testing.T, adminID int64, repo domain.SessionRepository) AuthUsecase {
	t.Helper()
	uc, err := NewAuthUsecase(fakeProvider{user: domain.GitHubUser{ID: 42, Login: "admin"}}, repo, adminID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return uc
}

func TestAuthLoginAuthenticateLogout(t *testing.T) {
	ctx := context.Background()
	uc := newAuth(t, 42, newFakeSessions())

	token, _, err := uc.CompleteLogin(ctx, "code")
	if err != nil {
		t.Fatalf("CompleteLogin: %v", err)
	}
	if _, err := uc.Authenticate(ctx, token); err != nil {
		t.Fatalf("Authenticate after login: %v", err)
	}

	if err := uc.Logout(ctx, token); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if _, err := uc.Authenticate(ctx, token); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("revoked session must be unauthorized, got %v", err)
	}
}

func TestAuthRejectsNonAdminLogin(t *testing.T) {
	uc := newAuth(t, 99, newFakeSessions()) // provider returns user 42
	if _, _, err := uc.CompleteLogin(context.Background(), "code"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("want ErrForbidden, got %v", err)
	}
}

func TestAuthRejectsSessionOfAnotherAdmin(t *testing.T) {
	ctx := context.Background()
	repo := newFakeSessions()

	token, _, err := newAuth(t, 42, repo).CompleteLogin(ctx, "code")
	if err != nil {
		t.Fatal(err)
	}

	// The configured admin changes after the session was issued.
	rotated := newAuth(t, 7, repo)
	if _, err := rotated.Authenticate(ctx, token); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("session of a different github id must be unauthorized, got %v", err)
	}
}
