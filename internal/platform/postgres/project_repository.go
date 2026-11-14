package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ProjectRepository implements domain.ProjectRepository on PostgreSQL.
type ProjectRepository struct {
	pool *pgxpool.Pool
}

// NewProjectRepository returns a ProjectRepository backed by pool.
func NewProjectRepository(pool *pgxpool.Pool) *ProjectRepository {
	return &ProjectRepository{pool: pool}
}

// projectColumns is the column list shared by every SELECT/RETURNING so the
// scan order in scanProject cannot drift.
const projectColumns = `id::text, title, description, tech_stack, created_at, updated_at`

func scanProject(row pgx.Row, p *domain.Project) error {
	if err := row.Scan(&p.ID, &p.Title, &p.Description, &p.TechStack, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return err
	}
	p.TechStack = orEmptyStrings(p.TechStack)
	return nil
}

// Create inserts a project and fills in its generated id and timestamps.
func (r *ProjectRepository) Create(ctx context.Context, p *domain.Project) error {
	query := `
		INSERT INTO projects (title, description, tech_stack)
		VALUES ($1, $2, $3)
		RETURNING ` + projectColumns
	if err := scanProject(r.pool.QueryRow(ctx, query, p.Title, p.Description, orEmptyStrings(p.TechStack)), p); err != nil {
		return fmt.Errorf("create project query: %w", err)
	}
	return nil
}

// List returns one page of projects, newest first (served by idx_projects_created_at).
func (r *ProjectRepository) List(ctx context.Context, page domain.Page) ([]domain.Project, error) {
	page = page.Normalize()
	query := `SELECT ` + projectColumns + ` FROM projects ORDER BY created_at DESC, id DESC LIMIT $1 OFFSET $2`
	rows, err := r.pool.Query(ctx, query, page.Limit, page.Offset)
	if err != nil {
		return nil, fmt.Errorf("list projects query: %w", err)
	}
	defer rows.Close()

	projects := make([]domain.Project, 0, page.Limit)
	for rows.Next() {
		var p domain.Project
		if err := scanProject(rows, &p); err != nil {
			return nil, fmt.Errorf("scan project row: %w", err)
		}
		projects = append(projects, p)
	}
	return projects, rows.Err()
}

// GetByID returns one project, or domain.ErrNotFound.
func (r *ProjectRepository) GetByID(ctx context.Context, id string) (*domain.Project, error) {
	query := `SELECT ` + projectColumns + ` FROM projects WHERE id = $1`
	var p domain.Project
	if err := scanProject(r.pool.QueryRow(ctx, query, id), &p); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("get project id %s: %w", id, domain.ErrNotFound)
		}
		return nil, fmt.Errorf("get project by id: %w", err)
	}
	return &p, nil
}

// Update replaces a project, or returns domain.ErrNotFound.
func (r *ProjectRepository) Update(ctx context.Context, p *domain.Project) error {
	query := `
		UPDATE projects
		SET title = $1, description = $2, tech_stack = $3
		WHERE id = $4
		RETURNING ` + projectColumns
	if err := scanProject(r.pool.QueryRow(ctx, query, p.Title, p.Description, orEmptyStrings(p.TechStack), p.ID), p); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("update project id %s: %w", p.ID, domain.ErrNotFound)
		}
		return fmt.Errorf("update project query: %w", err)
	}
	return nil
}

// Delete removes a project, or returns domain.ErrNotFound.
func (r *ProjectRepository) Delete(ctx context.Context, id string) error {
	commandTag, err := r.pool.Exec(ctx, `DELETE FROM projects WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete project query: %w", err)
	}
	if commandTag.RowsAffected() == 0 {
		return fmt.Errorf("delete project id %s: %w", id, domain.ErrNotFound)
	}
	return nil
}
