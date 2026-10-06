// Package response provides standardized JSON response helpers for HTTP handlers.
//
// Every API response follows a consistent envelope so that consumers can rely on
// a stable contract regardless of whether the request succeeded or failed.
//
// Success envelope:
//
//	{ "success": true, "message": "string", "data": any }
//
// Error envelope:
//
//	{ "success": false, "message": "string", "errors": any }
package response

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// SuccessBody is the canonical envelope returned on successful operations.
type SuccessBody struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// ErrorBody is the canonical envelope returned when an operation fails.
type ErrorBody struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Errors  any    `json:"errors,omitempty"`
}

// JSON encodes an arbitrary payload as JSON and writes it to w.
// This is the lowest-level helper—prefer Success or Error for consistency.
func JSON(w http.ResponseWriter, statusCode int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(statusCode)

	if err := json.NewEncoder(w).Encode(payload); err != nil {
		// At this point headers have already been sent, so we can only log.
		slog.Error("response: failed to encode JSON", "error", err)
	}
}

// Success writes a standardised success response.
//
//	response.Success(w, http.StatusOK, "projects fetched", projects)
func Success(w http.ResponseWriter, statusCode int, message string, data any) {
	JSON(w, statusCode, SuccessBody{
		Success: true,
		Message: message,
		Data:    data,
	})
}

// Error writes a standardised error response.
//
//	response.Error(w, http.StatusBadRequest, "validation failed", validationErrs)
func Error(w http.ResponseWriter, statusCode int, message string, errs any) {
	JSON(w, statusCode, ErrorBody{
		Success: false,
		Message: message,
		Errors:  errs,
	})
}
