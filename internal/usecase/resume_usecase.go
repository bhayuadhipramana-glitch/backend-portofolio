package usecase

import (
	"context"
	"errors"
	"fmt"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
)

// ResumeUsecase defines the business operations available for the Resume entity.
// Reads take a domain.Visibility: public callers only ever see the active resume.
type ResumeUsecase interface {
	Create(ctx context.Context, res *domain.Resume) error
	List(ctx context.Context, vis domain.Visibility, page domain.Page) ([]domain.Resume, error)
	GetByID(ctx context.Context, vis domain.Visibility, id string) (*domain.Resume, error)
	Update(ctx context.Context, res *domain.Resume) error
	Delete(ctx context.Context, id string) error
}

// resumeUsecase is the concrete implementation of ResumeUsecase.
type resumeUsecase struct {
	repo domain.ResumeRepository
}

// NewResumeUsecase constructs a ResumeUsecase with its required repository
// dependency. It returns an error if repo is nil to fail fast during wiring.
func NewResumeUsecase(repo domain.ResumeRepository) (ResumeUsecase, error) {
	if repo == nil {
		return nil, errors.New("usecase: resume repository must not be nil")
	}
	return &resumeUsecase{repo: repo}, nil
}

// Create persists a new resume. Setting IsActive makes it the active resume.
func (u *resumeUsecase) Create(ctx context.Context, res *domain.Resume) error {
	if res == nil {
		return errors.New("usecase: resume must not be nil")
	}
	return u.repo.Create(ctx, res)
}

// List retrieves one page of resumes.
func (u *resumeUsecase) List(ctx context.Context, vis domain.Visibility, page domain.Page) ([]domain.Resume, error) {
	return u.repo.List(ctx, vis, page.Normalize())
}

// GetByID retrieves a single resume. Returns domain.ErrNotFound (wrapped) when
// the resume does not exist or is not visible to the caller.
func (u *resumeUsecase) GetByID(ctx context.Context, vis domain.Visibility, id string) (*domain.Resume, error) {
	if id == "" {
		return nil, fmt.Errorf("usecase: invalid resume id %q", id)
	}
	return u.repo.GetByID(ctx, vis, id)
}

// Update replaces the full representation of a resume. Setting IsActive makes
// it the active resume.
func (u *resumeUsecase) Update(ctx context.Context, res *domain.Resume) error {
	if res == nil {
		return errors.New("usecase: resume must not be nil")
	}
	if res.ID == "" {
		return fmt.Errorf("usecase: invalid resume id %q", res.ID)
	}
	return u.repo.Update(ctx, res)
}

// Delete removes a resume by its identifier.
func (u *resumeUsecase) Delete(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("usecase: invalid resume id %q", id)
	}
	return u.repo.Delete(ctx, id)
}
