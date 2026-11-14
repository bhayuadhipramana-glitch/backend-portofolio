package domain

import (
	"context"
	"encoding/json"
	"time"
)

// Resume is a CV. At most one resume is active; the repository switches the
// active one atomically.
type Resume struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	FileURL   string          `json:"file_url"`
	Content   json.RawMessage `json:"content"` // JSONB object
	IsActive  bool            `json:"is_active"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// ResumeRepository is the persistence contract for resumes. Reads take a
// Visibility: VisibilityPublic must only ever return the active resume.
// Create and Update with IsActive set deactivate any other active resume in
// the same transaction.
type ResumeRepository interface {
	Create(ctx context.Context, r *Resume) error
	List(ctx context.Context, vis Visibility, page Page) ([]Resume, error)
	GetByID(ctx context.Context, vis Visibility, id string) (*Resume, error)
	Update(ctx context.Context, r *Resume) error
	Delete(ctx context.Context, id string) error
}
