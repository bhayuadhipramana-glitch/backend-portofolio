package usecase

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
)

// ArticleUsecase defines the business operations available for the Article entity.
// Reads take a domain.Visibility: public callers only ever see published articles.
type ArticleUsecase interface {
	Create(ctx context.Context, a *domain.Article) error
	List(ctx context.Context, vis domain.Visibility, page domain.Page) ([]domain.Article, error)
	GetByID(ctx context.Context, vis domain.Visibility, id string) (*domain.Article, error)
	GetBySlug(ctx context.Context, vis domain.Visibility, slug string) (*domain.Article, error)
	Update(ctx context.Context, a *domain.Article) error
	Delete(ctx context.Context, id string) error
}

// articleUsecase is the concrete implementation of ArticleUsecase.
type articleUsecase struct {
	repo domain.ArticleRepository
}

// NewArticleUsecase constructs an ArticleUsecase with its required repository
// dependency. It returns an error if repo is nil to fail fast during wiring.
func NewArticleUsecase(repo domain.ArticleRepository) (ArticleUsecase, error) {
	if repo == nil {
		return nil, errors.New("usecase: article repository must not be nil")
	}
	return &articleUsecase{repo: repo}, nil
}

// Create persists a new article.
func (u *articleUsecase) Create(ctx context.Context, a *domain.Article) error {
	if a == nil {
		return errors.New("usecase: article must not be nil")
	}
	stampPublishedAt(a)
	return u.repo.Create(ctx, a)
}

// List retrieves one page of articles.
func (u *articleUsecase) List(ctx context.Context, vis domain.Visibility, page domain.Page) ([]domain.Article, error) {
	return u.repo.List(ctx, vis, page.Normalize())
}

// GetByID retrieves a single article. Returns domain.ErrNotFound (wrapped) when
// the article does not exist or is not visible to the caller.
func (u *articleUsecase) GetByID(ctx context.Context, vis domain.Visibility, id string) (*domain.Article, error) {
	if id == "" {
		return nil, fmt.Errorf("usecase: invalid article id %q", id)
	}
	return u.repo.GetByID(ctx, vis, id)
}

// GetBySlug retrieves a single article by slug. Returns domain.ErrNotFound
// (wrapped) when the article does not exist or is not visible to the caller.
func (u *articleUsecase) GetBySlug(ctx context.Context, vis domain.Visibility, slug string) (*domain.Article, error) {
	if slug == "" {
		return nil, fmt.Errorf("usecase: invalid article slug %q", slug)
	}
	return u.repo.GetBySlug(ctx, vis, slug)
}

// Update replaces the full representation of an article.
func (u *articleUsecase) Update(ctx context.Context, a *domain.Article) error {
	if a == nil {
		return errors.New("usecase: article must not be nil")
	}
	if a.ID == "" {
		return fmt.Errorf("usecase: invalid article id %q", a.ID)
	}
	stampPublishedAt(a)
	return u.repo.Update(ctx, a)
}

// Delete removes an article by its identifier.
func (u *articleUsecase) Delete(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("usecase: invalid article id %q", id)
	}
	return u.repo.Delete(ctx, id)
}

// stampPublishedAt sets PublishedAt to now when an article is being published
// without an explicit timestamp, so published articles always carry one.
func stampPublishedAt(a *domain.Article) {
	if a.Status == domain.ArticleStatusPublished && a.PublishedAt == nil {
		now := time.Now()
		a.PublishedAt = &now
	}
}
