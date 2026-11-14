package dto

import "encoding/json"

// CreateProfileRequest represents the expected JSON body for profile creation.
type CreateProfileRequest struct {
	Name        string          `json:"name"         validate:"required,max=150"`
	Headline    string          `json:"headline"     validate:"max=255"`
	Bio         string          `json:"bio"`
	AvatarURL   string          `json:"avatar_url"   validate:"omitempty,url,max=500"`
	SocialLinks json.RawMessage `json:"social_links" validate:"omitempty,jsonobject"`
	IsActive    bool            `json:"is_active"`
}

// UpdateProfileRequest represents the expected JSON body for profile updates.
// A PUT replaces the full resource representation.
type UpdateProfileRequest struct {
	Name        string          `json:"name"         validate:"required,max=150"`
	Headline    string          `json:"headline"     validate:"max=255"`
	Bio         string          `json:"bio"`
	AvatarURL   string          `json:"avatar_url"   validate:"omitempty,url,max=500"`
	SocialLinks json.RawMessage `json:"social_links" validate:"omitempty,jsonobject"`
	IsActive    bool            `json:"is_active"`
}
