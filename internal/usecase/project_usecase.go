// Package usecase implements application-level business rules.
//
// Each usecase orchestrates one or more domain entities and repository calls.
// Usecases are the only layer that handlers depend on directly, enforcing the
// Clean Architecture boundary: Handler → Usecase → Repository.
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
	List(ctx context.Context) ([]domain.Project, error)
	GetByID(ctx context.Context, id int) (*domain.Project, error)
	Update(ctx context.Context, p *domain.Project) error
	Delete(ctx context.Context, id int) error
}

// projectUsecase is the concrete implementation of ProjectUsecase.
type projectUsecase struct {
	repo domain.ProjectRepository
}

// NewProjectUsecase constructs a ProjectUsecase with its required repository
// dependency. It returns an error if repo is nil to fail fast during wiring.
func NewProjectUsecase(repo domain.ProjectRepository) (ProjectUsecase, error) {
	if repo == nil {
		return nil, errors.New("usecase: project repository must not be nil")
	}
	return &projectUsecase{repo: repo}, nil
}

// Create validates business invariants and persists a new project.
func (u *projectUsecase) Create(ctx context.Context, p *domain.Project) error {
	if p == nil {
		return fmt.Errorf("usecase: project must not be nil")
	}
	return u.repo.Create(ctx, p)
}

// List retrieves all projects ordered by the repository's default sort.
func (u *projectUsecase) List(ctx context.Context) ([]domain.Project, error) {
	return u.repo.List(ctx)
}

// GetByID retrieves a single project. Returns domain.ErrNotFound (wrapped) when
// the project does not exist—callers should use errors.Is to check.
func (u *projectUsecase) GetByID(ctx context.Context, id int) (*domain.Project, error) {
	if id <= 0 {
		return nil, fmt.Errorf("usecase: invalid project id %d", id)
	}
	return u.repo.GetByID(ctx, id)
}

// Update replaces the full representation of a project.
func (u *projectUsecase) Update(ctx context.Context, p *domain.Project) error {
	if p == nil {
		return fmt.Errorf("usecase: project must not be nil")
	}
	if p.ID <= 0 {
		return fmt.Errorf("usecase: invalid project id %d", p.ID)
	}
	return u.repo.Update(ctx, p)
}

// Delete removes a project by its identifier.
func (u *projectUsecase) Delete(ctx context.Context, id int) error {
	if id <= 0 {
		return fmt.Errorf("usecase: invalid project id %d", id)
	}
	return u.repo.Delete(ctx, id)
}
