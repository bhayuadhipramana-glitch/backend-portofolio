// Package dto defines Data Transfer Objects for inbound HTTP requests.
//
// Each DTO carries struct-level validation tags consumed by the
// go-playground/validator library. The Validate function translates
// validator.FieldError values into a map that is safe and helpful
// to return in an API error response.
package dto

import (
	"fmt"
	"strings"
	"sync"

	"github.com/go-playground/validator/v10"
)

// validate is a package-level, concurrency-safe validator instance.
// We initialise it once via sync.Once to avoid repeated allocations.
var (
	once     sync.Once
	validate *validator.Validate
)

// getValidator returns the singleton validator instance.
func getValidator() *validator.Validate {
	once.Do(func() {
		validate = validator.New(validator.WithRequiredStructEnabled())
	})
	return validate
}

// FieldError is a single validation error for one struct field.
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// Validate runs struct-tag validation on v and returns a slice of FieldError
// when validation fails. Returns nil when the struct is valid.
func Validate(v any) []FieldError {
	err := getValidator().Struct(v)
	if err == nil {
		return nil
	}

	var errs []FieldError
	for _, fe := range err.(validator.ValidationErrors) {
		errs = append(errs, FieldError{
			Field:   jsonFieldName(fe.Field()),
			Message: msgForTag(fe),
		})
	}
	return errs
}

// ---------- Request DTOs ----------

// CreateProjectRequest represents the expected JSON body for project creation.
type CreateProjectRequest struct {
	Title       string   `json:"title"       validate:"required,max=150"`
	Description string   `json:"description" validate:"required"`
	TechStack   []string `json:"tech_stack"  validate:"required,min=1,dive,required"`
}

// UpdateProjectRequest represents the expected JSON body for project updates.
// All fields are required—a PUT replaces the full resource representation.
type UpdateProjectRequest struct {
	Title       string   `json:"title"       validate:"required,max=150"`
	Description string   `json:"description" validate:"required"`
	TechStack   []string `json:"tech_stack"  validate:"required,min=1,dive,required"`
}

// ---------- internal helpers ----------

// jsonFieldName converts a Go struct field name to its snake_case JSON
// counterpart using a simple heuristic (lowercase first letter, insert
// underscore before uppercase letters). This keeps error messages aligned
// with the JSON keys consumers actually send.
func jsonFieldName(field string) string {
	var b strings.Builder
	for i, r := range field {
		if i > 0 && r >= 'A' && r <= 'Z' {
			b.WriteByte('_')
		}
		b.WriteRune(r)
	}
	return strings.ToLower(b.String())
}

// msgForTag returns a human-readable message for each validation tag.
func msgForTag(fe validator.FieldError) string {
	switch fe.Tag() {
	case "required":
		return fmt.Sprintf("%s is required", jsonFieldName(fe.Field()))
	case "max":
		return fmt.Sprintf("%s must be at most %s characters", jsonFieldName(fe.Field()), fe.Param())
	case "min":
		return fmt.Sprintf("%s must have at least %s item(s)", jsonFieldName(fe.Field()), fe.Param())
	default:
		return fmt.Sprintf("%s failed on '%s' validation", jsonFieldName(fe.Field()), fe.Tag())
	}
}
