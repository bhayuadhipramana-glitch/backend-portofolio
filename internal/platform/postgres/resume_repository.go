package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// resumesActiveLockKey serialises concurrent "make this resume active"
// transactions so the single-active partial unique index never trips.
const resumesActiveLockKey int64 = 7001002

// ResumeRepository implements domain.ResumeRepository on PostgreSQL.
type ResumeRepository struct {
	pool *pgxpool.Pool
}

// NewResumeRepository returns a ResumeRepository backed by pool.
func NewResumeRepository(pool *pgxpool.Pool) *ResumeRepository {
	return &ResumeRepository{pool: pool}
}

// resumeColumns is the column list shared by every SELECT/RETURNING so the
// scan order in scanResume cannot drift.
const resumeColumns = `id::text, name, file_url, content, is_active, created_at, updated_at`

func scanResume(row pgx.Row, res *domain.Resume) error {
	if err := row.Scan(&res.ID, &res.Name, &res.FileURL, &res.Content, &res.IsActive, &res.CreatedAt, &res.UpdatedAt); err != nil {
		return err
	}
	res.Content = orEmptyObject(res.Content)
	return nil
}

// Create inserts a resume. When res.IsActive is set, the previously active
// resume is deactivated in the same transaction.
func (r *ResumeRepository) Create(ctx context.Context, res *domain.Resume) error {
	err := pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		if res.IsActive {
			if err := acquireLock(ctx, tx, resumesActiveLockKey); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE resumes SET is_active = FALSE WHERE is_active`); err != nil {
				return err
			}
		}
		query := `
			INSERT INTO resumes (name, file_url, content, is_active)
			VALUES ($1, $2, $3, $4)
			RETURNING ` + resumeColumns
		row := tx.QueryRow(ctx, query, res.Name, res.FileURL, orEmptyObject(res.Content), res.IsActive)
		return scanResume(row, res)
	})
	if err != nil {
		return wrapWriteErr("create resume", err)
	}
	return nil
}

// List returns one page of resumes. Public reads only see the active resume;
// admin reads see all, active first.
func (r *ResumeRepository) List(ctx context.Context, vis domain.Visibility, page domain.Page) ([]domain.Resume, error) {
	page = page.Normalize()

	query := `SELECT ` + resumeColumns + ` FROM resumes ORDER BY is_active DESC, created_at DESC, id DESC LIMIT $1 OFFSET $2`
	if vis == domain.VisibilityPublic {
		query = `SELECT ` + resumeColumns + ` FROM resumes
			WHERE is_active ORDER BY created_at DESC, id DESC LIMIT $1 OFFSET $2`
	}

	rows, err := r.pool.Query(ctx, query, page.Limit, page.Offset)
	if err != nil {
		return nil, fmt.Errorf("list resumes query: %w", err)
	}
	defer rows.Close()

	resumes := make([]domain.Resume, 0, page.Limit)
	for rows.Next() {
		var res domain.Resume
		if err := scanResume(rows, &res); err != nil {
			return nil, fmt.Errorf("scan resume row: %w", err)
		}
		resumes = append(resumes, res)
	}
	return resumes, rows.Err()
}

// GetByID returns one resume, or domain.ErrNotFound. Public reads treat
// inactive resumes as missing.
func (r *ResumeRepository) GetByID(ctx context.Context, vis domain.Visibility, id string) (*domain.Resume, error) {
	query := `SELECT ` + resumeColumns + ` FROM resumes WHERE id = $1`
	if vis == domain.VisibilityPublic {
		query += ` AND is_active`
	}
	var res domain.Resume
	if err := scanResume(r.pool.QueryRow(ctx, query, id), &res); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("get resume id %s: %w", id, domain.ErrNotFound)
		}
		return nil, fmt.Errorf("get resume by id: %w", err)
	}
	return &res, nil
}

// Update replaces a resume, or returns domain.ErrNotFound. When res.IsActive
// is set, every other resume is deactivated in the same transaction.
func (r *ResumeRepository) Update(ctx context.Context, res *domain.Resume) error {
	err := pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		if res.IsActive {
			if err := acquireLock(ctx, tx, resumesActiveLockKey); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE resumes SET is_active = FALSE WHERE is_active AND id <> $1`, res.ID); err != nil {
				return err
			}
		}
		query := `
			UPDATE resumes
			SET name = $1, file_url = $2, content = $3, is_active = $4
			WHERE id = $5
			RETURNING ` + resumeColumns
		row := tx.QueryRow(ctx, query, res.Name, res.FileURL, orEmptyObject(res.Content), res.IsActive, res.ID)
		return scanResume(row, res)
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("update resume id %s: %w", res.ID, domain.ErrNotFound)
		}
		return wrapWriteErr("update resume", err)
	}
	return nil
}

// Delete removes a resume, or returns domain.ErrNotFound.
func (r *ResumeRepository) Delete(ctx context.Context, id string) error {
	commandTag, err := r.pool.Exec(ctx, `DELETE FROM resumes WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete resume query: %w", err)
	}
	if commandTag.RowsAffected() == 0 {
		return fmt.Errorf("delete resume id %s: %w", id, domain.ErrNotFound)
	}
	return nil
}
