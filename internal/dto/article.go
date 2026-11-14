package dto

import "time"

// CreateArticleRequest represents the expected JSON body for article creation.
type CreateArticleRequest struct {
	Slug        string     `json:"slug"         validate:"required,max=255,slug"`
	Title       string     `json:"title"        validate:"required,max=255"`
	Excerpt     string     `json:"excerpt"`
	Content     string     `json:"content"      validate:"required"`
	Tags        []string   `json:"tags"         validate:"dive,required,max=50"`
	Status      string     `json:"status"       validate:"required,oneof=draft published archived"`
	PublishedAt *time.Time `json:"published_at"`
}

// UpdateArticleRequest represents the expected JSON body for article updates.
// A PUT replaces the full resource representation.
type UpdateArticleRequest struct {
	Slug        string     `json:"slug"         validate:"required,max=255,slug"`
	Title       string     `json:"title"        validate:"required,max=255"`
	Excerpt     string     `json:"excerpt"`
	Content     string     `json:"content"      validate:"required"`
	Tags        []string   `json:"tags"         validate:"dive,required,max=50"`
	Status      string     `json:"status"       validate:"required,oneof=draft published archived"`
	PublishedAt *time.Time `json:"published_at"`
}
