package domain

import (
	"context"
	"time"
)

// Project adalah entitas data portofolio
type Project struct {
	ID          int       `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	TechStack   []string  `json:"tech_stack"`
	CreatedAt   time.Time `json:"created_at"`
}

// ProjectRepository adalah kontrak interface yang wajib dipenuhi oleh database adapter
type ProjectRepository interface {
	Create(ctx context.Context, p *Project) error
	List(ctx context.Context) ([]Project, error)
	GetByID(ctx context.Context, id int) (*Project, error)
	Update(ctx context.Context, p *Project) error
	Delete(ctx context.Context, id int) error
}
