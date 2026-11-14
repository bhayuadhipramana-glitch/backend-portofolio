package usecase

import (
	"context"
	"errors"
	"fmt"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
)

// ProfileUsecase defines the business operations available for the Profile entity.
// Reads take a domain.Visibility: public callers only ever see the active profile.
type ProfileUsecase interface {
	Create(ctx context.Context, p *domain.Profile) error
	List(ctx context.Context, vis domain.Visibility, page domain.Page) ([]domain.Profile, error)
	GetByID(ctx context.Context, vis domain.Visibility, id string) (*domain.Profile, error)
	Update(ctx context.Context, p *domain.Profile) error
	Delete(ctx context.Context, id string) error
}

// profileUsecase is the concrete implementation of ProfileUsecase.
type profileUsecase struct {
	repo domain.ProfileRepository
}

// NewProfileUsecase constructs a ProfileUsecase with its required repository
// dependency. It returns an error if repo is nil to fail fast during wiring.
func NewProfileUsecase(repo domain.ProfileRepository) (ProfileUsecase, error) {
	if repo == nil {
		return nil, errors.New("usecase: profile repository must not be nil")
	}
	return &profileUsecase{repo: repo}, nil
}

// Create persists a new profile. Setting IsActive makes it the active profile.
func (u *profileUsecase) Create(ctx context.Context, p *domain.Profile) error {
	if p == nil {
		return errors.New("usecase: profile must not be nil")
	}
	return u.repo.Create(ctx, p)
}

// List retrieves one page of profiles.
func (u *profileUsecase) List(ctx context.Context, vis domain.Visibility, page domain.Page) ([]domain.Profile, error) {
	return u.repo.List(ctx, vis, page.Normalize())
}

// GetByID retrieves a single profile. Returns domain.ErrNotFound (wrapped) when
// the profile does not exist or is not visible to the caller.
func (u *profileUsecase) GetByID(ctx context.Context, vis domain.Visibility, id string) (*domain.Profile, error) {
	if id == "" {
		return nil, fmt.Errorf("usecase: invalid profile id %q", id)
	}
	return u.repo.GetByID(ctx, vis, id)
}

// Update replaces the full representation of a profile. Setting IsActive makes
// it the active profile.
func (u *profileUsecase) Update(ctx context.Context, p *domain.Profile) error {
	if p == nil {
		return errors.New("usecase: profile must not be nil")
	}
	if p.ID == "" {
		return fmt.Errorf("usecase: invalid profile id %q", p.ID)
	}
	return u.repo.Update(ctx, p)
}

// Delete removes a profile by its identifier.
func (u *profileUsecase) Delete(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("usecase: invalid profile id %q", id)
	}
	return u.repo.Delete(ctx, id)
}
