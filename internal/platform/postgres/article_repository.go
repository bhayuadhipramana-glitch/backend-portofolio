package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ArticleRepository implements domain.ArticleRepository on PostgreSQL.
type ArticleRepository struct {
	pool *pgxpool.Pool
}

// NewArticleRepository returns an ArticleRepository backed by pool.
func NewArticleRepository(pool *pgxpool.Pool) *ArticleRepository {
	return &ArticleRepository{pool: pool}
}

// articleColumns is the column list shared by every SELECT/RETURNING so the
// scan order in scanArticle cannot drift.
const articleColumns = `id::text, slug, title, excerpt, content, tags, status, published_at, created_at, updated_at`

func scanArticle(row pgx.Row, a *domain.Article) error {
	if err := row.Scan(&a.ID, &a.Slug, &a.Title, &a.Excerpt, &a.Content, &a.Tags, &a.Status, &a.PublishedAt, &a.CreatedAt, &a.UpdatedAt); err != nil {
		return err
	}
	a.Tags = orEmptyStrings(a.Tags)
	return nil
}

// Create inserts an article and fills in its generated id and timestamps.
func (r *ArticleRepository) Create(ctx context.Context, a *domain.Article) error {
	query := `
		INSERT INTO articles (slug, title, excerpt, content, tags, status, published_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING ` + articleColumns
	row := r.pool.QueryRow(ctx, query, a.Slug, a.Title, a.Excerpt, a.Content, orEmptyStrings(a.Tags), a.Status, a.PublishedAt)
	if err := scanArticle(row, a); err != nil {
		return wrapWriteErr("create article", err)
	}
	return nil
}

// List returns one page of articles. Public reads only see published articles,
// newest first (served by idx_articles_published); admin reads see every status.
func (r *ArticleRepository) List(ctx context.Context, vis domain.Visibility, page domain.Page) ([]domain.Article, error) {
	page = page.Normalize()

	query := `SELECT ` + articleColumns + ` FROM articles ORDER BY created_at DESC, id DESC LIMIT $1 OFFSET $2`
	if vis == domain.VisibilityPublic {
		query = `SELECT ` + articleColumns + ` FROM articles
			WHERE status = 'published'
			ORDER BY published_at DESC, id DESC LIMIT $1 OFFSET $2`
	}

	rows, err := r.pool.Query(ctx, query, page.Limit, page.Offset)
	if err != nil {
		return nil, fmt.Errorf("list articles query: %w", err)
	}
	defer rows.Close()

	articles := make([]domain.Article, 0, page.Limit)
	for rows.Next() {
		var a domain.Article
		if err := scanArticle(rows, &a); err != nil {
			return nil, fmt.Errorf("scan article row: %w", err)
		}
		articles = append(articles, a)
	}
	return articles, rows.Err()
}

// GetByID returns one article, or domain.ErrNotFound. Public reads treat
// unpublished articles as missing.
func (r *ArticleRepository) GetByID(ctx context.Context, vis domain.Visibility, id string) (*domain.Article, error) {
	query := `SELECT ` + articleColumns + ` FROM articles WHERE id = $1`
	if vis == domain.VisibilityPublic {
		query += ` AND status = 'published'`
	}
	var a domain.Article
	if err := scanArticle(r.pool.QueryRow(ctx, query, id), &a); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("get article id %s: %w", id, domain.ErrNotFound)
		}
		return nil, fmt.Errorf("get article by id: %w", err)
	}
	return &a, nil
}

// GetBySlug returns one article by its slug, or domain.ErrNotFound. Public
// reads treat unpublished articles as missing.
func (r *ArticleRepository) GetBySlug(ctx context.Context, vis domain.Visibility, slug string) (*domain.Article, error) {
	query := `SELECT ` + articleColumns + ` FROM articles WHERE slug = $1`
	if vis == domain.VisibilityPublic {
		query += ` AND status = 'published'`
	}
	var a domain.Article
	if err := scanArticle(r.pool.QueryRow(ctx, query, slug), &a); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("get article slug %s: %w", slug, domain.ErrNotFound)
		}
		return nil, fmt.Errorf("get article by slug: %w", err)
	}
	return &a, nil
}

// Update replaces an article, or returns domain.ErrNotFound.
func (r *ArticleRepository) Update(ctx context.Context, a *domain.Article) error {
	query := `
		UPDATE articles
		SET slug = $1, title = $2, excerpt = $3, content = $4, tags = $5, status = $6, published_at = $7
		WHERE id = $8
		RETURNING ` + articleColumns
	row := r.pool.QueryRow(ctx, query, a.Slug, a.Title, a.Excerpt, a.Content, orEmptyStrings(a.Tags), a.Status, a.PublishedAt, a.ID)
	if err := scanArticle(row, a); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("update article id %s: %w", a.ID, domain.ErrNotFound)
		}
		return wrapWriteErr("update article", err)
	}
	return nil
}

// Delete removes an article, or returns domain.ErrNotFound.
func (r *ArticleRepository) Delete(ctx context.Context, id string) error {
	commandTag, err := r.pool.Exec(ctx, `DELETE FROM articles WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete article query: %w", err)
	}
	if commandTag.RowsAffected() == 0 {
		return fmt.Errorf("delete article id %s: %w", id, domain.ErrNotFound)
	}
	return nil
}
