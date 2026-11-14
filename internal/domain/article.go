package domain

import (
	"context"
	"errors"
	"time"
)

// ErrConflict is returned when a write violates a uniqueness constraint.
var ErrConflict = errors.New("entity already exists")

// Article status values, mirrored by the chk_articles_status constraint.
const (
	ArticleStatusDraft     = "draft"
	ArticleStatusPublished = "published"
	ArticleStatusArchived  = "archived"
)

// Article is a blog post / written piece of the portfolio.
type Article struct {
	ID          string     `json:"id"`
	Slug        string     `json:"slug"`
	Title       string     `json:"title"`
	Excerpt     string     `json:"excerpt"`
	Content     string     `json:"content"`
	Tags        []string   `json:"tags"`
	Status      string     `json:"status"`
	PublishedAt *time.Time `json:"published_at"` // nil until the article is published
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// ArticleRepository is the persistence contract for articles. Reads take a
// Visibility: VisibilityPublic must only ever return published articles.
type ArticleRepository interface {
	Create(ctx context.Context, a *Article) error
	List(ctx context.Context, vis Visibility, page Page) ([]Article, error)
	GetByID(ctx context.Context, vis Visibility, id string) (*Article, error)
	GetBySlug(ctx context.Context, vis Visibility, slug string) (*Article, error)
	Update(ctx context.Context, a *Article) error
	Delete(ctx context.Context, id string) error
}
