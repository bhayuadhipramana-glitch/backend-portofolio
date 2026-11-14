package dto

import "encoding/json"

// CreateResumeRequest represents the expected JSON body for resume creation.
type CreateResumeRequest struct {
	Name     string          `json:"name"      validate:"required,max=150"`
	FileURL  string          `json:"file_url"  validate:"omitempty,url,max=500"`
	Content  json.RawMessage `json:"content"   validate:"omitempty,jsonobject"`
	IsActive bool            `json:"is_active"`
}

// UpdateResumeRequest represents the expected JSON body for resume updates.
// A PUT replaces the full resource representation.
type UpdateResumeRequest struct {
	Name     string          `json:"name"      validate:"required,max=150"`
	FileURL  string          `json:"file_url"  validate:"omitempty,url,max=500"`
	Content  json.RawMessage `json:"content"   validate:"omitempty,jsonobject"`
	IsActive bool            `json:"is_active"`
}
