package postgres

import (
	"context"
	"fmt"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ProjectRepository struct {
	pool *pgxpool.Pool
}

func NewProjectRepository(pool *pgxpool.Pool) *ProjectRepository {
	return &ProjectRepository{pool: pool}
}

func (r *ProjectRepository) Create(ctx context.Context, p *domain.Project) error {
	query := `
		INSERT INTO projects (title, description, tech_stack)
		VALUES ($1, $2, $3)
		RETURNING id, created_at
	`
	err := r.pool.QueryRow(ctx, query, p.Title, p.Description, p.TechStack).Scan(&p.ID, &p.CreatedAt)
	if err != nil {
		return fmt.Errorf("create project query: %w", err)
	}
	return nil
}

func (r *ProjectRepository) List(ctx context.Context) ([]domain.Project, error) {
	query := `
		SELECT id, title, description, tech_stack, created_at
		FROM projects
		ORDER BY created_at DESC
	`
	rows, err := r.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list projects query: %w", err)
	}
	defer rows.Close()

	var projects []domain.Project
	for rows.Next() {
		var p domain.Project
		if err := rows.Scan(&p.ID, &p.Title, &p.Description, &p.TechStack, &p.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan project row: %w", err)
		}
		projects = append(projects, p)
	}
	return projects, rows.Err()
}

// Mengambil satu data spesifik berdasarkan ID
func (r *ProjectRepository) GetByID(ctx context.Context, id int) (*domain.Project, error) {
	query := `
		SELECT id, title, description, tech_stack, created_at
		FROM projects
		WHERE id = $1
	`
	var p domain.Project
	err := r.pool.QueryRow(ctx, query, id).Scan(&p.ID, &p.Title, &p.Description, &p.TechStack, &p.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("get project by id: %w", err)
	}
	return &p, nil
}

// Memperbarui data yang sudah ada
func (r *ProjectRepository) Update(ctx context.Context, p *domain.Project) error {
	query := `
		UPDATE projects
		SET title = $1, description = $2, tech_stack = $3
		WHERE id = $4
	`
	commandTag, err := r.pool.Exec(ctx, query, p.Title, p.Description, p.TechStack, p.ID)
	if err != nil {
		return fmt.Errorf("update project query: %w", err)
	}
	if commandTag.RowsAffected() == 0 {
		return fmt.Errorf("project with id %d not found", p.ID)
	}
	return nil
}

// Menghapus data dari database
func (r *ProjectRepository) Delete(ctx context.Context, id int) error {
	query := `DELETE FROM projects WHERE id = $1`
	commandTag, err := r.pool.Exec(ctx, query, id)
	if err != nil {
		return fmt.Errorf("delete project query: %w", err)
	}
	if commandTag.RowsAffected() == 0 {
		return fmt.Errorf("project with id %d not found", id)
	}
	return nil
}