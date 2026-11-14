package domain

import (
	"context"
	"encoding/json"
	"time"
)

// Profile is the identity of the portfolio owner. At most one profile is
// active; the repository switches the active one atomically.
type Profile struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Headline    string          `json:"headline"`
	Bio         string          `json:"bio"`
	AvatarURL   string          `json:"avatar_url"`
	SocialLinks json.RawMessage `json:"social_links"` // JSONB object
	IsActive    bool            `json:"is_active"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

// ProfileRepository is the persistence contract for profiles. Reads take a
// Visibility: VisibilityPublic must only ever return the active profile.
// Create and Update with IsActive set deactivate any other active profile in
// the same transaction.
type ProfileRepository interface {
	Create(ctx context.Context, p *Profile) error
	List(ctx context.Context, vis Visibility, page Page) ([]Profile, error)
	GetByID(ctx context.Context, vis Visibility, id string) (*Profile, error)
	Update(ctx context.Context, p *Profile) error
	Delete(ctx context.Context, id string) error
}
