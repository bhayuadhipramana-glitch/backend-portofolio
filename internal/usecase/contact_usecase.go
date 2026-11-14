package usecase

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
)

// ContactUsecase handles contact form submissions (F9).
type ContactUsecase interface {
	// Submit stores the contact and its outbox message atomically. clientIP is
	// stored only as a SHA-256 hash.
	Submit(ctx context.Context, c *domain.Contact, clientIP string) error
	// List returns one page of contacts, newest first (admin only).
	List(ctx context.Context, page domain.Page) ([]domain.Contact, error)
}

type contactUsecase struct {
	repo domain.ContactRepository
}

// NewContactUsecase constructs a ContactUsecase.
func NewContactUsecase(repo domain.ContactRepository) (ContactUsecase, error) {
	if repo == nil {
		return nil, errors.New("usecase: contact repository must not be nil")
	}
	return &contactUsecase{repo: repo}, nil
}

// HashIP returns the lowercase hex SHA-256 of ip (64 characters).
func HashIP(ip string) string {
	sum := sha256.Sum256([]byte(ip))
	return hex.EncodeToString(sum[:])
}

func (u *contactUsecase) Submit(ctx context.Context, c *domain.Contact, clientIP string) error {
	if c == nil {
		return errors.New("usecase: contact must not be nil")
	}
	c.IPHash = HashIP(clientIP)
	return u.repo.CreateWithOutbox(ctx, c)
}

func (u *contactUsecase) List(ctx context.Context, page domain.Page) ([]domain.Contact, error) {
	return u.repo.List(ctx, page.Normalize())
}
