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
	repo  domain.ProfileRepository
	cache domain.ContentCache
}

// NewProfileUsecase constructs a ProfileUsecase with its required repository
// dependencies. It returns an error if repo or cache is nil to fail fast during wiring.
func NewProfileUsecase(repo domain.ProfileRepository, cache domain.ContentCache) (ProfileUsecase, error) {
	if repo == nil {
		return nil, errors.New("usecase: profile repository must not be nil")
	}
	if cache == nil {
		return nil, errors.New("usecase: profile cache must not be nil")
	}
	return &profileUsecase{repo: repo, cache: cache}, nil
}

// Create persists a new profile. Setting IsActive makes it the active profile.
func (u *profileUsecase) Create(ctx context.Context, p *domain.Profile) error {
	if p == nil {
		return errors.New("usecase: profile must not be nil")
	}
	if err := u.repo.Create(ctx, p); err != nil {
		return err
	}
	invalidate(ctx, u.cache, domain.CacheNamespaceProfiles)
	return nil
}

// List retrieves one page of profiles.
func (u *profileUsecase) List(ctx context.Context, vis domain.Visibility, page domain.Page) ([]domain.Profile, error) {
	page = page.Normalize()
	if vis != domain.VisibilityPublic {
		return u.repo.List(ctx, vis, page) // admin reads are never cached
	}
	return cached(ctx, u.cache, domain.CacheNamespaceProfiles, listKey(page), func(ctx context.Context) ([]domain.Profile, error) {
		return u.repo.List(ctx, vis, page)
	})
}

// GetByID retrieves a single profile. Returns domain.ErrNotFound (wrapped) when
// the profile does not exist or is not visible to the caller.
func (u *profileUsecase) GetByID(ctx context.Context, vis domain.Visibility, id string) (*domain.Profile, error) {
	if id == "" {
		return nil, fmt.Errorf("usecase: invalid profile id %q", id)
	}
	if vis != domain.VisibilityPublic {
		return u.repo.GetByID(ctx, vis, id)
	}
	return cached(ctx, u.cache, domain.CacheNamespaceProfiles, "id:"+id, func(ctx context.Context) (*domain.Profile, error) {
		return u.repo.GetByID(ctx, vis, id)
	})
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
	if err := u.repo.Update(ctx, p); err != nil {
		return err
	}
	invalidate(ctx, u.cache, domain.CacheNamespaceProfiles)
	return nil
}

// Delete removes a profile by its identifier.
func (u *profileUsecase) Delete(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("usecase: invalid profile id %q", id)
	}
	if err := u.repo.Delete(ctx, id); err != nil {
		return err
	}
	invalidate(ctx, u.cache, domain.CacheNamespaceProfiles)
	return nil
}
