// Package usecase implements application-level business rules.
//
// Each usecase orchestrates one or more domain entities and repository calls.
// Usecases are the only layer that handlers depend on directly, enforcing the
// Handler → Usecase → Repository boundary.
package usecase

import (
	"context"
	"errors"
	"fmt"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
)

// ProjectUsecase defines the business operations available for the Project entity.
type ProjectUsecase interface {
	Create(ctx context.Context, p *domain.Project) error
	List(ctx context.Context, page domain.Page) ([]domain.Project, error)
	GetByID(ctx context.Context, id string) (*domain.Project, error)
	Update(ctx context.Context, p *domain.Project) error
	Delete(ctx context.Context, id string) error
}

// projectUsecase is the concrete implementation of ProjectUsecase.
type projectUsecase struct {
	repo  domain.ProjectRepository
	cache domain.ContentCache
}

// NewProjectUsecase constructs a ProjectUsecase with its required repository
// dependencies. It returns an error if repo or cache is nil to fail fast during wiring.
func NewProjectUsecase(repo domain.ProjectRepository, cache domain.ContentCache) (ProjectUsecase, error) {
	if repo == nil {
		return nil, errors.New("usecase: project repository must not be nil")
	}
	if cache == nil {
		return nil, errors.New("usecase: project cache must not be nil")
	}
	return &projectUsecase{repo: repo, cache: cache}, nil
}

// Create persists a new project.
func (u *projectUsecase) Create(ctx context.Context, p *domain.Project) error {
	if p == nil {
		return errors.New("usecase: project must not be nil")
	}
	if err := u.repo.Create(ctx, p); err != nil {
		return err
	}
	invalidate(ctx, u.cache, domain.CacheNamespaceProjects)
	return nil
}

// List retrieves one page of projects.
func (u *projectUsecase) List(ctx context.Context, page domain.Page) ([]domain.Project, error) {
	page = page.Normalize()
	return cached(ctx, u.cache, domain.CacheNamespaceProjects, listKey(page), func(ctx context.Context) ([]domain.Project, error) {
		return u.repo.List(ctx, page)
	})
}

// GetByID retrieves a single project. Returns domain.ErrNotFound (wrapped) when
// the project does not exist; callers should use errors.Is to check.
func (u *projectUsecase) GetByID(ctx context.Context, id string) (*domain.Project, error) {
	if id == "" {
		return nil, fmt.Errorf("usecase: invalid project id %q", id)
	}
	return cached(ctx, u.cache, domain.CacheNamespaceProjects, "id:"+id, func(ctx context.Context) (*domain.Project, error) {
		return u.repo.GetByID(ctx, id)
	})
}

// Update replaces the full representation of a project.
func (u *projectUsecase) Update(ctx context.Context, p *domain.Project) error {
	if p == nil {
		return errors.New("usecase: project must not be nil")
	}
	if p.ID == "" {
		return fmt.Errorf("usecase: invalid project id %q", p.ID)
	}
	if err := u.repo.Update(ctx, p); err != nil {
		return err
	}
	invalidate(ctx, u.cache, domain.CacheNamespaceProjects)
	return nil
}

// Delete removes a project by its identifier.
func (u *projectUsecase) Delete(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("usecase: invalid project id %q", id)
	}
	if err := u.repo.Delete(ctx, id); err != nil {
		return err
	}
	invalidate(ctx, u.cache, domain.CacheNamespaceProjects)
	return nil
}
