package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// profilesActiveLockKey serialises concurrent "make this profile active"
// transactions so the single-active partial unique index never trips.
const profilesActiveLockKey int64 = 7001001

// ProfileRepository implements domain.ProfileRepository on PostgreSQL.
type ProfileRepository struct {
	pool *pgxpool.Pool
}

// NewProfileRepository returns a ProfileRepository backed by pool.
func NewProfileRepository(pool *pgxpool.Pool) *ProfileRepository {
	return &ProfileRepository{pool: pool}
}

// profileColumns is the column list shared by every SELECT/RETURNING so the
// scan order in scanProfile cannot drift.
const profileColumns = `id::text, name, headline, bio, avatar_url, social_links, is_active, created_at, updated_at`

func scanProfile(row pgx.Row, p *domain.Profile) error {
	if err := row.Scan(&p.ID, &p.Name, &p.Headline, &p.Bio, &p.AvatarURL, &p.SocialLinks, &p.IsActive, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return err
	}
	p.SocialLinks = orEmptyObject(p.SocialLinks)
	return nil
}

// Create inserts a profile. When p.IsActive is set, the previously active
// profile is deactivated in the same transaction.
func (r *ProfileRepository) Create(ctx context.Context, p *domain.Profile) error {
	err := pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		if p.IsActive {
			if err := acquireLock(ctx, tx, profilesActiveLockKey); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE profiles SET is_active = FALSE WHERE is_active`); err != nil {
				return err
			}
		}
		query := `
			INSERT INTO profiles (name, headline, bio, avatar_url, social_links, is_active)
			VALUES ($1, $2, $3, $4, $5, $6)
			RETURNING ` + profileColumns
		row := tx.QueryRow(ctx, query, p.Name, p.Headline, p.Bio, p.AvatarURL, orEmptyObject(p.SocialLinks), p.IsActive)
		return scanProfile(row, p)
	})
	if err != nil {
		return wrapWriteErr("create profile", err)
	}
	return nil
}

// List returns one page of profiles. Public reads only see the active
// profile; admin reads see all, active first.
func (r *ProfileRepository) List(ctx context.Context, vis domain.Visibility, page domain.Page) ([]domain.Profile, error) {
	page = page.Normalize()

	query := `SELECT ` + profileColumns + ` FROM profiles ORDER BY is_active DESC, created_at DESC, id DESC LIMIT $1 OFFSET $2`
	if vis == domain.VisibilityPublic {
		query = `SELECT ` + profileColumns + ` FROM profiles
			WHERE is_active ORDER BY created_at DESC, id DESC LIMIT $1 OFFSET $2`
	}

	rows, err := r.pool.Query(ctx, query, page.Limit, page.Offset)
	if err != nil {
		return nil, fmt.Errorf("list profiles query: %w", err)
	}
	defer rows.Close()

	profiles := make([]domain.Profile, 0, page.Limit)
	for rows.Next() {
		var p domain.Profile
		if err := scanProfile(rows, &p); err != nil {
			return nil, fmt.Errorf("scan profile row: %w", err)
		}
		profiles = append(profiles, p)
	}
	return profiles, rows.Err()
}

// GetByID returns one profile, or domain.ErrNotFound. Public reads treat
// inactive profiles as missing.
func (r *ProfileRepository) GetByID(ctx context.Context, vis domain.Visibility, id string) (*domain.Profile, error) {
	query := `SELECT ` + profileColumns + ` FROM profiles WHERE id = $1`
	if vis == domain.VisibilityPublic {
		query += ` AND is_active`
	}
	var p domain.Profile
	if err := scanProfile(r.pool.QueryRow(ctx, query, id), &p); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("get profile id %s: %w", id, domain.ErrNotFound)
		}
		return nil, fmt.Errorf("get profile by id: %w", err)
	}
	return &p, nil
}

// Update replaces a profile, or returns domain.ErrNotFound. When p.IsActive is
// set, every other profile is deactivated in the same transaction.
func (r *ProfileRepository) Update(ctx context.Context, p *domain.Profile) error {
	err := pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		if p.IsActive {
			if err := acquireLock(ctx, tx, profilesActiveLockKey); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE profiles SET is_active = FALSE WHERE is_active AND id <> $1`, p.ID); err != nil {
				return err
			}
		}
		query := `
			UPDATE profiles
			SET name = $1, headline = $2, bio = $3, avatar_url = $4, social_links = $5, is_active = $6
			WHERE id = $7
			RETURNING ` + profileColumns
		row := tx.QueryRow(ctx, query, p.Name, p.Headline, p.Bio, p.AvatarURL, orEmptyObject(p.SocialLinks), p.IsActive, p.ID)
		return scanProfile(row, p)
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("update profile id %s: %w", p.ID, domain.ErrNotFound)
		}
		return wrapWriteErr("update profile", err)
	}
	return nil
}

// Delete removes a profile, or returns domain.ErrNotFound.
func (r *ProfileRepository) Delete(ctx context.Context, id string) error {
	commandTag, err := r.pool.Exec(ctx, `DELETE FROM profiles WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete profile query: %w", err)
	}
	if commandTag.RowsAffected() == 0 {
		return fmt.Errorf("delete profile id %s: %w", id, domain.ErrNotFound)
	}
	return nil
}
