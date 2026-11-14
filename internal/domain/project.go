package domain

import (
	"context"
	"errors"
	"time"
)

// ErrNotFound is returned when a requested entity does not exist.
var ErrNotFound = errors.New("entity not found")

// Project is a portfolio project entry.
type Project struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	TechStack   []string  `json:"tech_stack"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ProjectRepository is the persistence contract for projects.
type ProjectRepository interface {
	Create(ctx context.Context, p *Project) error
	List(ctx context.Context, page Page) ([]Project, error)
	GetByID(ctx context.Context, id string) (*Project, error)
	Update(ctx context.Context, p *Project) error
	Delete(ctx context.Context, id string) error
}
