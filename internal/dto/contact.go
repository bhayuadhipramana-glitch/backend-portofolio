package dto

import "strings"

// ContactRequest is the body of POST /api/v1/contact. Website is a honeypot:
// real visitors never see the field, bots fill it.
type ContactRequest struct {
	Name    string `json:"name"    validate:"required,notblank,max=120"`
	Email   string `json:"email"   validate:"required,email,max=254"`
	Subject string `json:"subject" validate:"required,notblank,max=200"`
	Message string `json:"message" validate:"required,min=10,max=4000"`
	Website string `json:"website"`
}

// IsSpam reports whether the honeypot field was filled.
func (r ContactRequest) IsSpam() bool { return strings.TrimSpace(r.Website) != "" }

// Normalize trims surrounding whitespace from every text field.
func (r *ContactRequest) Normalize() {
	r.Name = strings.TrimSpace(r.Name)
	r.Email = strings.TrimSpace(r.Email)
	r.Subject = strings.TrimSpace(r.Subject)
	r.Message = strings.TrimSpace(r.Message)
}
